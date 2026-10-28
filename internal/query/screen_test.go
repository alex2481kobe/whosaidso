// Viewer lists must search the complete ledger while returning bounded rows.
// Fixture payloads deliberately dwarf previews, and CLI answers stay complete.
package query

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func TestScreenPagesBoundTextAndSearchBeyondTheFirstPage(t *testing.T) {
	p := screenProject(t)
	events := []model.TypedEvent{}
	for i := 1; i <= 55; i++ {
		task := testTask(i)
		task.Spec.Intent = strings.Repeat("界", 1000) + " marker " + string(testID(i))
		events = append(events, task)
	}
	appendEvents(t, p, 1001, events...)
	read := func(r ScreenRequest) *ScreenAnswer {
		t.Helper()
		a, err := ReadScreen(p, r)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	first := read(ScreenRequest{View: "records"})
	if len(first.Rows) != 24 || first.Total != 55 || first.Next == nil || *first.Next != 24 || first.Previous != nil || first.Counts["TASK"] != 55 {
		t.Fatalf("first page: rows=%d total=%d counts=%v", len(first.Rows), first.Total, first.Counts)
	}
	if _, ok := first.Counts["READY"]; ok {
		t.Fatal("startup evaluated unrequested work totals")
	}
	totals := read(ScreenRequest{View: "home", StatusCounts: true})
	if totals.Counts["READY"] != 55 {
		t.Fatalf("work status totals: %v", totals.Counts)
	}
	b, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > 40000 || !strings.HasSuffix(first.Rows[0].Title, "…") || len([]rune(first.Rows[0].Title)) != 256 {
		t.Fatalf("preview body %d bytes, title %d runes", len(b), len([]rune(first.Rows[0].Title)))
	}
	second := read(ScreenRequest{View: "records", Offset: 24})
	if len(second.Rows) != 24 || second.Previous == nil || *second.Previous != 0 || first.Rows[0].ID == second.Rows[0].ID {
		t.Fatalf("second page: %+v", second)
	}
	last := read(ScreenRequest{View: "records", Offset: 999999})
	if last.Offset != 48 || len(last.Rows) != 7 || last.Next != nil {
		t.Fatalf("last page: %+v", last)
	}
	found := read(ScreenRequest{View: "records", Search: "marker " + string(testID(1)), Kind: "task"})
	if found.Total != 1 || len(found.Rows) != 1 || found.Rows[0].ID != testID(1) {
		t.Fatalf("full search behind preview and first page: %+v", found)
	}
	wrongKind := read(ScreenRequest{View: "records", Kind: "CLAIM"})
	if wrongKind.Total != 0 {
		t.Fatal("kind filter was ignored")
	}
	wrongStatus := read(ScreenRequest{View: "home", Status: "CLOSED"})
	if wrongStatus.Total != 0 {
		t.Fatal("status filter was ignored")
	}
	empty := read(ScreenRequest{View: "records", Search: "no matching record"})
	if empty.Total != 0 || len(empty.Rows) != 0 || empty.Counts["TASK"] != 55 {
		t.Fatalf("filtered totals: %+v", empty)
	}
	full := showOf(t, p, "")
	if len(full.Records) != 55 || !strings.Contains(full.Records[0].Fact.Task.Intent, "marker") {
		t.Fatal("viewer limits changed the CLI answer")
	}
	for _, view := range []string{"home", "history"} {
		a := read(ScreenRequest{View: view})
		if len(a.Rows) > 24 || (view == "home" && (a.Total != 0 || a.Counts["READY"] != 55)) || (view == "history" && a.Counts["events"] < 55) {
			t.Fatalf("unbounded %s: %+v", view, a)
		}
	}
}

func TestScreenOrderingAndInvalidRequests(t *testing.T) {
	p := screenProject(t)
	a, b := testTask(1), testTask(2)
	a.Spec.Intent = "zeta"
	b.Spec.Intent = "alpha"
	appendEvents(t, p, 1001, a, b)
	for _, test := range []struct {
		sort string
		id   model.ID
	}{{"recent", testID(2)}, {"oldest", testID(1)}, {"title", testID(2)}} {
		page, err := ReadScreen(p, ScreenRequest{View: "records", Sort: test.sort})
		if err != nil || page.Rows[0].ID != test.id {
			t.Fatalf("sort %s: %+v %v", test.sort, page, err)
		}
	}
	for _, r := range []ScreenRequest{{View: "invalid"}, {View: "records", Offset: -1}, {View: "records", Sort: "invalid"}, {View: "records", Kind: "invalid"}, {View: "records", Search: strings.Repeat("x", 513)}, {View: "records", Search: "\xff"}, {View: "history", Kind: "TASK"}} {
		if err := CheckScreen(r); err == nil {
			t.Fatalf("accepted invalid request %+v", r)
		}
	}
}

func TestScreenStatusesAgreeWithCompleteRecordProjections(t *testing.T) {
	p := screenProject(t)
	viewsWorld(t, p)
	page, err := ReadScreen(p, ScreenRequest{View: "records"})
	if err != nil {
		t.Fatal(err)
	}
	rows := page.Rows
	for offset := page.Next; offset != nil; {
		next, err := ReadScreen(p, ScreenRequest{View: "records", Offset: *offset})
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, next.Rows...)
		offset = next.Next
	}
	full := showOf(t, p, "")
	if len(rows) != len(full.Records)+len(*full.Sources) {
		t.Fatalf("summary records %d, complete records %d", len(rows), len(full.Records))
	}
	byID := map[model.ID]ScreenRow{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	for _, record := range full.Records {
		row := byID[record.Ref.RecordID]
		want := ""
		switch {
		case record.Task != nil:
			want = string(record.Task.Status)
		case record.Claim != nil:
			want = string(record.Claim.Status)
		case record.Decision != nil:
			want = string(record.Decision.Status)
		case record.Instrument != nil:
			want = "See validation and trust in record"
		}
		text, _ := record.Fact.SemanticText()
		if row.Status != want || row.Title != screenPreview(text) {
			t.Fatalf("summary of %s: %+v, complete status %s", record.Ref.RecordID, row, want)
		}
	}
	state, err := store.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	heads := state.Snapshot().RecordHeads()
	heads[0].Text = "mutated"
	summary, ok := state.Snapshot().RecordSummary(reduce.Ident{Project: p.ID, ID: testID(1)})
	if !ok || summary.NextActor == nil {
		t.Fatal("fixture task summary missing")
	}
	summary.NextActor.ID = "mutated"
	fresh, _ := state.Snapshot().RecordSummary(reduce.Ident{Project: p.ID, ID: testID(1)})
	if state.Snapshot().RecordHeads()[0].Text == "mutated" || fresh.NextActor.ID == "mutated" {
		t.Fatal("summary exposed mutable snapshot data")
	}
}

func screenProject(t *testing.T) store.Project {
	t.Helper()
	p := testProject(t)
	if err := os.WriteFile(filepath.Join(p.Root, "whosaidso.toml"), []byte("id = 'example/query-tests'\nledger = '.whosaidso/events'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestActionScreensRefuseUnsupportedFilters(t *testing.T) {
	for _, request := range []ScreenRequest{{View: "home", Search: "needle"}, {View: "home", Kind: "TASK"}, {View: "home", Sort: "title"}, {View: "tasks", Kind: "CLAIM"}, {View: "tasks", StatusCounts: true}} {
		if CheckScreen(request) == nil {
			t.Fatalf("filter was silently ignored: %+v", request)
		}
		if _, err := ReadScreenFrom(store.Project{ID: projectID}, request, store.State{}); err == nil {
			t.Fatalf("provided-prefix read silently ignored a filter: %+v", request)
		}
	}
}
