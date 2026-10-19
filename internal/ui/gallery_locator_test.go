// Preview must recheck relative locators after admission, including symlink
// changes to identical bytes outside the checkout. External absolute pins remain valid.
package ui

import (
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
	if err := os.Symlink(inside, link); err != nil {
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
