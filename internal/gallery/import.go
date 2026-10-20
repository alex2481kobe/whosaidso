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

// ImportCollapse identifies legacy list positions (one-based) and their ids.
type ImportCollapse struct {
	Record, Into           int
	LegacyID, IntoLegacyID string
}

type ImportResult struct {
	Events    []model.TypedEvent
	Collapsed []ImportCollapse
	Skipped   []model.GalleryImportSkip
}

func Import(p store.Project, by model.Actor, now time.Time) (ImportResult, error) {
	result := ImportResult{}
	file, err := store.GalleryFile(p.ID)
	if err != nil {
		return ImportResult{}, err
	}
	state, err := store.Load(p)
	if err != nil {
		return ImportResult{}, err
	}
	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return ImportResult{}, err
	}
	var old []Entry
	if err := json.Unmarshal(raw, &old); err != nil {
		return ImportResult{}, err
	}
	inventory := make([]model.Digest, 0, len(old))
	prepared := make([]model.GalleryAdd, 0, len(old))
	first := map[model.Digest]int{}
	for i, e := range old {
		add, err := Prepare(p.Root, e.Path, e.Title, e.Note, by, nil, now)
		if errors.Is(err, os.ErrNotExist) {
			result.Skipped = append(result.Skipped, model.GalleryImportSkip{LegacyID: e.ID, Path: e.Path, Title: e.Title, Reason: "file missing"})
			continue
		}
		if err != nil {
			return ImportResult{}, err
		}
		add.AddedAt = e.AddedAt.UTC()
		identity := model.GalleryImportIdentity(add.File, add.AddedAt)
		if into, exists := first[identity]; exists {
			result.Collapsed = append(result.Collapsed, ImportCollapse{Record: i + 1, Into: into + 1, LegacyID: e.ID, IntoLegacyID: old[into].ID})
			continue
		}
		first[identity] = i
		add.ImportID = string(identity)
		prepared = append(prepared, add)
		inventory = append(inventory, identity)
	}
	source := model.GallerySource(inventory, result.Skipped...)
	if state.Snapshot().GalleryImported(source) {
		return result, nil
	}
	entries, err := Entries(p)
	if err != nil {
		return ImportResult{}, err
	}
	out := []model.TypedEvent{}
	imported := []model.ID{}
	reused := []model.GalleryImportReuse{}
	for _, add := range prepared {
		match, byPin := existingEntry(entries, add)
		if match == nil {
			out = append(out, &add)
			imported = append(imported, add.EntryID)
			continue
		}
		imported = append(imported, match.Add.EntryID)
		if byPin {
			reused = append(reused, model.GalleryImportReuse{EntryID: match.Add.EntryID, AddedAt: add.AddedAt})
		}
	}
	result.Events = append(out, &model.GalleryImport{Source: source, Inventory: inventory, EmptySource: len(inventory) == 0 && len(result.Skipped) == 0, Entries: imported, Skipped: result.Skipped, Reused: reused})
	return result, nil
}

// existingEntry finds what already holds a legacy entry: the entry an earlier
// import made for the same identity, else an entry added by hand (no import id)
// with the same file pin. Imported entries match by identity only, because the
// same file listed under two dates is two legacy entries.
func existingEntry(entries []reduce.GalleryEntry, add model.GalleryAdd) (match *reduce.GalleryEntry, byPin bool) {
	for i := range entries {
		if entries[i].Add.ImportID == add.ImportID {
			return &entries[i], false
		}
	}
	for i := range entries {
		if entries[i].Add.ImportID == "" && entries[i].Add.File == add.File {
			return &entries[i], true
		}
	}
	return nil, false
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
