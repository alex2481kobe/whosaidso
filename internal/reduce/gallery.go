// Gallery projection and visual acceptance rules use admitted events only.
// Local file inspection and packet capture belong to internal/gallery.
package reduce

import (
	"fmt"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

type GalleryEntry struct {
	Add           model.GalleryAdd `json:"add"`
	Origin        Origin           `json:"origin"`
	AddedAt       time.Time        `json:"added_at"`
	Retracted     bool             `json:"retracted"`
	VerdictOrigin Origin           `json:"verdict_origin"`
	Verdict       string           `json:"verdict"`
	Actor         model.Actor      `json:"actor"`
	Note          string           `json:"note,omitempty"`
}

func (s *state) galleryEntries() []GalleryEntry {
	out := []GalleryEntry{}
	positions := map[model.ID]int{}
	for _, o := range s.log.all {
		switch e := s.events[o].(type) {
		case *model.GalleryAdd:
			positions[e.EntryID] = len(out)
			out = append(out, GalleryEntry{Add: *e, Origin: o, AddedAt: e.AddedAt.UTC(), Verdict: "unreviewed", Actor: model.Actor{UnknownReason: "no admitted verdict"}})
		case *model.GalleryRetract:
			if i, ok := positions[e.EntryID]; ok {
				out[i].Retracted = true
			}
		case *model.GalleryApprove:
			if i, ok := positions[e.EntryID]; ok {
				out[i].VerdictOrigin = o
				out[i].Verdict = "approved"
				out[i].Actor = e.Actor
				out[i].Note = e.Note
			}
		case *model.GalleryReject:
			if i, ok := positions[e.EntryID]; ok {
				out[i].VerdictOrigin = o
				out[i].Verdict = "rejected"
				out[i].Actor = e.Actor
				out[i].Note = e.Note
			}
		}
	}
	return out
}

func (s Snapshot) Gallery() []GalleryEntry { return deepCopySlice(s.inner().galleryEntries()) }

func (s *state) galleryEntry(id model.ID) (GalleryEntry, bool) {
	for _, e := range s.galleryEntries() {
		if e.Add.EntryID == id {
			return e, true
		}
	}
	return GalleryEntry{}, false
}

func (s *state) galleryEvent(b model.Bundle, idx int, event model.TypedEvent) error {
	fail := func(path, detail string) error { return faultAt(CodeInvalidField, b.Sequence, idx, path, detail) }
	var id model.ID
	switch e := event.(type) {
	case *model.GalleryImport:
		for _, o := range s.log.all {
			if old, ok := s.events[o].(*model.GalleryImport); ok && old.Source == e.Source {
				return fail("source", "gallery list already imported")
			}
		}
		if e.Inventory == nil || model.GallerySource(e.Inventory, e.Skipped...) != e.Source || len(e.Inventory) != len(e.Entries) || e.EmptySource != (len(e.Inventory) == 0 && len(e.Skipped) == 0) {
			return fail("inventory", "completion must name the complete source inventory; an empty source must be explicit")
		}
		usedReuse := make([]bool, len(e.Reused))
		seen := map[model.Digest]bool{}
		for i, id := range e.Entries {
			legacy := e.Inventory[i]
			if seen[legacy] {
				return fail("inventory", "duplicate source entry")
			}
			seen[legacy] = true
			entry, ok := s.galleryEntry(id)
			if !ok {
				return fail("entries", "import completion needs every imported entry admitted first")
			}
			if entry.Add.ImportID == string(legacy) {
				if model.GalleryImportIdentity(entry.Add.File, entry.Add.AddedAt) != legacy {
					return fail("entries", "import identity must match the entry's file and added time")
				}
				continue
			}
			// An entry added by hand holds the legacy entry's file pin; the
			// reuse supplies the legacy date the identity was made with.
			found := false
			for j, reuse := range e.Reused {
				if reuse.EntryID == id && entry.Add.ImportID == "" && model.GalleryImportIdentity(entry.Add.File, reuse.AddedAt) == legacy {
					usedReuse[j], found = true, true
					break
				}
			}
			if !found {
				return fail("entries", "import completion needs every imported entry admitted first")
			}
		}
		for _, used := range usedReuse {
			if !used {
				return fail("reused", "reused entry must be in the inventory")
			}
		}
		return nil
	case *model.GalleryAdd:
		if _, ok := s.galleryEntry(e.EntryID); ok {
			return fail("entry_id", "gallery entry already exists")
		}
		if e.ImportID != "" {
			if string(model.GalleryImportIdentity(e.File, e.AddedAt)) != e.ImportID {
				return fail("import_id", "import identity must match the entry's file and added time")
			}
			for _, old := range s.galleryEntries() {
				if old.Add.ImportID == e.ImportID {
					return fail("import_id", "legacy entry already imported")
				}
			}
		}
		author, ok := s.bundle.packetAuthor(idx)
		if !ok || author != e.Actor {
			return fail("actor", "must equal the packet's recorded author")
		}
		for _, task := range e.Tasks {
			if task.Project != b.Project {
				return fail("tasks", "gallery task must belong to this project")
			}
			if err := s.requireKind(b, idx, task, model.Task, "tasks"); err != nil {
				return err
			}
		}
		return nil
	case *model.GalleryRetract:
		id = e.EntryID
	case *model.GalleryApprove:
		id = e.EntryID
		if err := s.checkPacketAuthor(b, idx, e.Actor, "actor"); err != nil {
			return err
		}
	case *model.GalleryReject:
		id = e.EntryID
		if err := s.checkPacketAuthor(b, idx, e.Actor, "actor"); err != nil {
			return err
		}
	case *model.GalleryRequire:
		id = e.EntryID
		if e.Task.Project != b.Project {
			return fail("task", "gallery requirement must name this project")
		}
		rec, err := s.taskAt(b, idx, e.Task, "task")
		if err != nil {
			return err
		}
		if s.current[ident(e.Task)] != e.Task.Revision {
			return fail("task", "gallery requirement must name the current task revision")
		}
		if err := s.checkAccepter(b, idx, rec); err != nil {
			return err
		}
		found := false
		for _, c := range rec.Task.AcceptanceCriteria {
			if c.ID == e.CriterionID && c.Revision == e.CriterionRevision {
				found = true
			}
		}
		if !found {
			return fail("criterion_id", "no such acceptance criterion revision on this task")
		}
		for _, o := range s.log.all {
			if old, ok := s.events[o].(*model.GalleryRequire); ok && ident(old.Task) == ident(e.Task) && old.CriterionID == e.CriterionID {
				return fail("criterion_id", "gallery requirement already fixed for this criterion")
			}
		}
	}
	entry, ok := s.galleryEntry(id)
	if !ok || entry.Retracted {
		return fail("entry_id", "no active admitted gallery entry "+string(id))
	}
	var digest model.Digest
	switch e := event.(type) {
	case *model.GalleryApprove:
		digest = e.SHA256
	case *model.GalleryReject:
		digest = e.SHA256
	}
	if digest != "" && digest != entry.Add.File.SHA256 {
		return fail("sha256", "verdict must name the entry's current content hash")
	}
	return nil
}

func (s *state) requireGallery(b model.Bundle, idx int, rec Record) error {
	for _, bound := range s.galleryRequirements(model.RecordRef{Project: rec.Key.Project, RecordID: rec.Key.ID, Revision: rec.Key.Revision}) {
		r, o := bound.Requirement, bound.Origin
		entry, exists := s.galleryEntry(r.EntryID)
		if !exists || entry.Retracted || entry.Verdict != "approved" || model.SameActor(entry.Actor, entry.Add.Actor) || !model.SameActor(entry.Actor, r.Approver) {
			return faultAt(CodeInvalidTransition, b.Sequence, idx, "acceptance_witness_refs", fmt.Sprintf("criterion %s needs admitted gallery approval of entry %s hash %s by %s, independently of its author", r.CriterionID, r.EntryID, entry.Add.File.SHA256, r.Approver.ID))
		}
		if err := s.unseen(b, idx, "acceptance_witness_refs", "gallery requirement and approval", o, entry.VerdictOrigin); err != nil {
			return err
		}
	}
	return nil
}

// GalleryImported answers from the ledger before the legacy file is opened.
func (s Snapshot) GalleryImported(source model.Digest) bool {
	for _, o := range s.inner().log.all {
		if e, ok := s.inner().events[o].(*model.GalleryImport); ok && e.Source == source {
			return true
		}
	}
	return false
}

// GallerySealedIdentities is every legacy identity a gallery.import seal has
// listed in its inventory, whichever entry now holds it.
func (s Snapshot) GallerySealedIdentities() map[model.Digest]bool {
	out := map[model.Digest]bool{}
	for _, o := range s.inner().log.all {
		if e, ok := s.inner().events[o].(*model.GalleryImport); ok {
			for _, legacy := range e.Inventory {
				out[legacy] = true
			}
		}
	}
	return out
}

// GalleryRequirements lists the visual obligations of this task's criteria.
func (s Snapshot) GalleryRequirements(task model.RecordRef) []model.GalleryRequire {
	out := []model.GalleryRequire{}
	for _, bound := range s.inner().galleryRequirements(task) {
		out = append(out, bound.Requirement)
	}
	return deepCopySlice(out)
}

type galleryRequirement struct {
	Requirement model.GalleryRequire
	Origin      Origin
}

func (s *state) galleryRequirements(task model.RecordRef) []galleryRequirement {
	rec, ok := s.records[recordKey(task)]
	if !ok || rec.Task == nil {
		return nil
	}
	criteria := map[model.ID]bool{}
	for _, c := range rec.Task.AcceptanceCriteria {
		criteria[c.ID] = true
	}
	out := []galleryRequirement{}
	for _, o := range s.log.all {
		r, ok := s.events[o].(*model.GalleryRequire)
		if ok && ident(r.Task) == ident(task) && criteria[r.CriterionID] {
			out = append(out, galleryRequirement{Requirement: *r, Origin: o})
		}
	}
	return out
}
