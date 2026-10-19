// Import completion must account for exactly the source inventory on admission
// and replay; entries from another list cannot stand in for its missing entries.
package reduce

import (
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestGalleryAddBindsImportIdentity(t *testing.T) {
	for _, mode := range []string{"control", "plain", "forged-file", "forged-time"} {
		t.Run(mode, func(t *testing.T) {
			l, add, _ := galleryLedger(t)
			entry := *add
			entry.EntryID = newID("GAY2")
			entry.ImportID = string(model.GalleryImportIdentity(entry.File, entry.AddedAt))
			switch mode {
			case "plain":
				entry.ImportID = ""
			case "forged-file":
				entry.File.SHA256 = newDigest("different bytes")
			case "forged-time":
				entry.AddedAt = entry.AddedAt.Add(time.Second)
			}
			prefix := mustReplay(t, l.bundles())
			b := galleryFact(t, l, entry.Actor, &entry)
			_, applyErr := Apply(prefix, b)
			_, replayErr := Replay(l.bundles())
			for name, err := range map[string]error{"Apply": applyErr, "Replay": replayErr} {
				if mode == "control" || mode == "plain" {
					if err != nil {
						t.Fatalf("%s %s: %v", name, mode, err)
					}
					continue
				}
				if err == nil {
					t.Errorf("%s accepted standalone add with %s import identity", name, mode)
					continue
				}
				if f := wantFault(t, err, CodeInvalidField); f.Path != "import_id" {
					t.Errorf("%s refused another field: %v", name, err)
				}
			}
		})
	}
}

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
					entry.File.Path = "another-source.mp4"
				}
				if mode == "foreign-source" || mode == "wrong-inventory" {
					entry.ImportID = string(model.GalleryImportIdentity(entry.File, entry.AddedAt))
				}
				prefix := mustReplay(t, l.bundles())
				b := galleryFact(t, l, entry.Actor, &entry)
				if mode == "foreign-content" {
					_, applyErr := Apply(prefix, b)
					_, replayErr := Replay(l.bundles())
					for name, err := range map[string]error{"Apply": applyErr, "Replay": replayErr} {
						if err == nil {
							t.Errorf("%s accepted foreign-content add", name)
							continue
						}
						if f := wantFault(t, err, CodeInvalidField); f.Path != "import_id" {
							t.Errorf("%s refused another field: %v", name, err)
						}
					}
					return
				}
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
