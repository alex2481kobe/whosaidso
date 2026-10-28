// Import is the sole reader of the retired machine-local gallery list.
// It captures proposals; it never rewrites that file or admits a packet.
package gallery

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	entries, err := Entries(p)
	if err != nil {
		return ImportResult{}, err
	}
	sealed := state.Snapshot().GallerySealedIdentities()
	inventory := make([]model.Digest, 0, len(old))
	prepared := make([]model.GalleryAdd, 0, len(old))
	first := map[model.Digest]int{}
	for i, e := range old {
		add, err := Prepare(p.Root, e.Path, e.Title, e.Note, by, nil, now)
		if errors.Is(err, os.ErrNotExist) {
			// A file that vanished after the ledger took the entry is not a
			// skip: the entry keeps the identity it was sealed under.
			held, ok := heldFile(p.Root, e, entries, sealed)
			if !ok {
				result.Skipped = append(result.Skipped, model.GalleryImportSkip{LegacyID: e.ID, Path: e.Path, Title: e.Title, Reason: "file missing"})
				continue
			}
			add, err = model.GalleryAdd{File: held}, nil
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
	seal := &model.GalleryImport{Source: source, Inventory: inventory, EmptySource: len(inventory) == 0 && len(result.Skipped) == 0, Entries: imported, Skipped: result.Skipped, Reused: reused}
	// A seal that cannot be encoded must stop the import before any add is
	// captured, or a half import would be left admitted.
	if err := model.ValidateSchema(seal); err != nil {
		return ImportResult{}, err
	}
	result.Events = append(out, seal)
	return result, nil
}

// heldFile finds the file pin the ledger already holds for a legacy entry whose
// file is now gone: an entry at the same locator whose identity, made with the
// legacy date, was sealed in an earlier import or is the entry's own import id.
func heldFile(root string, e Entry, entries []reduce.GalleryEntry, sealed map[model.Digest]bool) (model.GalleryFile, bool) {
	locators := map[string]bool{e.Path: true}
	if !filepath.IsAbs(e.Path) {
		locators[filepath.ToSlash(filepath.Clean(e.Path))] = true
	} else if rel, err := filepath.Rel(root, e.Path); err == nil && filepath.IsLocal(rel) {
		locators[filepath.ToSlash(rel)] = true
	}
	for _, held := range entries {
		if !locators[held.Add.File.Path] {
			continue
		}
		identity := model.GalleryImportIdentity(held.Add.File, e.AddedAt)
		if sealed[identity] || held.Add.ImportID == string(identity) {
			return held.Add.File, true
		}
	}
	return model.GalleryFile{}, false
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
	// An entry still in the gallery wins over one retracted; with only a
	// retracted one, the legacy entry binds to it and stays removed.
	var retracted *reduce.GalleryEntry
	for i := range entries {
		if entries[i].Add.ImportID != "" || entries[i].Add.File != add.File {
			continue
		}
		if !entries[i].Retracted {
			return &entries[i], true
		}
		if retracted == nil {
			retracted = &entries[i]
		}
	}
	return retracted, retracted != nil
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
