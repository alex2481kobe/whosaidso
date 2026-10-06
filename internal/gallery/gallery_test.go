package gallery

// The gallery's rules: add refuses what is not an existing file and words
// that are not short; rm drops the entry and never the file; a gallery never
// written is empty; and a file gone from disk lists as missing.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAddRefusesWhatIsNotAnExistingFileWithShortWords(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(t.TempDir(), "g.json")
	if err := os.WriteFile(filepath.Join(dir, "shot.png"), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	e, err := Add(context.Background(), file, dir, "shot.png", "  a shot  ", "look at the edge", "agent", now)
	if err != nil || e.Path != filepath.Join(dir, "shot.png") || e.Title != "a shot" || e.AddedBy != "agent" || len(e.ID) != 8 {
		t.Fatalf("control: %+v %v", e, err)
	}
	for name, c := range map[string]struct{ path, title, note string }{
		"a missing path":        {"gone.png", "t", ""},
		"a directory":           {dir, "t", ""},
		"a blank title":         {"shot.png", "  ", ""},
		"a two-line title":      {"shot.png", "one\ntwo", ""},
		"a title past its cap":  {"shot.png", strings.Repeat("t", maxTitle+1), ""},
		"a note past its cap":   {"shot.png", "t", strings.Repeat("n", maxNote+1)},
		"an empty path":         {"", "t", ""},
		"a path through a file": {"shot.png/x", "t", ""},
	} {
		if _, err := Add(context.Background(), file, dir, c.path, c.title, c.note, "", now); err == nil {
			t.Errorf("%s was added", name)
		}
	}
	if entries, _ := Load(file); len(entries) != 1 {
		t.Fatalf("a refused add wrote an entry: %v", entries)
	}
}

func TestRemoveDropsTheEntryAndNeverTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(path, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "g.json")
	a, _ := Add(context.Background(), file, "/", path, "a", "", "", time.Now())
	b, _ := Add(context.Background(), file, "/", path, "b", "", "", time.Now())
	if _, err := Remove(context.Background(), file, a.ID); err != nil {
		t.Fatal(err)
	}
	entries, err := Load(file)
	if err != nil || len(entries) != 1 || entries[0].ID != b.ID {
		t.Fatalf("after rm: %v %v", entries, err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "video" {
		t.Fatalf("rm touched the file: %q %v", got, err)
	}
	if _, err := Remove(context.Background(), file, a.ID); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("removing an id twice: %v", err)
	}
}

func TestListLooksAtEachFileWhenAsked(t *testing.T) {
	file := filepath.Join(t.TempDir(), "never", "g.json")
	if shown, err := List(file); err != nil || len(shown) != 0 {
		t.Fatalf("a gallery never written: %v %v", shown, err)
	}
	path := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(context.Background(), file, "/", path, "notes", "", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if shown, _ := List(file); len(shown) != 1 || shown[0].Status != "present" || shown[0].Kind != Text {
		t.Fatalf("present: %+v", shown)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if shown, _ := List(file); shown[0].Status != "missing" {
		t.Fatalf("a removed file: %+v", shown)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if shown, _ := List(file); shown[0].Status != "missing" {
		t.Fatalf("a directory where the file was: %+v", shown)
	}
}

// Overlapping adds and removes are all kept: each holds the gallery's lock
// from its read to its rename, so none starts from a list another is about
// to replace.
func TestOverlappingChangesAreAllKept(t *testing.T) {
	file := filepath.Join(t.TempDir(), "g.json")
	path := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(path, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	const n = 24
	var old []string
	for i := 0; i < n; i++ {
		e, err := Add(context.Background(), file, "/", path, "old", "", "", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		old = append(old, e.ID)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := Add(context.Background(), file, "/", path, "new", "", "", time.Now())
			errs <- err
		}()
		go func(id string) { defer wg.Done(); _, err := Remove(context.Background(), file, id); errs <- err }(old[i])
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	entries, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Title != "new" {
			t.Errorf("removed entry %s came back", e.ID)
		}
	}
	if len(entries) != n {
		t.Fatalf("%d overlapping adds left %d entries", n, len(entries))
	}
}
