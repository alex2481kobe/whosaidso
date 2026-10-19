// Import completion must account for exactly the source inventory on admission
// and replay; entries from another list cannot stand in for its missing entries.
package reduce

import (
	"github.com/alex2481kobe/whosaidso/internal/model"
	"testing"
)

func TestGalleryImportCompletionBindsSourceInventory(t *testing.T) {
	for _, mode := range []string{"control", "foreign-source", "empty", "omitted", "duplicate", "genuinely-empty", "wrong-inventory"} {
		t.Run(mode, func(t *testing.T) {
			l, add, _ := galleryLedger(t)
			inventory := []model.Digest{newDigest("legacy-one"), newDigest("legacy-two")}
			if mode == "genuinely-empty" {
				inventory = []model.Digest{}
			}
			source := model.GallerySource(inventory)
			ids := []model.ID{}
			for i, legacy := range inventory {
				entry := *add
				entry.EntryID = newID(string(legacy[:4]))
				entry.ImportID = string(source) + ":" + string(legacy)
				if mode == "foreign-source" && i == 0 {
					entry.ImportID = string(newDigest("other")) + ":" + string(legacy)
				}
				galleryFact(t, l, entry.Actor, &entry)
				ids = append(ids, entry.EntryID)
			}
			event := &model.GalleryImport{Source: source, Inventory: inventory, EmptySource: mode == "genuinely-empty", Entries: ids}
			switch mode {
			case "wrong-inventory":
				event.Inventory = []model.Digest{newDigest("other"), inventory[1]}
			case "empty":
				event.Entries = []model.ID{}
			case "omitted":
				event.Entries = ids[:1]
			case "duplicate":
				event.Entries = []model.ID{ids[0], ids[0]}
			}
			prefix := mustReplay(t, l.bundles())
			b := galleryFact(t, l, add.Actor, event)
			_, applyErr := Apply(prefix, b)
			_, replayErr := Replay(l.bundles())
			for name, err := range map[string]error{"Apply": applyErr, "Replay": replayErr} {
				if mode == "control" || mode == "genuinely-empty" {
					if err != nil {
						t.Fatalf("%s control: %v", name, err)
					}
				} else if err == nil {
					t.Errorf("%s accepted %s completion", name, mode)
				}
			}
		})
	}
}
