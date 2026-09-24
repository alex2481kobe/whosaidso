package write

// Accepted-artifact resolution and byte verification, handing verified bytes
// to publication, and checking that a home still holds what admission kept
// live here. Durable publication itself (store), admission policy and
// transaction ownership do not.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"whosaidso/internal/evidence"
	"whosaidso/internal/model"
	"whosaidso/internal/store"
)

// materializeAdmission resolves every accepted artifact and hands verified
// bytes to preserve: the store on admission, the dry run's staging otherwise.
func materializeAdmission(ctx context.Context, project store.Project, packets []model.Packet, dry *dryRun) error {
	preserve := dry.preserver()
	inbox, err := store.IntakeDir(project)
	if err != nil {
		return err
	}
	resolver := dry.resolver(project)
	for _, packet := range packets {
		for _, raw := range packet.Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				return err
			}
			var published []model.ArtifactRef
			if seal, ok := event.(*model.InvocationSeal); ok {
				own, err := runAdmitOutputs(inbox, packet, seal.Envelope, dry)
				if err != nil {
					return err
				}
				for _, out := range own {
					if err := ctx.Err(); err != nil {
						return err
					}
					resolved, err := resolver.RunOutput(out.out, out.bytes)
					if err != nil {
						return fmt.Errorf("accepted support is unavailable or invalid: %w", err)
					}
					if err := admissionReadable(resolved, model.Selector{Kind: "whole"}); err != nil {
						return err
					}
					// The proven bytes themselves are published, never a copy
					// found again by digest.
					if err := preserve(project.Root, project.ArtifactDir(), out.bytes); err != nil {
						return err
					}
					published = append(published, out.out.Ref())
				}
			}
			for _, ref := range admissionArtifacts(event) {
				if err := ctx.Err(); err != nil {
					return err
				}
				if slices.ContainsFunc(published, func(p model.ArtifactRef) bool { return reflect.DeepEqual(p, ref) }) {
					continue // a run output, verified and published above
				}
				if ref.Content != nil {
					for _, candidate := range packets {
						path := filepath.Join(inbox, string(candidate.CommandID), "blobs", string(ref.Content.SHA256))
						blob, err := dry.packetBlob(inbox, candidate.CommandID, ref.Content.SHA256)
						if os.IsNotExist(err) {
							continue
						}
						if err != nil {
							return err
						}
						if model.HashBytes(blob) != ref.Content.SHA256 || uint64(len(blob)) != ref.Content.Length {
							return admissionFault("conflict", path, "intake bytes disagree with the accepted content pin")
						}
						if err := preserve(project.Root, project.ArtifactDir(), blob); err != nil {
							return err
						}
						break
					}
				}
				resolved, err := resolver.Resolve(ctx, ref)
				if err != nil {
					return fmt.Errorf("accepted support is unavailable or invalid: %w", err)
				}
				// A working locator can disappear with its producer. Keep its verified
				// bytes without adding our location to the authored event.
				if resolved.Origin == evidence.OriginLocator {
					if err := preserve(project.Root, project.ArtifactDir(), resolved.Bytes); err != nil {
						return err
					}
				}
				if err := admissionReadable(resolved, ref.Selector); err != nil {
					return err
				}
				if source, ok := event.(*model.SourceIntake); ok && (resolved.SHA256 != source.OriginalDigest || resolved.Length != source.Length) {
					return admissionFault("conflict", "source_ref", "resolved source differs from the captured original")
				}
			}
		}
	}
	return nil
}

func admissionBlob(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, admissionFault("invalid-field", path, "artifact must be a regular file, never a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, evidence.DefaultMaxBytes+1))
	closeErr := f.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(data)) > evidence.DefaultMaxBytes {
		return nil, admissionFault("unavailable", path, "artifact exceeds the resolver byte limit")
	}
	return data, nil
}

// admissionReadable refuses accepted support its own selector cannot read.
func admissionReadable(resolved evidence.ResolvedArtifact, selector model.Selector) error {
	reading, err := evidence.Select(resolved, selector)
	if err != nil {
		return err
	}
	if reading.Kind == evidence.ReadingAbsent {
		return admissionFault("unavailable", "artifact.selector", reading.Reason)
	}
	return nil
}

// preserveAdmissionBlob is admission's preserver: the store's durable,
// content-addressed publication (a dry run records instead).
func preserveAdmissionBlob(root, artifactDir string, data []byte) error {
	return store.PublishArtifact(root, artifactDir, data)
}

// HomeHoldsKeptArtifacts refuses a home whose artifact store does not hold
// every artifact its admitted ledger keeps there. Admission keeps the verified
// bytes of every content artifact an admitted event cites (materializeAdmission),
// so each must resolve from the store alone, locators set aside. Two exceptions,
// both from the ledger itself: a git artifact resolves from the repository, and
// a recorded disposal says those bytes may be gone. Each refusal names the digest,
// every admitted event citing it (bundle, event index, type) and the resolver's
// reason, which tells missing bytes from different ones.
func HomeHoldsKeptArtifacts(ctx context.Context, home store.Project, state store.State) error {
	bundles, err := state.Bundles()
	if err != nil {
		return err
	}
	snapshot := state.Snapshot()
	resolver := evidence.NewResolverAt(home.Root, home.ArtifactDir())
	reasons := map[model.Digest]string{}
	citers := map[model.Digest][]string{}
	var missing []model.Digest
	for _, bundle := range bundles {
		for i, raw := range bundle.Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				return err
			}
			citer := fmt.Sprintf("bundle %d event %d (%s)", bundle.Sequence, i, raw.Type)
			for _, ref := range admissionArtifacts(event) {
				if ref.Kind != "content" || disposed(snapshot, ref) {
					continue
				}
				digest := ref.Content.SHA256
				if _, checked := reasons[digest]; !checked {
					kept := model.ArtifactRef{Kind: "content", Selector: model.Selector{Kind: "whole"}, Content: &model.ContentPin{
						SHA256: digest, Length: ref.Content.Length, MediaType: ref.Content.MediaType, Locators: []model.Locator{}}}
					reasons[digest] = ""
					if _, err := resolver.Resolve(ctx, kept); err != nil {
						reasons[digest] = err.Error()
						missing = append(missing, digest)
					}
				}
				if reasons[digest] != "" && !slices.Contains(citers[digest], citer) {
					citers[digest] = append(citers[digest], citer)
				}
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	lines := make([]string, len(missing))
	for i, digest := range missing {
		lines[i] = fmt.Sprintf("sha256 %s cited by %s: %s", digest, strings.Join(citers[digest], ", "), reasons[digest])
	}
	return admissionFault("home-evidence-missing", home.ArtifactDir(), fmt.Sprintf(
		"%s does not hold %d artifact(s) its admitted records cite; commit the whole .whosaidso folder (events and artifacts) together:\n  %s",
		home.Root, len(missing), strings.Join(lines, "\n  ")))
}
