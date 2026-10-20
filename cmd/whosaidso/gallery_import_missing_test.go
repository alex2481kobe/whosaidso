// Import outcomes when a legacy entry's file is missing or already in the
// gallery. Lists are synthetic and homes isolated; the checks read the admitted
// seal, never local intake.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

// importFixture writes the legacy list for a fresh project and one present
// file, present.png, in its checkout.
func importFixture(t *testing.T, list string) string {
	t.Helper()
	t.Setenv(store.HomeEnv, t.TempDir())
	root, _ := cliFixture(t)
	legacy, err := store.GalleryFile("test/cli")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(list), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "present.png"), []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

type importSeal struct {
	Skipped []struct {
		LegacyID            string `json:"legacy_id"`
		Path, Title, Reason string
	} `json:"skipped"`
	Reused []json.RawMessage `json:"reused"`
}

func importSeals(t *testing.T, root string) []importSeal {
	t.Helper()
	p, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	bundles, err := store.ReadPrefix(p)
	if err != nil {
		t.Fatal(err)
	}
	seals := []importSeal{}
	for _, b := range bundles {
		for _, event := range b.Events {
			if event.Type != "gallery.import" {
				continue
			}
			var seal importSeal
			if err := json.Unmarshal(event.Data, &seal); err != nil {
				t.Fatal(err)
			}
			seals = append(seals, seal)
		}
	}
	return seals
}

func galleryTitles(t *testing.T, root string) []string {
	t.Helper()
	p, _ := store.Discover(root)
	state, err := store.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	titles := []string{}
	for _, e := range state.Snapshot().Gallery() {
		titles = append(titles, e.Add.Title)
	}
	return titles
}

// importOnce runs an admitted import and fails if it produces no output.
func importOnce(t *testing.T, root string) string {
	t.Helper()
	out, diagnostic := galleryCommand(t, root, "gallery", "import", "--admit", "--reason", "reviewed")
	if out == "" {
		t.Fatal("import captured nothing")
	}
	return diagnostic
}

// importRetryCapturesNothing runs the import again and requires no new packet
// and no second seal.
func importRetryCapturesNothing(t *testing.T, root string) {
	t.Helper()
	out, _ := galleryCommand(t, root, "gallery", "import", "--admit", "--reason", "retry")
	if out != "" || len(importSeals(t, root)) != 1 {
		t.Fatalf("retry captured more: %q, %d seals", out, len(importSeals(t, root)))
	}
}

func TestGalleryImportRecordsMissingFilesAndImportsTheRest(t *testing.T) {
	root := importFixture(t, `[{"id":"gone","path":"gone.png","title":"Lost image","added_at":"2026-01-01T00:00:00Z"},{"id":"kept","path":"present.png","title":"Kept image","added_at":"2026-01-02T00:00:00Z"}]`)
	diagnostic := importOnce(t, root)
	for _, word := range []string{"gone", "gone.png", "Lost image", "file missing"} {
		if !strings.Contains(diagnostic, word) {
			t.Errorf("output does not name %q: %s", word, diagnostic)
		}
	}
	if titles := galleryTitles(t, root); len(titles) != 1 || titles[0] != "Kept image" {
		t.Fatalf("the entry whose file exists was not imported: %v", titles)
	}
	seals := importSeals(t, root)
	if len(seals) != 1 || len(seals[0].Skipped) != 1 {
		t.Fatalf("seal does not record the missing entry: %+v", seals)
	}
	if s := seals[0].Skipped[0]; s.LegacyID != "gone" || s.Path != "gone.png" || s.Title != "Lost image" || s.Reason != "file missing" {
		t.Fatalf("seal lost part of the missing entry: %+v", s)
	}
	importRetryCapturesNothing(t, root)
}

func TestGalleryImportAllFilesMissingSealsTheSkipsAndRetryCapturesNothing(t *testing.T) {
	root := importFixture(t, `[{"id":"gone","path":"gone.png","title":"Lost image","added_at":"2026-01-01T00:00:00Z"}]`)
	importOnce(t, root)
	seals := importSeals(t, root)
	if len(seals) != 1 || len(seals[0].Skipped) != 1 || len(galleryTitles(t, root)) != 0 {
		t.Fatalf("all-missing import: %+v, entries %v", seals, galleryTitles(t, root))
	}
	importRetryCapturesNothing(t, root)
}

func TestGalleryImportOtherErrorsStillFailTheImport(t *testing.T) {
	root := importFixture(t, `[{"id":"kept","path":"present.png","title":"Kept image","added_at":"2026-01-01T00:00:00Z"},{"id":"dir","path":"adir","title":"A directory","added_at":"2026-01-02T00:00:00Z"}]`)
	if err := os.Mkdir(filepath.Join(root, "adir"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, errout, code := cliRun(t, root, nil, "contributor", "gallery", "import", "--admit", "--reason", "reviewed"); code == 0 || !strings.Contains(errout, "not a regular file") {
		t.Fatalf("a non-missing error did not fail the import: %d %s", code, errout)
	}
	if len(importSeals(t, root)) != 0 || len(galleryTitles(t, root)) != 0 {
		t.Fatal("a failed import left entries or a seal")
	}
}

func TestGalleryImportReusesHandAddedFilePin(t *testing.T) {
	root := importFixture(t, `[{"id":"old","path":"present.png","title":"Old image","added_at":"2026-01-01T00:00:00Z"},{"id":"older","path":"present.png","title":"Older image","added_at":"2025-12-01T00:00:00Z"}]`)
	_, diagnostic := galleryCommand(t, root, "gallery", "add", "present.png", "--title", "Hand added", "--admit", "--reason", "reviewed")
	id := galleryEntryID(t, diagnostic)
	galleryCommand(t, root, "gallery", "import", "--admit", "--reason", "reviewed")
	p, _ := store.Discover(root)
	state, err := store.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if entries := state.Snapshot().Gallery(); len(entries) != 1 || entries[0].Add.EntryID != model.ID(id) {
		t.Fatalf("hand-added entry duplicated: %+v", entries)
	}
	if seals := importSeals(t, root); len(seals) != 1 || len(seals[0].Reused) != 2 {
		t.Fatalf("seal does not bind both legacy dates to the hand-added entry: %+v", seals)
	}
	importRetryCapturesNothing(t, root)
}

func TestGalleryImportKeepsSameBytesAtAnotherLocator(t *testing.T) {
	root := importFixture(t, `[{"id":"copy","path":"copy.png","title":"Copy","added_at":"2026-01-01T00:00:00Z"}]`)
	if err := os.WriteFile(filepath.Join(root, "copy.png"), []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	galleryCommand(t, root, "gallery", "add", "present.png", "--title", "Hand added", "--admit", "--reason", "reviewed")
	importOnce(t, root)
	if titles := galleryTitles(t, root); len(titles) != 2 {
		t.Fatalf("same bytes at another locator were treated as the hand-added pin: %v", titles)
	}
}
