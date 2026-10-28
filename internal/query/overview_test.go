// Owner overview exposes open status totals, live holders and owed decisions.
// Historical records and completed work must not obscure actions still owed.
package query

import (
	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"testing"
)

func TestOverviewShowsOnlyOwnerActionsAndOpenStatusCounts(t *testing.T) {
	p := screenProject(t)
	viewsWorld(t, p)
	page, err := ReadScreen(p, ScreenRequest{View: "home"})
	if err != nil {
		t.Fatal(err)
	}
	todo := view_(t, p, ViewRequest{View: "todo"}).(*TodoAnswer)
	if page.Counts["READY"] != todo.Totals.Ready || page.Counts["IN FLIGHT"] != todo.Totals.InFlight || page.Counts["BLOCKED"] != todo.Totals.Blocked+todo.Totals.AwaitingAcceptance || len(page.Counts) != 3 {
		t.Fatalf("open counts: %v vs %+v", page.Counts, todo.Totals)
	}
	seen := map[model.ID]ScreenRow{}
	for _, row := range page.Rows {
		seen[row.ID] = row
		if row.Group != "in_flight" && row.Group != "awaiting_decision" {
			t.Fatalf("non-action row: %+v", row)
		}
		if row.Kind != string(model.Task) && row.Kind != string(model.Decision) {
			t.Fatalf("unrelated kind: %+v", row)
		}
		if row.Status == string(reduce.StatusClosed) || row.Status == string(reduce.StatusReady) {
			t.Fatalf("non-action task: %+v", row)
		}
	}
	if seen[testID(15)].Actor != "worker" || seen[testID(15)].Group != "in_flight" {
		t.Fatalf("live holder: %+v", seen[testID(15)])
	}
	if seen[testID(5)].Group != "awaiting_decision" || seen[testID(33)].Group != "awaiting_decision" {
		t.Fatal("missing acceptance or open decision")
	}
	if _, ok := seen[testID(9)]; ok {
		t.Fatal("closed task shown")
	}
}
