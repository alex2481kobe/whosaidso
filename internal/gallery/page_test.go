// Page tests prove file-read bounds, selection across pages, and explicit
// availability; HTTP parsing and browser behavior are tested at their boundaries.
package gallery

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func TestGalleryPageOnlyObservesSelectedFiles(t *testing.T) {
	p := pageProject(t, 51)
	for _, tc := range []struct {
		offset, first, last, count int
		previous, next             *int
	}{{0, 150, 127, 24, nil, new(24)}, {24, 126, 103, 24, new(0), new(48)}, {48, 102, 100, 3, new(24), nil}, {math.MaxInt, 102, 100, 3, new(24), nil}} {
		observed := []model.ID{}
		page, err := readPage(p, PageRequest{Offset: tc.offset}, func(p store.Project, r reduce.GalleryEntry) Shown {
			observed = append(observed, r.Add.EntryID)
			return observeEntry(p, r)
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(observed) != tc.count || len(page.Entries) != tc.count || page.Total != 51 {
			t.Fatalf("offset %d observed %d files, returned %d of %d; want %d of 51", tc.offset, len(observed), len(page.Entries), page.Total, tc.count)
		}
		if observed[0] != pageID(tc.first) || observed[len(observed)-1] != pageID(tc.last) {
			t.Fatalf("offset %d observed wrong files: %v", tc.offset, observed)
		}
		equal := func(a, b *int) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
		if !equal(page.Previous, tc.previous) || !equal(page.Next, tc.next) {
			t.Fatalf("offset %d navigation: previous %v next %v", tc.offset, page.Previous, page.Next)
		}
	}
}

func TestGalleryFiltersAndOrdersBeforePaging(t *testing.T) {
	p := pageProject(t, 51)
	cases := []struct {
		request            PageRequest
		total, first, last int
	}{
		{PageRequest{Search: "NEEDLE"}, 2, 129, 101},
		{PageRequest{Kind: Video}, 17, 149, 101},
		{PageRequest{Kind: Video, Sort: "oldest"}, 17, 101, 149},
		{PageRequest{Sort: "title"}, 51, 150, 122},
	}
	for _, tc := range cases {
		page, err := ReadPage(p, tc.request)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != tc.total || page.Entries[0].ID != string(pageID(tc.first)) || page.Entries[len(page.Entries)-1].ID != string(pageID(tc.last)) {
			t.Fatalf("%+v: total %d entries %+v", tc.request, page.Total, page.Entries)
		}
	}
	pageAdmit(t, p, &model.GalleryRetract{EntryID: pageID(150)})
	page, err := ReadPage(p, PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 50 || page.Entries[0].ID != string(pageID(149)) {
		t.Fatalf("retracted entry remained: %+v", page)
	}
	empty, err := ReadPage(p, PageRequest{Search: "no such entry"})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Total != 0 || len(empty.Entries) != 0 || empty.Next != nil || empty.Previous != nil || empty.Offset != 0 {
		t.Fatalf("empty page: %+v", empty)
	}
}

func TestGalleryRejectsInvalidPagesBeforeReading(t *testing.T) {
	p := store.Project{Root: t.TempDir()}
	for _, r := range []PageRequest{{Offset: -1}, {Sort: "invented"}, {Kind: "invented"}, {Search: strings.Repeat("x", 513)}, {Search: string([]byte{0xff})}} {
		if _, err := ReadPage(p, r); err == nil || !strings.Contains(err.Error(), "gallery") {
			t.Fatalf("invalid page %+v was not refused before the ledger: %v", r, err)
		}
	}
}

func TestGalleryAvailabilityDoesNotInventComparisonResults(t *testing.T) {
	p := pageProject(t, 4)
	// Control: every pin can be compared before its file is changed or removed.
	control, err := ReadPage(p, PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range control.Entries {
		if e.Status != "present" {
			t.Fatalf("control: %+v", e)
		}
	}
	if err := os.WriteFile(filepath.Join(p.Root, "screen-00.png"), []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(p.Root, "screen-01.mp4")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(p.Root, "screen-02.txt")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	page, err := ReadPage(p, PageRequest{Sort: "oldest"})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"changed", "missing", "unknown", "present"} {
		if got := page.Entries[i]; got.Status != want || (want == "unknown" && got.StatusReason == "") {
			t.Fatalf("entry %d: %+v, want %s", i, got, want)
		}
	}
	if page.Entries[1].Kind != Video {
		t.Fatalf("missing media lost its preview kind: %+v", page.Entries[1])
	}
	listed, err := List(p)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range listed {
		if e.Status != page.Entries[i].Status {
			t.Fatalf("list and page disagree: %+v / %+v", e, page.Entries[i])
		}
	}
	// A readable directory at a long, valid locator is not a comparable file;
	// its diagnostic is marked and bounded rather than expanding the page reply.
	locator := strings.Repeat("a", 200) + "/" + strings.Repeat("b", 200)
	if err := os.MkdirAll(filepath.Join(p.Root, filepath.FromSlash(locator)), 0700); err != nil {
		t.Fatal(err)
	}
	records, err := Entries(p)
	if err != nil {
		t.Fatal(err)
	}
	record := records[0]
	record.Add.File.Path = locator
	observed := observeEntry(p, record)
	if observed.Status != "unknown" || utf8.RuneCountInString(observed.StatusReason) > 240 || !strings.HasSuffix(observed.StatusReason, "…") {
		t.Fatalf("unbounded diagnostic: %+v", observed)
	}

}
