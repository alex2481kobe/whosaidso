// Import regressions use synthetic lists and isolated homes; no real intake is read.
package main

import (
	"os"
	"path/filepath"
	"strings"
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

func TestGalleryImportDeduplicatesEntriesAcrossInventories(t *testing.T) {
	root, _ := cliFixture(t)
	media := filepath.Join(root, "clip.mp4")
	if err := os.WriteFile(media, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	a := `{"id":"a","path":` + strconvQuote(media) + `,"title":"A","added_at":"2026-01-01T00:00:00Z"}`
	b := `{"id":"b","path":` + strconvQuote(media) + `,"title":"B","added_at":"2026-01-02T00:00:00Z"}`
	home := t.TempDir()
	for _, step := range []struct {
		name, list string
		count      int
		newHome    bool
	}{
		{"first", "[" + a + "]", 1, false},
		{"extended", "[" + a + "," + b + "]", 2, false},
		{"reordered", "[" + b + "," + a + "]", 2, false},
		{"other-home", "[" + b + "," + a + "]", 2, true},
	} {
		t.Run(step.name, func(t *testing.T) {
			if step.newHome {
				home = t.TempDir()
			}
			if err := os.Chmod(home, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv(store.HomeEnv, home)
			legacy, err := store.GalleryFile("test/cli")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(legacy, []byte(step.list), 0600); err != nil {
				t.Fatal(err)
			}
			galleryCommand(t, root, "gallery", "import", "--admit", "--reason", "reviewed")
			p, err := store.Discover(root)
			if err != nil {
				t.Fatal(err)
			}
			state, err := store.Load(p)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(state.Snapshot().Gallery()); got != step.count {
				t.Errorf("got %d entries, want %d", got, step.count)
			}
		})
	}
}

func TestGalleryImportCollapsesMatchingLegacyRecords(t *testing.T) {
	root, _ := cliFixture(t)
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(store.HomeEnv, home)
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
	first := `{"id":"first","path":` + strconvQuote(media) + `,"title":"First","note":"keep","added_at":"2026-01-01T00:00:00Z"}`
	second := `{"id":"second","path":` + strconvQuote(media) + `,"title":"Second","note":"different","added_at":"2025-12-31T18:00:00-06:00"}`
	third := `{"id":"third","path":` + strconvQuote(media) + `,"title":"Third","added_at":"2026-01-01T00:00:00Z"}`
	raw := "[" + first + "," + second + "," + third + "]"
	if err := os.WriteFile(legacy, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"first import", "repeated import"} {
		_, diagnostic := galleryCommand(t, root, "gallery", "import", "--admit", "--reason", "reviewed")
		for _, line := range []string{`collapsed legacy record 2 "second" into record 1 "first"`, `collapsed legacy record 3 "third" into record 1 "first"`} {
			if !strings.Contains(diagnostic, line) {
				t.Errorf("%s missing collapse report %q: %s", step, line, diagnostic)
			}
		}
		p, err := store.Discover(root)
		if err != nil {
			t.Fatal(err)
		}
		state, err := store.Load(p)
		if err != nil {
			t.Fatal(err)
		}
		entries := state.Snapshot().Gallery()
		if len(entries) != 1 {
			t.Fatalf("%s: got %d entries, want one", step, len(entries))
		}
		if entries[0].Add.Title != "First" || entries[0].Add.Note != "keep" {
			t.Fatalf("first record was not retained: %+v", entries[0].Add)
		}
	}
	after, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != raw {
		t.Fatal("import changed the legacy inventory")
	}
}
