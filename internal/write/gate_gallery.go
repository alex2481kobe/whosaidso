// Gallery admission verifies external pins without materializing media blobs.
// Replay and the identity/actor approval rule remain entirely in reduce.
package write

import (
	"fmt"

	"github.com/alex2481kobe/whosaidso/internal/gallery"
	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func gateGalleryPin(p store.Project, pin model.GalleryFile) error {
	path := gallery.ResolvePath(p.Root, pin.Path)
	observed, err := gallery.Pin(path)
	if err != nil {
		return admissionFault("gallery-unavailable", "file", fmt.Sprintf("missing gallery file %s: %v", pin.Path, err))
	}
	if !observed.SameContent(pin) {
		return admissionFault("gallery-changed", "file", "changed gallery file "+pin.Path+"; add its current content as a new entry")
	}
	return nil
}

func gateGallery(ctxProject store.Project, after reduce.Snapshot, event model.TypedEvent) error {
	if e, ok := event.(*model.GalleryAdd); ok {
		return gateGalleryPin(ctxProject, e.File)
	}
	entries := after.Gallery()
	check := func(id model.ID) error {
		for _, entry := range entries {
			if entry.Add.EntryID == id {
				return gateGalleryPin(ctxProject, entry.Add.File)
			}
		}
		return admissionFault("unknown-reference", "entry_id", "no admitted gallery entry "+string(id))
	}
	switch e := event.(type) {
	case *model.GalleryApprove:
		return check(e.EntryID)
	case *model.TaskClose:
		if e.Outcome != model.ClosureSuccess {
			return nil
		}
		for _, r := range after.GalleryRequirements(e.Task) {
			if err := check(r.EntryID); err != nil {
				return fmt.Errorf("criterion %s needs current gallery content for entry %s: %w", r.CriterionID, r.EntryID, err)
			}
		}
	}
	return nil
}
