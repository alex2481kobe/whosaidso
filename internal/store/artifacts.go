package store

// Durable publication of verified bytes into the project's committed,
// content-addressed artifact store lives here. Deciding WHICH bytes may be
// published (evidence resolution, run-output provenance, admission policy) does
// not: callers hand over bytes they have already verified.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"

	"whosaidso/internal/model"
)

// artifactIO is the publication boundary the destination-verification test
// drives. Production links without overwriting.
type artifactIO struct {
	link func(oldname, newname string) error
}

func systemArtifactIO() artifactIO { return artifactIO{link: os.Link} }

// PublishArtifact makes data durable at <root>/<artifactDir>/<sha256>, the one
// committed copy of those bytes. Every store directory must be a real directory,
// never a symlink. An existing file under that name must already hold exactly
// these bytes. A new file is written and flushed under a temporary name, linked
// into place without overwriting, read back and compared before the directory
// is flushed. artifactDir is project-relative and slash-separated.
func PublishArtifact(root, artifactDir string, data []byte) error {
	return publishArtifact(root, artifactDir, data, systemArtifactIO())
}

func publishArtifact(root, artifactDir string, data []byte, disk artifactIO) error {
	if !filepath.IsAbs(root) {
		return storeFault("invalid-field", "project.root", "artifact publication needs an absolute project root")
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
			return storeFault("invalid-field", dir, "artifact directories cannot be symlinks")
		}
		if err := syncArtifactPath(parent); err != nil {
			return err
		}
	}
	final := filepath.Join(dir, string(model.HashBytes(data)))
	if prior, err := readArtifact(final, len(data)); err == nil {
		if !bytes.Equal(prior, data) {
			return storeFault("conflict", final, "existing artifact bytes do not match their name")
		}
		if err := syncArtifactPath(final); err != nil {
			return err
		}
		return syncArtifactPath(dir)
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
	if err := disk.link(f.Name(), final); err != nil {
		return err
	}
	verified, err := readArtifact(final, len(data))
	if err != nil {
		return err
	}
	if !bytes.Equal(data, verified) {
		return storeFault("conflict", final, "published artifact failed byte verification")
	}
	return syncArtifactPath(dir)
}

// readArtifact reads a regular, non-symlink file, bounded one byte past the
// length it should have: a longer file is already a mismatch, and nothing
// larger than the verified bytes is ever pulled into memory.
func readArtifact(path string, want int) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, storeFault("invalid-field", path, "artifact must be a regular file, never a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, int64(want)+1))
	closeErr := f.Close()
	if readErr != nil {
		return nil, readErr
	}
	return data, closeErr
}

func syncArtifactPath(path string) error {
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
