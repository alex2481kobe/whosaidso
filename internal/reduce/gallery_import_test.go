// Import completion must account for exactly the source inventory on admission
// and replay; entries from another list cannot stand in for its missing entries.
package reduce

import (
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestGalleryImportCompletionBindsSourceInventory(t *testing.T) {
	for _, mode := range []string{"control", "foreign-source", "foreign-content", "empty", "omitted", "duplicate", "genuinely-empty", "wrong-inventory", "empty-unmarked", "nonempty-marked", "duplicate-inventory"} {
		t.Run(mode, func(t *testing.T) {
			l, add, _ := galleryLedger(t)
			otherFile := add.File
			otherFile.Path = "other.mp4"
			inventory := []model.Digest{model.GalleryImportIdentity(add.File, add.AddedAt), model.GalleryImportIdentity(otherFile, add.AddedAt)}
			if mode == "genuinely-empty" || mode == "empty-unmarked" {
				inventory = []model.Digest{}
			}
			if mode == "duplicate-inventory" {
				inventory[1] = inventory[0]
			}
			source := model.GallerySource(inventory)
			if mode == "wrong-inventory" {
				inventory[0] = newDigest("other")
			}
			ids := []model.ID{}
			for i, legacy := range inventory {
				if mode == "duplicate-inventory" && i == 1 {
					ids = append(ids, ids[0])
					continue
				}
				entry := *add
				if i == 1 {
					entry.File = otherFile
				}
				entry.EntryID = newID(string(legacy[:4]))
				entry.ImportID = string(legacy)
				if mode == "foreign-content" && i == 0 {
					entry.File.SHA256 = newDigest("different bytes")
				}
				if mode == "foreign-source" && i == 0 {
					entry.ImportID = string(newDigest("other"))
				}
				galleryFact(t, l, entry.Actor, &entry)
				ids = append(ids, entry.EntryID)
			}
			event := &model.GalleryImport{Source: source, Inventory: inventory, EmptySource: mode == "genuinely-empty", Entries: ids}
			switch mode {
			case "nonempty-marked":
				event.EmptySource = true
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
