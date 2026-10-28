// Every batch names the whole admitted gallery revision, so a first-batch poll
// notices retractions elsewhere. Local availability does not define that revision.
package gallery

import (
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func TestPageRevisionIncludesRetractionsBeyondTheSelectedBatch(t *testing.T) {
	p := pageProject(t, 51)
	read := func(request PageRequest) Page {
		t.Helper()
		page, err := readPage(p, request, func(p store.Project, e reduce.GalleryEntry) Shown { return Shown{Entry: localEntry(p, e)} })
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	before := read(PageRequest{})
	if !model.ValidDigest(before.Revision) {
		t.Fatal("gallery revision is not a digest")
	}
	filtered := read(PageRequest{Search: "Screen 00", Sort: "title"})
	if before.Revision != filtered.Revision {
		t.Fatal("filter changed the admitted gallery revision")
	}
	pageAdmit(t, p, &model.GalleryRetract{EntryID: pageID(100)})
	after := read(PageRequest{})
	if len(before.Entries) != len(after.Entries) || before.Revision == after.Revision {
		t.Fatal("unselected retraction was invisible to a first-batch reader")
	}
	for i, e := range before.Entries {
		if e.ID != after.Entries[i].ID {
			t.Fatal("control retraction changed the selected first batch")
		}
	}
}
