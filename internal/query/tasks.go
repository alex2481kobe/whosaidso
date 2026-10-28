// Task nesting is a view of current open obligations linked by prerequisites.
// It introduces no ledger records and never infers links to unresolved targets.
package query

import (
	"fmt"
	"strings"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
)

type taskTree struct {
	facts    map[model.ID]reduce.RecordSummary
	children map[model.ID][]model.ID
	roots    []model.ID
	note     string
}

func openTaskTree(s reduce.Snapshot) taskTree {
	tree := taskTree{facts: map[model.ID]reduce.RecordSummary{}, children: map[model.ID][]model.ID{}}
	order := []model.ID{}
	for _, head := range s.RecordHeads() {
		if head.Kind != model.Task {
			continue
		}
		fact, _ := s.RecordSummary(reduce.Ident{Project: head.Key.Project, ID: head.Key.ID})
		if fact.Status == string(reduce.StatusClosed) {
			continue
		}
		tree.facts[head.Key.ID] = fact
		order = append(order, head.Key.ID)
	}
	nested := map[model.ID]bool{}
	for _, id := range order {
		seen := map[model.ID]bool{}
		for _, ref := range tree.facts[id].Prerequisites {
			child, ok := tree.facts[ref.RecordID]
			if !ok || ref.Project != s.Project() || ref.Revision == 0 || ref.Revision > child.Key.Revision || seen[ref.RecordID] {
				continue
			}
			tree.children[id] = append(tree.children[id], ref.RecordID)
			nested[ref.RecordID] = true
			seen[ref.RecordID] = true
		}
	}
	for _, id := range order {
		if !nested[id] {
			tree.roots = append(tree.roots, id)
		}
	}
	reached := map[model.ID]bool{}
	tree.walk(tree.roots, reached)
	if missing := len(tree.facts) - len(reached); missing > 0 {
		tree.note = fmt.Sprintf("%d open tasks have no top-level ancestor because their prerequisite links contain a cycle. Use --under ID to inspect them.", missing)
	}
	return tree
}

func (tree taskTree) walk(queue []model.ID, seen map[model.ID]bool) {
	queue = append([]model.ID(nil), queue...)
	for len(queue) > 0 {
		id := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		queue = append(queue, tree.children[id]...)
	}
}

func (tree taskTree) rows(under model.ID, actorFilter string) []ScreenRow {
	ids := tree.roots
	if under != "" {
		ids = tree.children[under]
	}
	rows := []ScreenRow{}
	for _, id := range ids {
		fact := tree.facts[id]
		if actorFilter != "" {
			match := fact.NextActor != nil && fact.NextActor.ID != "" && fact.NextActor.ID == actorFilter
			for _, holder := range fact.Holders {
				match = match || holder.ID != "" && holder.ID == actorFilter
			}
			if !match {
				continue
			}
		}
		who := "UNKNOWN"
		if fact.NextActor != nil {
			who = actorName(*fact.NextActor)
		}
		if len(fact.Holders) > 0 {
			names := []string{}
			for _, holder := range fact.Holders {
				names = append(names, actorName(holder))
			}
			who = strings.Join(names, ", ")
		}
		seen := map[model.ID]bool{id: true}
		tree.walk(tree.children[id], seen)
		rows = append(rows, ScreenRow{ID: id, Kind: "TASK", Title: screenPreview(fact.Text), Status: fact.Status, Actor: screenPreview(who), Nested: len(seen) - 1, Sequence: fact.Origin.Sequence, order: fact.Origin.EventIndex, sortTitle: fact.Text})
	}
	return rows
}

type TaskListAnswer struct {
	ViewHeader
	Under model.ID    `json:"under,omitempty"`
	Tasks []ScreenRow `json:"tasks"`
	Total int         `json:"total"`
	Open  int         `json:"open"`
	Note  string      `json:"note,omitempty"`
}

func taskList(s reduce.Snapshot, h ViewHeader, r ViewRequest) *TaskListAnswer {
	tree := openTaskTree(s)
	a := &TaskListAnswer{ViewHeader: h, Under: r.Under, Tasks: []ScreenRow{}, Open: len(tree.facts), Note: tree.note}
	if r.Under != "" {
		if _, ok := tree.facts[r.Under]; !ok {
			a.Result = "UNKNOWN"
			a.Reason = "no open task with this ID at this watermark"
			return a
		}
	}
	rows := tree.rows(r.Under, r.Actor)
	a.Total = len(rows)
	if r.Limit > 0 && len(rows) > r.Limit {
		rows = rows[:r.Limit]
	}
	a.Tasks = rows
	return a
}

func taskScreen(s reduce.Snapshot, r ScreenRequest, a *ScreenAnswer) *ScreenAnswer {
	tree := openTaskTree(s)
	a.Note = tree.note
	a.Counts = map[string]int{"open": len(tree.facts)}
	if r.Under != "" {
		fact, ok := tree.facts[r.Under]
		if !ok {
			a.Result = "UNKNOWN"
			a.Reason = "no open task with this ID at this watermark"
			return a
		}
		a.Parent = &ScreenRow{ID: r.Under, Title: screenPreview(fact.Text)}
	}
	rows := tree.rows(r.Under, "")
	selected := []ScreenRow{}
	search := strings.ToLower(strings.TrimSpace(r.Search))
	for _, row := range rows {
		fact := tree.facts[row.ID]
		if r.Status != "" && r.Status != row.Status {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(fact.Text+" "+string(row.ID)+" "+row.Actor), search) {
			continue
		}
		selected = append(selected, row)
	}
	sortScreenRows(selected, r.Sort)
	end := a.bounds(len(selected), r.Offset)
	a.Rows = append(a.Rows, selected[a.Offset:end]...)
	return a
}
