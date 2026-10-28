// Gallery ordering must be deterministic across host locales, and kind sort
// must group the entire gallery before page selection.
package gallery

import (
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func TestGalleryKindSortGroupsBeforePaging(t *testing.T) {
	p := pageProject(t, 51)
	page, err := readPage(p, PageRequest{Sort: "kind"}, func(p store.Project, e reduce.GalleryEntry) Shown { return Shown{Entry: localEntry(p, e)} })
	if err != nil || len(page.Entries) != 24 {
		t.Fatalf("kind page: %+v %v", page, err)
	}
	for i, e := range page.Entries {
		expected := "image"
		if i >= 17 {
			expected = "text"
		}
		if kind, _ := Kind(e.Locator); kind != expected {
			t.Fatalf("entry %d kind %s, want %s", i, kind, expected)
		}
	}
}
