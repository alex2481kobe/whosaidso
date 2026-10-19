// Import regressions use synthetic lists and isolated homes; no real intake is read.
package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/store"
)

func TestGalleryImportIdentityFollowsListContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv(store.HomeEnv, home)
	root, _ := cliFixture(t)
	media := filepath.Join(root, "clip.mp4")
	if err := os.WriteFile(media, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	legacy, err := store.GalleryFile("test/cli")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	first := []byte(`[{"id":"first","path":` + strconvQuote(media) + `,"title":"First","added_at":"2026-01-01T00:00:00Z"}]`)
	second := []byte(`[{"id":"second","path":` + strconvQuote(media) + `,"title":"Second","added_at":"2026-01-02T00:00:00Z"}]`)
	if err := os.WriteFile(legacy, first, 0600); err != nil {
		t.Fatal(err)
	}
	galleryCommand(t, root, "gallery", "import", "--admit", "--reason", "reviewed")
	// A different machine can have the same pathname and a different list.
	if err := os.WriteFile(legacy, second, 0600); err != nil {
		t.Fatal(err)
	}
	galleryCommand(t, root, "gallery", "import", "--admit", "--reason", "reviewed")
	p, _ := store.Discover(root)
	state, err := store.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(state.Snapshot().Gallery()); got != 2 {
		t.Errorf("different list at same path suppressed: got %d entries", got)
	}
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(home, moved); err != nil {
		t.Fatal(err)
	}
	t.Setenv(store.HomeEnv, moved)
	galleryCommand(t, root, "gallery", "import", "--admit", "--reason", "reviewed")
	state, err = store.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(state.Snapshot().Gallery()); got != 2 {
		t.Errorf("moved list duplicated: got %d entries", got)
	}
}
