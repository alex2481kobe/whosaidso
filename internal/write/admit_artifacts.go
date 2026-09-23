package write

// Accepted-artifact resolution, byte verification, and durable preservation live here.
// Admission policy and transaction ownership do not.
// This file stays below 200 lines because artifact preservation is one complete responsibility.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"datum/internal/evidence"
	"datum/internal/model"
	"datum/internal/store"
)

// materializeAdmission resolves every accepted artifact and hands verified
// bytes to preserve: preserveAdmissionBlob on admission, a recorder on a dry run.
func materializeAdmission(ctx context.Context, project store.Project, packets []model.Packet, preserve func(root, artifactDir string, data []byte) error) error {
	inbox, err := store.IntakeDir(project)
	if err != nil {
		return err
	}
	resolver := evidence.NewResolverAt(project.Root, project.ArtifactDir())
	for _, packet := range packets {
		for _, raw := range packet.Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				return err
			}
			if seal, ok := event.(*model.InvocationSeal); ok {
				if err := runAdmitOutputs(project.Root, project.ArtifactDir(), inbox, packet, seal.Envelope); err != nil {
					return err
				}
			}
			for _, ref := range admissionArtifacts(event) {
				if err := ctx.Err(); err != nil {
					return err
				}
				if ref.Content != nil {
					for _, candidate := range packets {
						path := filepath.Join(inbox, string(candidate.CommandID), "blobs", string(ref.Content.SHA256))
						blob, err := admissionBlob(path)
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
				reading, err := evidence.Select(resolved, ref.Selector)
				if err != nil {
					return err
				}
				if reading.Kind == evidence.ReadingAbsent {
					return admissionFault("unavailable", "artifact.selector", reading.Reason)
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

func preserveAdmissionBlob(root, artifactDir string, data []byte) error {
	if !filepath.IsAbs(root) {
		return admissionFault("invalid-field", "project.root", "artifact publication needs an absolute project root")
	}
	dir := root
	for _, part := range strings.Split(artifactDir, "/") {
		parent := dir
		dir = filepath.Join(dir, part)
		if err := os.Mkdir(dir, 0755); err != nil && !os.IsExist(err) {
			return err
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return admissionFault("invalid-field", dir, "artifact directories cannot be symlinks")
		}
		if err := syncAdmissionPath(parent); err != nil {
			return err
		}
	}
	final := filepath.Join(dir, string(model.HashBytes(data)))
	if prior, err := admissionBlob(final); err == nil {
		if !bytes.Equal(prior, data) {
			return admissionFault("conflict", final, "existing artifact bytes do not match their name")
		}
		if err := syncAdmissionPath(final); err != nil {
			return err
		}
		return syncAdmissionPath(dir)
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(dir, ".admit-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	// Link publishes without overwriting an immutable artifact created elsewhere.
	if err := os.Link(f.Name(), final); err != nil {
		return err
	}
	verified, err := admissionBlob(final)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, verified) {
		return admissionFault("conflict", final, "published artifact failed byte verification")
	}
	return syncAdmissionPath(dir)
}

func syncAdmissionPath(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
