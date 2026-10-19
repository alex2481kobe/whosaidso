// Import is the sole reader of the retired machine-local gallery list.
// It captures proposals; it never rewrites that file or admits a packet.
package gallery

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func Import(p store.Project, by model.Actor, now time.Time) ([]model.TypedEvent, error) {
	file, err := store.GalleryFile(p.ID)
	if err != nil {
		return nil, err
	}
	state, err := store.Load(p)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return []model.TypedEvent{}, nil
	}
	if err != nil {
		return nil, err
	}
	var old []Entry
	if err := json.Unmarshal(raw, &old); err != nil {
		return nil, err
	}
	inventory := make([]model.Digest, 0, len(old))
	prepared := make([]model.GalleryAdd, 0, len(old))
	for _, e := range old {
		add, err := Prepare(p.Root, e.Path, e.Title, e.Note, by, nil, now)
		if err != nil {
			return nil, err
		}
		add.AddedAt = e.AddedAt.UTC()
		identity := model.GalleryImportIdentity(add.File, add.AddedAt)
		add.ImportID = string(identity)
		prepared = append(prepared, add)
		inventory = append(inventory, identity)
	}
	source := model.GallerySource(inventory)
	if state.Snapshot().GalleryImported(source) {
		return []model.TypedEvent{}, nil
	}
	entries, err := Entries(p)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Add.ImportID] = true
	}
	out := []model.TypedEvent{}
	imported := []model.ID{}
	for _, add := range prepared {
		importID := add.ImportID
		if seen[importID] {
			for _, existing := range entries {
				if existing.Add.ImportID == importID {
					imported = append(imported, existing.Add.EntryID)
					break
				}
			}
			continue
		}
		out = append(out, &add)
		imported = append(imported, add.EntryID)
		seen[importID] = true
	}
	return append(out, &model.GalleryImport{Source: source, Inventory: inventory, EmptySource: len(inventory) == 0, Entries: imported}), nil
}

func TaskRefs(p store.Project, ids []string) ([]model.RecordRef, error) {
	state, err := store.Load(p)
	if err != nil {
		return nil, err
	}
	refs := make([]model.RecordRef, 0, len(ids))
	for _, id := range ids {
		rec, ok := state.Snapshot().Current(reduce.Ident{Project: p.ID, ID: model.ID(id)})
		if !ok || rec.Kind != model.Task {
			return nil, fmt.Errorf("no admitted task %s", id)
		}
		refs = append(refs, model.RecordRef{Project: p.ID, RecordID: rec.Key.ID, Revision: rec.Key.Revision})
	}
	return refs, nil
}
