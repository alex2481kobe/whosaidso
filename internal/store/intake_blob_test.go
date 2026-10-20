package store

// A blob is published under its digest. When that name is taken, only a
// regular file with the same length and digest can be reused. These tests
// exercise no-replace collisions even on systems where rename replaces files.
// Platform-specific rename implementation tests do not belong here.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestCaptureRefusesAnythingElseAtTheDigestName(t *testing.T) {
	plants := map[string]func(t *testing.T, path string){
		"directory":                 func(t *testing.T, path string) { must(t, os.Mkdir(path, 0o700)) },
		"other bytes":               func(t *testing.T, path string) { must(t, os.WriteFile(path, []byte("longer than source"), 0o600)) },
		"equal length, other bytes": func(t *testing.T, path string) { must(t, os.WriteFile(path, []byte("target"), 0o600)) },
	}
	for name, plant := range plants {
		t.Run(name, func(t *testing.T) {
			disk := systemIntakeIO()
			planted := ""
			// The blob's flush comes just before the rename to its digest name.
			disk.sync = func(f *os.File) error {
				if planted == "" && strings.HasPrefix(filepath.Base(f.Name()), "blob-") {
					planted = filepath.Join(filepath.Dir(f.Name()), string(model.HashBytes([]byte("source"))))
					plant(t, planted)
				}
				return f.Sync()
			}
			blob, err := captureBlobWithRename(context.Background(), t.TempDir(), strings.NewReader("source"), disk, blobTestRenameNoReplace)
			if planted == "" {
				t.Fatal("the blob was never flushed")
			}
			requireFault(t, err, "io")
			if blob != (CapturedBlob{}) {
				t.Fatalf("a refused capture acknowledged %+v", blob)
			}
		})
	}
}

func TestCaptureAcceptsIdenticalBlobsInOneRequest(t *testing.T) {
	p := intakeProject(t)
	disk := systemIntakeIO()
	collisions := 0
	rename := func(from, to string) error {
		err := blobTestRenameNoReplace(from, to)
		if errors.Is(err, os.ErrExist) {
			collisions++
		}
		return err
	}
	dir := t.TempDir()
	for range 2 {
		blob, err := captureBlobWithRename(context.Background(), dir, strings.NewReader("source"), disk, rename)
		must(t, err)
		if blob != blobOf("source") {
			t.Fatalf("unexpected blob: %+v", blob)
		}
	}
	req := requestFor(commandID(1), "source")
	req.Blobs = append(req.Blobs, strings.NewReader("source"))
	_, err := WriteIntake(context.Background(), p, req)
	must(t, err)
	if collisions != 1 {
		t.Fatalf("duplicate must reach the existing-file path once, got %d collisions", collisions)
	}
	packets, err := ReadVerifiedIntake(p, nil)
	if err != nil || len(packets) != 1 {
		t.Fatalf("an acknowledged capture must read back: %+v, %v", packets, err)
	}
	if len(packets[0].Blobs) != 1 || packets[0].Blobs[0] != blobOf("source") {
		t.Fatalf("duplicate must share one verified blob: %+v", packets[0].Blobs)
	}
}

// This serial fixture models destination-exists failures, not atomic rename
// races or Windows sharing modes; those require platform-specific tests.
func blobTestRenameNoReplace(from, to string) error {
	if _, err := os.Lstat(to); err == nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: os.ErrExist}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(from, to)
}
