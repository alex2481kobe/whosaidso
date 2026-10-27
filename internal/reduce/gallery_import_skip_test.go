// A gallery.import seal may record legacy entries that were not imported (file
// missing) and legacy entries bound to an entry added by hand. Both must be
// decidable from the ledger alone: the source hash covers the skips, and a
// reuse is valid only for a hand-added entry whose pin and the stated legacy
// date reproduce the inventory member.
package reduce

import (
	"strings"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestGalleryImportSealAccountsForSkipsAndReuse(t *testing.T) {
	later := baseTime.Add(48 * time.Hour)
	missing := []model.GalleryImportSkip{{LegacyID: "gone", Path: "gone.png", Title: "Lost", Reason: "file missing"}}
	for _, mode := range []string{
		"control-skip-only", "control-skip-with-import", "control-reuse", "control-reuse-twice",
		"forged-skip", "empty-flag-with-skip", "stray-reuse", "wrong-reuse-date", "reuse-of-imported-entry", "reuse-twice-same-date",
	} {
		t.Run(mode, func(t *testing.T) {
			l, hand, _ := galleryLedger(t)
			event := &model.GalleryImport{Inventory: []model.Digest{}, Entries: []model.ID{}}
			legacy := func(at time.Time) model.Digest { return model.GalleryImportIdentity(hand.File, at) }
			switch mode {
			case "control-skip-only", "forged-skip", "empty-flag-with-skip":
				event.Skipped = missing
			case "control-skip-with-import":
				imported := *hand
				imported.EntryID = newID("GAY2")
				imported.ImportID = string(legacy(hand.AddedAt))
				galleryFact(t, l, imported.Actor, &imported)
				event.Inventory, event.Entries, event.Skipped = []model.Digest{legacy(hand.AddedAt)}, []model.ID{imported.EntryID}, missing
			case "control-reuse", "wrong-reuse-date", "stray-reuse":
				event.Inventory, event.Entries = []model.Digest{legacy(later)}, []model.ID{hand.EntryID}
				event.Reused = []model.GalleryImportReuse{{EntryID: hand.EntryID, AddedAt: later}}
				if mode == "wrong-reuse-date" {
					event.Reused[0].AddedAt = later.Add(time.Hour)
				}
				if mode == "stray-reuse" {
					event.Inventory, event.Entries = []model.Digest{}, []model.ID{}
				}
			case "control-reuse-twice", "reuse-twice-same-date":
				second := later.Add(time.Hour)
				if mode == "reuse-twice-same-date" {
					second = later
				}
				event.Inventory, event.Entries = []model.Digest{legacy(later), legacy(second)}, []model.ID{hand.EntryID, hand.EntryID}
				event.Reused = []model.GalleryImportReuse{{EntryID: hand.EntryID, AddedAt: later}, {EntryID: hand.EntryID, AddedAt: second}}
			case "reuse-of-imported-entry":
				imported := *hand
				imported.EntryID = newID("GAY2")
				imported.ImportID = string(legacy(hand.AddedAt))
				galleryFact(t, l, imported.Actor, &imported)
				event.Inventory, event.Entries = []model.Digest{legacy(later)}, []model.ID{imported.EntryID}
				event.Reused = []model.GalleryImportReuse{{EntryID: imported.EntryID, AddedAt: later}}
			}
			event.Source = model.GallerySource(event.Inventory, event.Skipped...)
			event.EmptySource = len(event.Inventory) == 0 && len(event.Skipped) == 0
			switch mode {
			case "forged-skip":
				event.Skipped = []model.GalleryImportSkip{{LegacyID: "other", Path: "other.png", Title: "Other", Reason: "file missing"}}
			case "empty-flag-with-skip":
				event.EmptySource = true
			}
			prefix := mustReplay(t, l.bundles())
			b := galleryFact(t, l, hand.Actor, event)
			_, applyErr := Apply(prefix, b)
			_, replayErr := Replay(l.bundles())
			for name, err := range map[string]error{"Apply": applyErr, "Replay": replayErr} {
				if strings.HasPrefix(mode, "control-") {
					if err != nil {
						t.Errorf("%s refused %s: %v", name, mode, err)
					}
				} else if err == nil {
					t.Errorf("%s accepted %s", name, mode)
				}
			}
		})
	}
}
