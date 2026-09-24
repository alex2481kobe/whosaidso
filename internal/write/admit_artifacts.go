package write

// Accepted-artifact resolution and byte verification, and handing verified
// bytes to publication, live here. Durable publication itself (store),
// admission policy and transaction ownership do not.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"

	"datum/internal/evidence"
	"datum/internal/model"
	"datum/internal/store"
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
				own, err := runAdmitOutputs(project.Root, project.ArtifactDir(), inbox, packet, seal.Envelope, dry)
				if err != nil {
					return err
				}
				for _, out := range own {
					if err := ctx.Err(); err != nil {
						return err
					}
					if out.ref.Git != nil {
						// Publish the proven bytes; the generic path below
						// still corroborates the git pin.
						if err := preserve(project.Root, project.ArtifactDir(), out.bytes); err != nil {
							return err
						}
						continue
					}
					resolved, err := resolver.RunOutput(out.ref, out.bytes)
					if err != nil {
						return fmt.Errorf("accepted support is unavailable or invalid: %w", err)
					}
					if err := admissionReadable(resolved, out.ref.Selector); err != nil {
						return err
					}
					// The proven bytes themselves are published, never a copy
					// found again by digest.
					if err := preserve(project.Root, project.ArtifactDir(), out.bytes); err != nil {
						return err
					}
					published = append(published, out.ref)
				}
			}
			for _, ref := range admissionArtifacts(event) {
				if err := ctx.Err(); err != nil {
					return err
				}
				if admissionPublished(published, ref) {
					continue
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

// admissionPublished reports whether ref is exactly a run output whose proven
// bytes were already verified and published for this event.
func admissionPublished(published []model.ArtifactRef, ref model.ArtifactRef) bool {
	for _, p := range published {
		if reflect.DeepEqual(p, ref) {
			return true
		}
	}
	return false
}

// preserveAdmissionBlob is admission's preserver: the store's durable,
// content-addressed publication (a dry run records instead).
func preserveAdmissionBlob(root, artifactDir string, data []byte) error {
	return store.PublishArtifact(root, artifactDir, data)
}
