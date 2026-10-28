// Overview selects only open status counts and work requiring action. Full
// record browsing, intake and history belong to their respective views.
package query

import (
	"strings"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
)

func overview(s reduce.Snapshot, r ScreenRequest, a *ScreenAnswer) *ScreenAnswer {
	a.Note = ""
	a.Counts = map[string]int{"READY": 0, "BLOCKED": 0, "IN FLIGHT": 0}
	rows := []ScreenRow{}
	for _, head := range s.RecordHeads() {
		if head.Kind != model.Task && head.Kind != model.Decision {
			continue
		}
		fact, _ := s.RecordSummary(reduce.Ident{Project: head.Key.Project, ID: head.Key.ID})
		if head.Kind == model.Task && fact.Status != string(reduce.StatusClosed) {
			a.Counts[fact.Status]++
		}
		group := ""
		actors := []string{}
		if fact.Status == string(reduce.StatusInFlight) {
			group = "in_flight"
			for _, holder := range fact.Holders {
				actors = append(actors, actorName(holder))
			}
		} else if fact.AwaitingDecision {
			group = "awaiting_decision"
			for _, waiting := range fact.WaitingActors {
				actors = append(actors, actorName(waiting))
			}
		}
		if group == "" {
			continue
		}
		who := strings.Join(actors, ", ")
		if who == "" {
			who = "UNKNOWN"
		}
		row := ScreenRow{ID: head.Key.ID, Kind: string(head.Kind), Title: screenPreview(head.Text), Actor: screenPreview(who), Status: fact.Status, Group: group, Sequence: head.Origin.Sequence}
		if r.Status != "" && r.Status != row.Status {
			continue
		}
		rows = append(rows, row)
	}
	end := a.bounds(len(rows), r.Offset)
	a.Rows = append(a.Rows, rows[a.Offset:end]...)
	return a
}
