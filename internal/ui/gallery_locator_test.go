// Preview must recheck relative locators after admission, including symlink
// changes to identical bytes outside the checkout. External absolute pins remain valid.
package ui

import (
	"github.com/alex2481kobe/whosaidso/internal/gallery"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestGalleryPreviewRefusesRetargetedRelativeSymlink(t *testing.T) {
	world := &stubWorld{}
	host, session, root := galleryUI(t, world)
	inside := filepath.Join(root, "inside.mp4")
	outside := filepath.Join(t.TempDir(), "outside.mp4")
	for _, path := range []string{inside, outside} {
		if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "link.mp4")
	if err := os.Symlink("inside.mp4", link); err != nil {
		t.Fatal(err)
	}
	entry := addShown(t, root, link, "Render", "")
	url := fileURL(t, host, session, string(entry.EntryID))
	if code, _, body := ask(t, host, "GET", url, "", session); code != 200 || string(body) != "video" {
		t.Fatalf("control: %d %s", code, body)
	}
	if err := os.Rename(link, link+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if code, _, body := ask(t, host, "GET", url, "", session); code == 200 {
		t.Fatalf("served escaped symlink: %s", body)
	}
}

func TestGalleryPreviewRefusesParentSwapAfterResolution(t *testing.T) {
	_, _, root := galleryUI(t, &stubWorld{})
	parent := filepath.Join(root, "media")
	outside := t.TempDir()
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{parent, outside} {
		if err := os.WriteFile(filepath.Join(dir, "clip.mp4"), []byte("video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	add := addShown(t, root, filepath.Join(parent, "clip.mp4"), "Render", "")
	entry, err := gallery.Find(galleryProject(t, root), string(add.EntryID))
	if err != nil {
		t.Fatal(err)
	}
	h := &handler{}
	serve := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.serveEntry(w, httptest.NewRequest("GET", "/api/gallery/file", nil), root, entry)
		return w
	}
	if w := serve(); w.Code != 200 || w.Body.String() != "video" {
		t.Fatalf("control: %d %s", w.Code, w.Body)
	}
	// Find has resolved the locator. Replace a parent before preview opens it.
	if err := os.Rename(parent, parent+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, parent); err != nil {
		t.Fatal(err)
	}
	if w := serve(); w.Code != 404 {
		t.Fatalf("escaped bytes served after resolution: %d %s", w.Code, w.Body)
	}
}
