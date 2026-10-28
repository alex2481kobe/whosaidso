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
	groups := map[string][]ScreenRow{"in_flight": {}, "awaiting_decision": {}}
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
		groups[group] = append(groups[group], row)
	}
	// Interleave the queues so either kind of action remains visible on the
	// first page even when the other queue contains many older identities.
	rows := []ScreenRow{}
	for i := 0; i < max(len(groups["in_flight"]), len(groups["awaiting_decision"])); i++ {
		for _, group := range []string{"in_flight", "awaiting_decision"} {
			if i < len(groups[group]) {
				rows = append(rows, groups[group][i])
			}
		}
	}
	end := a.bounds(len(rows), r.Offset)
	a.Rows = append(a.Rows, rows[a.Offset:end]...)
	return a
}
