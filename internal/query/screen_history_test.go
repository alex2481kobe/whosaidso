// History startup must decode one range, rather than loading the whole event
// model. Explicit search still scans all batches before returning one page.
package query

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

type rangeSource struct {
	store.State
	reads int
}

func (s *rangeSource) Bundles() ([]model.Bundle, error) {
	panic("default history must not load every bundle")
}
func (s *rangeSource) BundleRange(offset, count int) ([]model.Bundle, error) {
	s.reads += count
	return s.State.BundleRange(offset, count)
}

func TestHistoryStartupDecodesOnlyItsBatchPage(t *testing.T) {
	p := screenProject(t)
	for i := 1; i <= 55; i++ {
		task := testTask(i)
		if i == 1 {
			task.Spec.Intent = "unique old batch search"
		}
		appendEvents(t, p, 1000+i, task)
	}
	state, err := store.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	source := &rangeSource{State: state}
	page, err := screenFrom(p, ScreenRequest{View: "history"}, source)
	if err != nil || source.reads != 24 || len(page.Rows) != 24 || page.Total != 55 || page.Rows[0].Sequence != 55 || page.Rows[23].Sequence != 32 {
		t.Fatalf("history: rows %d total %d reads %d err %v", len(page.Rows), page.Total, source.reads, err)
	}
	oldest, err := screenFrom(p, ScreenRequest{View: "history", Sort: "oldest", Offset: 24}, source)
	if err != nil || oldest.Rows[0].Sequence != 25 || oldest.Previous == nil || *oldest.Previous != 0 {
		t.Fatalf("oldest page: %+v %v", oldest, err)
	}
	found, err := ReadScreen(p, ScreenRequest{View: "history", Search: "unique old batch search"})
	if err != nil || found.Total != 1 || found.Rows[0].Sequence != 1 {
		t.Fatalf("full history search: %+v %v", found, err)
	}
}

func TestHistoryBatchPositionsUseCanonicalOrderAfterBranchesMerge(t *testing.T) {
	left, right := screenProject(t), screenProject(t)
	appendEvents(t, left, 1001, testTask(1))
	files, err := os.ReadDir(left.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(right.Ledger, 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		raw, err := os.ReadFile(filepath.Join(left.Ledger, file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(right.Ledger, file.Name()), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	appendEvents(t, left, 1002, testTask(2))
	appendEvents(t, right, 1003, testTask(3))
	files, err = os.ReadDir(right.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		raw, err := os.ReadFile(filepath.Join(right.Ledger, file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(left.Ledger, file.Name()), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	state, err := store.Load(left)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := state.Bundles()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 3 || raw[1].Sequence != 2 || raw[2].Sequence != 2 {
		t.Fatalf("fixture must contain two authored sequence-2 siblings: %v", raw)
	}
	page, err := ReadScreen(left, ScreenRequest{View: "history"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Rows[0].Sequence != 3 || !strings.HasPrefix(page.Rows[0].Title, "Batch 3:") || page.Rows[1].Sequence != 2 {
		t.Fatalf("authored sequence confused with canonical position: %+v", page.Rows)
	}
	found, err := ReadScreen(left, ScreenRequest{View: "history", Search: "Batch 3"})
	if err != nil || found.Total != 1 || found.Rows[0].Sequence != 3 {
		t.Fatalf("search must find the displayed canonical batch: %+v %v", found, err)
	}
}
