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

// A deliberate removal wins. When the only hand-added entry holding the pin was
// retracted, the legacy entry binds to it (the seal records the reuse) and the
// image stays out of the gallery.
func TestGalleryImportBindsToARetractedHandEntryAndKeepsItRemoved(t *testing.T) {
	root := importFixture(t, `[{"id":"old","path":"present.png","title":"Old image","added_at":"2026-01-01T00:00:00Z"}]`)
	_, diagnostic := galleryCommand(t, root, "gallery", "add", "present.png", "--title", "Hand added", "--admit", "--reason", "reviewed")
	galleryCommand(t, root, "gallery", "rm", galleryEntryID(t, diagnostic), "--admit", "--reason", "removed on purpose")
	if out, _ := galleryCommand(t, root, "gallery", "list"); !strings.Contains(out, "empty") {
		t.Fatalf("control: the retracted entry is still listed: %s", out)
	}
	importOnce(t, root)
	if out, _ := galleryCommand(t, root, "gallery", "list"); !strings.Contains(out, "empty") {
		t.Fatalf("a removed image came back through import: %s", out)
	}
	if seals := importSeals(t, root); len(seals) != 1 || len(seals[0].Reused) != 1 {
		t.Fatalf("the seal does not record the reuse of the retracted entry: %+v", seals)
	}
	importRetryCapturesNothing(t, root)
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

// A hand-added entry that merely shares the vanished file's locator does not
// hold the legacy entry: nothing sealed it, so the missing file is reported.
func TestGalleryImportReportsAMissingFileThatNoSealedEntryHolds(t *testing.T) {
	root := importFixture(t, `[{"id":"b","path":"second.png","title":"Second","added_at":"2026-01-02T00:00:00Z"}]`)
	writeSecond(t, root)
	galleryCommand(t, root, "gallery", "add", "second.png", "--title", "Hand added", "--admit", "--reason", "reviewed")
	if err := os.Remove(filepath.Join(root, "second.png")); err != nil {
		t.Fatal(err)
	}
	importOnce(t, root)
	if seals := importSeals(t, root); len(seals) != 1 || len(seals[0].Skipped) != 1 {
		t.Fatalf("a missing file no sealed entry holds must be reported: %+v", seals)
	}
}

// An entry bound to a hand-added one stays held when its file later vanishes:
// the retry captures nothing and reports no skip.
func TestGalleryImportRetryCapturesNothingWhenAReusedFileLaterVanishes(t *testing.T) {
	root := importFixture(t, `[{"id":"old","path":"second.png","title":"Old","added_at":"2026-01-02T00:00:00Z"}]`)
	writeSecond(t, root)
	galleryCommand(t, root, "gallery", "add", "second.png", "--title", "Hand added", "--admit", "--reason", "reviewed")
	importOnce(t, root)
	if err := os.Remove(filepath.Join(root, "second.png")); err != nil {
		t.Fatal(err)
	}
	importRetryCapturesNothing(t, root)
}

// Holding is per legacy entry (locator and legacy date), not per locator: an
// entry listed later under the same locator and another date was never sealed,
// so once its file is gone it is reported, while the sealed one stays held.
func TestGalleryImportHoldsOnlyTheSealedDateOfASharedLocator(t *testing.T) {
	root := importFixture(t, `[{"id":"one","path":"second.png","title":"One","added_at":"2026-01-02T00:00:00Z"}]`)
	writeSecond(t, root)
	importOnce(t, root)
	if err := os.Remove(filepath.Join(root, "second.png")); err != nil {
		t.Fatal(err)
	}
	legacy, err := store.GalleryFile("test/cli")
	if err != nil {
		t.Fatal(err)
	}
	list := `[{"id":"one","path":"second.png","title":"One","added_at":"2026-01-02T00:00:00Z"},{"id":"two","path":"second.png","title":"Two","added_at":"2026-02-03T00:00:00Z"}]`
	if err := os.WriteFile(legacy, []byte(list), 0600); err != nil {
		t.Fatal(err)
	}
	importOnce(t, root)
	seals := importSeals(t, root)
	if len(seals) != 2 || len(seals[1].Skipped) != 1 || seals[1].Skipped[0].LegacyID != "two" {
		t.Fatalf("only the never-sealed entry may be reported missing: %+v", seals)
	}
	if got := len(galleryTitles(t, root)); got != 1 {
		t.Fatalf("a vanished file added entries: %d", got)
	}
}

// A skip is part of the sealed source, so a sealed entry vanishing later does
// not disturb it: the retry still captures nothing.
func TestGalleryImportRetryCapturesNothingWhenASkipAndASealedFileCoexist(t *testing.T) {
	root := importFixture(t, `[{"id":"a","path":"second.png","title":"A","added_at":"2026-01-01T00:00:00Z"},{"id":"c","path":"never.png","title":"C","added_at":"2026-01-02T00:00:00Z"}]`)
	writeSecond(t, root)
	importOnce(t, root)
	if seals := importSeals(t, root); len(seals) != 1 || len(seals[0].Skipped) != 1 {
		t.Fatalf("control: one skip sealed: %+v", seals)
	}
	if err := os.Remove(filepath.Join(root, "second.png")); err != nil {
		t.Fatal(err)
	}
	importRetryCapturesNothing(t, root)
}
