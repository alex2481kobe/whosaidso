// Import identity across time: what a second run does once files appear or
// vanish, and which already-held entry a legacy entry may be bound to. The
// fixtures reuse the synthetic legacy list and isolated homes of the missing
// file tests; nothing here reads a real machine's list.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/store"
)

const twoPresent = `[{"id":"a","path":"present.png","title":"Present","added_at":"2026-01-01T00:00:00Z"},{"id":"b","path":"second.png","title":"Second","added_at":"2026-01-02T00:00:00Z"}]`

func writeSecond(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "second.png"), []byte("other image"), 0600); err != nil {
		t.Fatal(err)
	}
}

// A file that reappears is imported once and the entries already held are not
// duplicated. This is the control for the vanish test below.
func TestGalleryImportPicksUpAFileThatReappears(t *testing.T) {
	root := importFixture(t, twoPresent)
	importOnce(t, root)
	if got := len(galleryTitles(t, root)); got != 1 {
		t.Fatalf("control: only the present file imports first, got %d entries", got)
	}
	writeSecond(t, root)
	importOnce(t, root)
	if titles := galleryTitles(t, root); len(titles) != 2 {
		t.Fatalf("a file that reappeared was duplicated or lost: %v", titles)
	}
}

// Re-running a completed import captures nothing. Every machine that sealed
// an import before skips existed has a list whose files can later move; the
// rerun must not seal the list again just because a file is now gone, and must
// not record as "file missing" an entry the ledger already holds.
func TestGalleryImportRetryCapturesNothingWhenASealedFileLaterVanishes(t *testing.T) {
	root := importFixture(t, twoPresent)
	writeSecond(t, root)
	importOnce(t, root)
	importRetryCapturesNothing(t, root) // control: nothing changed, nothing captured
	if err := os.Remove(filepath.Join(root, "second.png")); err != nil {
		t.Fatal(err)
	}
	out, _ := galleryCommand(t, root, "gallery", "import", "--admit", "--reason", "retry")
	seals := importSeals(t, root)
	if out != "" || len(seals) != 1 {
		t.Fatalf("a completed import was sealed again after a file vanished: output %q, %d seals; "+
			"the second seal claims %+v as skipped although that entry is already in the gallery (%v)",
			out, len(seals), seals[len(seals)-1].Skipped, galleryTitles(t, root))
	}
}

// A retracted entry is not in the gallery. Binding a legacy entry to it
// satisfies the seal while leaving the legacy entry invisible, so an entry
// whose file exists would not be imported.
func TestGalleryImportDoesNotBindToARetractedHandEntry(t *testing.T) {
	root := importFixture(t, `[{"id":"old","path":"present.png","title":"Old image","added_at":"2026-01-01T00:00:00Z"}]`)
	_, diagnostic := galleryCommand(t, root, "gallery", "add", "present.png", "--title", "Hand added", "--admit", "--reason", "reviewed")
	galleryCommand(t, root, "gallery", "rm", galleryEntryID(t, diagnostic), "--admit", "--reason", "removed on purpose")
	if out, _ := galleryCommand(t, root, "gallery", "list"); !strings.Contains(out, "empty") {
		t.Fatalf("control: the retracted entry is still listed: %s", out)
	}
	importOnce(t, root)
	if out, _ := galleryCommand(t, root, "gallery", "list"); strings.Contains(out, "empty") {
		t.Fatalf("every legacy entry whose file exists must be in the gallery after import, but the gallery is empty: "+
			"the legacy entry was bound to the retracted entry (seals %+v)", importSeals(t, root))
	}
}

// When the same file was added, removed and added again by hand, the legacy
// entry is bound to the entry that is still in the gallery, not to the first
// one ever added.
func TestGalleryImportBindsToTheActiveHandEntryNotARetractedOne(t *testing.T) {
	root := importFixture(t, `[{"id":"old","path":"present.png","title":"Old image","added_at":"2026-01-01T00:00:00Z"}]`)
	_, first := galleryCommand(t, root, "gallery", "add", "present.png", "--title", "First", "--admit", "--reason", "reviewed")
	galleryCommand(t, root, "gallery", "rm", galleryEntryID(t, first), "--admit", "--reason", "removed")
	_, second := galleryCommand(t, root, "gallery", "add", "present.png", "--title", "Second", "--admit", "--reason", "reviewed")
	active := galleryEntryID(t, second)
	importOnce(t, root)
	p, _ := store.Discover(root)
	bundles, err := store.ReadPrefix(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bundles {
		for _, event := range b.Events {
			if event.Type == "gallery.import" && strings.Contains(string(event.Data), galleryEntryID(t, first)) {
				t.Fatalf("the seal binds the legacy entry to the retracted entry %s although %s is active: %s", galleryEntryID(t, first), active, event.Data)
			}
		}
	}
}

// A skipped entry is sealed whatever its words are, so a legacy list that
// holds a missing file with an odd path must still complete. A path made only
// of spaces is a legal file name that a seal cannot carry.
func TestGalleryImportCompletesWhenAMissingEntryHasAnUnusualPath(t *testing.T) {
	root := importFixture(t, `[{"id":"ok","path":"present.png","title":"Present","added_at":"2026-01-01T00:00:00Z"},{"id":"odd","path":" ","title":"Odd","added_at":"2026-01-02T00:00:00Z"}]`)
	if _, errout, code := cliRun(t, root, nil, "contributor", "gallery", "import", "--admit", "--reason", "reviewed"); code != 0 {
		t.Fatalf("import failed after admitting %v and before sealing: %s", galleryTitles(t, root), errout)
	}
}
