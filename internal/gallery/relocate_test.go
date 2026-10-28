// A gallery pin follows the same relative file in another project home.
// Hash checks still distinguish changed local bytes from the recorded media.
package gallery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestGalleryLocatorsFollowProjectHomes(t *testing.T) {
	old := filepath.Join(t.TempDir(), "project")
	next := filepath.Join(t.TempDir(), "project")
	for _, root := range []string{old, next} {
		if err := os.MkdirAll(filepath.Join(root, "media"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	original := filepath.Join(old, "media", "image.png")
	local := filepath.Join(next, "media", "image.png")
	for _, name := range []string{original, local} {
		if err := os.WriteFile(name, []byte("synthetic media"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	pin, err := Pin(old, original)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := Prepare(old, original, "Image", "", model.Actor{ID: "author"}, nil, time.Now())
	if err != nil || fresh.File.Path != "media/image.png" {
		t.Fatalf("new locator is not portable: %+v %v", fresh, err)
	}
	if err := os.RemoveAll(old); err != nil {
		t.Fatal(err)
	}
	// ResolvePath returns the symlink-resolved path (macOS temp dirs sit behind /var -> /private/var).
	resolvedLocal, err := filepath.EvalSymlinks(local)
	if err != nil {
		t.Fatal(err)
	}
	for _, locator := range []string{original, fresh.File.Path, `Z:\old\project\media\image.png`} {
		got, err := Pin(next, locator)
		if err != nil || !got.SameContent(pin) {
			t.Fatalf("moved pin %s: %+v %v", locator, got, err)
		}
		path, err := ResolvePath(next, locator)
		if err != nil || path != resolvedLocal {
			t.Fatalf("local display location: %s %v", path, err)
		}
	}
	if err := os.WriteFile(local, []byte("different media"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Pin(next, original)
	if err != nil || got.SameContent(pin) {
		t.Fatalf("relocation hid changed bytes: %+v %v", got, err)
	}
	if _, err := Pin(next, filepath.Join(t.TempDir(), "unrelated", "media", "image.png")); err == nil {
		t.Fatal("unanchored path guessed a local file")
	}
	nested := filepath.Join(next, "project", "media")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "image.png"), []byte("different media"), 0600); err != nil {
		t.Fatal(err)
	}
	ambiguous := filepath.Join(t.TempDir(), "project", "project", "media", "image.png")
	if _, err := Pin(next, ambiguous); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous home mapping was guessed: %v", err)
	}

}
