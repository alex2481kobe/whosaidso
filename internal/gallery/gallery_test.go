package gallery

// Pinning tests distinguish bytes from paths and refuse non-files or long words.
// Admission, retraction and import behavior are exercised by the CLI tests.

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/evidence"
	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestPreparePinsActualBytesAndChecksWords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := Prepare(dir, path, " video ", "note", model.Actor{ID: "contributor"}, nil, time.Now())
	if err != nil || e.File.SHA256 != model.HashBytes([]byte("video")) || e.File.Length != 5 || e.File.Path != "clip.mp4" || e.Title != "video" {
		t.Fatalf("control: %+v %v", e, err)
	}
	for _, c := range []struct{ path, title, note string }{
		{"missing", "video", ""}, {dir, "video", ""}, {path, " ", ""}, {path, "a\nb", ""}, {path, strings.Repeat("t", 121), ""}, {path, "t", strings.Repeat("n", 1001)},
	} {
		if _, err := Prepare(dir, c.path, c.title, c.note, model.Actor{ID: "contributor"}, nil, time.Now()); err == nil {
			t.Errorf("accepted invalid input: %+v", c)
		}
	}
	if err := os.WriteFile(path, []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	pin, err := Pin("", path)
	if err != nil || pin.SHA256 == e.File.SHA256 {
		t.Fatalf("same-length replacement not detected: %+v %v", pin, err)
	}
}

func TestPinStreamsFilesBeyondBlobResolverLimit(t *testing.T) {
	// External media has no blob resolver size limit. A sparse file provides
	// real readable bytes without committing a large binary fixture.
	path := filepath.Join(t.TempDir(), "large.mp4")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	length := int64(evidence.DefaultMaxBytes) + 1
	if err := f.Truncate(length); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	pin, err := Pin("", path)
	if err != nil || pin.Length != uint64(length) || !model.ValidDigest(pin.SHA256) {
		t.Fatalf("large file was bounded or refused: %+v %v", pin, err)
	}
}

func TestRelativeMediaStaysInsideCheckout(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "checkout")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "external.mp4")
	inside := filepath.Join(root, "inside.mp4")
	for _, path := range []string{outside, inside} {
		if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("inside.mp4", filepath.Join(root, "safe.mp4")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"inside.mp4", "safe.mp4", outside, filepath.Join(root, "escape.mp4")} {
		if _, err := Prepare(root, path, "control", "", model.Actor{ID: "contributor"}, nil, time.Now()); err != nil {
			t.Fatalf("control %s: %v", path, err)
		}
	}
	for _, path := range []string{"../external.mp4", "../checkout/inside.mp4", "escape.mp4"} {
		if _, err := Prepare(root, path, "escape", "", model.Actor{ID: "contributor"}, nil, time.Now()); err == nil {
			t.Errorf("accepted relative escape %s", path)
		}
	}
}

func TestPinRefusesParentSwapAfterResolution(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	parent := filepath.Join(root, "media")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{parent, outside} {
		if err := os.WriteFile(filepath.Join(dir, "clip.mp4"), []byte("video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	locator := "media/clip.mp4"
	if _, err := ResolvePath(root, locator); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(root, locator)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if err := os.Rename(parent, parent+".old"); err != nil {
		// On Windows the error os.ErrPermission matches is ERROR_ACCESS_DENIED.
		if runtime.GOOS == "windows" && errors.Is(err, os.ErrPermission) {
			t.Skipf("Windows refuses to rename a directory holding an open file, so the swap this test attacks cannot happen: %v", err)
		}
		t.Fatal(err)
	}
	if err := os.Symlink(outside, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := Pin(root, locator); err == nil {
		t.Fatal("pinned escaped bytes after parent swap")
	}
	if pin, err := Hash(opened, locator); err != nil || pin.SHA256 != model.HashBytes([]byte("video")) {
		t.Fatalf("opened handle changed: %+v %v", pin, err)
	}
	if _, err := Pin("", filepath.Join(outside, "clip.mp4")); err != nil {
		t.Fatalf("absolute external control: %v", err)
	}
}
