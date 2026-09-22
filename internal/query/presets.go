package query

// Read preset routing: which admitted facts INSTRUMENTS, STATE, NOW, TODO,
// context and continue select, following the build plan's read routing
// matrix. A preset fills sections of one Preset inside the shared Answer, so
// text and JSON still render one structure. Per-record views, runs, closure,
// continuation and the provider seam live in their own files.

import (
	"fmt"
	"strings"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

var presetCommands = map[string]bool{"instruments": true, "state": true, "now": true, "todo": true, "context": true, "continue": true}
var idPresets = map[string]bool{"context": true, "continue": true}

// Preset holds only the sections its command selects; an absent section was
// not asked for, while a present empty list means none were admitted.
// Attention comes first: facts a reader must not miss.
type Preset struct {
	Attention          []Attention       `json:"attention"`
	Instruments        *[]InstrumentView `json:"instruments,omitempty"`
	Claims             *[]ClaimView      `json:"claims,omitempty"`
	Decisions          *[]DecisionView   `json:"decisions,omitempty"`
	InFlight           *[]Record         `json:"in_flight,omitempty"`
	Blocked            *[]Record         `json:"blocked,omitempty"`
	AwaitingAcceptance *[]Record         `json:"awaiting_acceptance,omitempty"`
	Ready              *[]Record         `json:"ready,omitempty"`
	Closed             *[]Record         `json:"closed,omitempty"`
	Runs               *[]RunView        `json:"runs,omitempty"`
	Limit              *LimitReport      `json:"limit,omitempty"`
	Closure            *Closure          `json:"closure,omitempty"`
	Proposals          *Proposal         `json:"proposals,omitempty"`
	Continue           *Continuation     `json:"continue,omitempty"`
}

func list[T any](xs []T) *[]T {
	xs = nonNil(xs)
	return &xs
}

func checkPresetRequest(r Request) error {
	switch {
	case r.Limit < 0:
		return fmt.Errorf("limit must be positive")
	case r.Limit > 0 && r.Command != "context" && r.Command != "continue" && r.Command != "todo":
		return fmt.Errorf("limit applies only to the optional results of context, continue and todo")
	case r.Command == "continue" && r.ID == "":
		return fmt.Errorf("continue requires a TASK ULID")
	case r.Observed != nil && r.Command != "continue":
		return fmt.Errorf("a workspace observation belongs only to continue")
	}
	return nil
}

func currentOf(s reduce.Snapshot, kind model.Kind) []reduce.Record {
	out := []reduce.Record{}
	for _, r := range s.Records() {
		if rev, _ := s.CurrentRevision(reduce.Ident{Project: r.Key.Project, ID: r.Key.ID}); r.Kind == kind && r.Key.Revision == rev {
			out = append(out, r)
		}
	}
	return out
}

func instrumentsInto(s reduce.Snapshot, p *Preset) {
	views := []InstrumentView{}
	for _, ip := range s.Instruments() {
		v, notes := instrumentView(s, ip)
		views = append(views, v)
		p.Attention = append(p.Attention, notes...)
	}
	p.Instruments = list(views)
}

// claimsAndRulings is the shared STATE/context body: every claim at every
// status, and every decided decision with its rulings.
func claimsAndRulings(s reduce.Snapshot, p *Preset) {
	claims, decided := []ClaimView{}, []DecisionView{}
	for _, c := range s.Claims() {
		claims = append(claims, claimView(s, c))
	}
	for _, d := range s.Decisions() {
		if d.Status == reduce.StatusDecided {
			decided = append(decided, decisionView(s, d))
		}
	}
	p.Claims, p.Decisions = list(claims), list(decided)
}

func openDecisions(s reduce.Snapshot) *[]DecisionView {
	open := []DecisionView{}
	for _, d := range s.Decisions() {
		if d.Status == reduce.StatusOpen {
			open = append(open, decisionView(s, d))
		}
	}
	return list(open)
}

func tasksWith(s reduce.Snapshot, keep func(Record) bool) []Record {
	out := []Record{}
	for _, fact := range currentOf(s, model.Task) {
		if r := describe(s, fact); keep(r) {
			out = append(out, r)
		}
	}
	return out
}

func statePreset(s reduce.Snapshot) *Preset {
	p := &Preset{Attention: []Attention{}}
	claimsAndRulings(s, p)
	p.Closed = list(tasksWith(s, func(r Record) bool { return r.Task.Status == reduce.StatusClosed }))
	all, notes := runs(s, func(reduce.Invocation) bool { return true })
	p.Runs, p.Attention = list(all), append(p.Attention, notes...)
	return p
}

func nowPreset(s reduce.Snapshot) *Preset {
	p := &Preset{Attention: []Attention{}}
	flying := tasksWith(s, func(r Record) bool { return r.Task.Status == reduce.StatusInFlight })
	ids := map[reduce.Ident]bool{}
	for _, r := range flying {
		ids[reduce.Ident{Project: r.Fact.Key.Project, ID: r.Fact.Key.ID}] = true
	}
	live, notes := runs(s, func(inv reduce.Invocation) bool {
		return ids[reduce.Ident{Project: inv.Attempt.Project, ID: inv.Attempt.Task}]
	})
	p.InFlight, p.Decisions, p.Runs, p.Attention = list(flying), openDecisions(s), list(live), notes
	return p
}

func hasReason(r Record, kind string) bool {
	for _, reason := range r.Task.Reasons {
		if reason.Kind == kind {
			return true
		}
	}
	return false
}

// todoInto never limits blocked work, awaiting acceptance or open decisions;
// the limit cuts only READY tasks, and says how many it cut.
func todoInto(s reduce.Snapshot, p *Preset, limit int) {
	blocked := tasksWith(s, func(r Record) bool { return r.Task.Status == reduce.StatusBlocked })
	awaiting, other := []Record{}, []Record{}
	for _, r := range blocked {
		if hasReason(r, reduce.ReasonAwaitingAcceptance) {
			awaiting = append(awaiting, r)
		}
		onlyAwaiting := len(r.Task.Reasons) > 0
		for _, reason := range r.Task.Reasons {
			onlyAwaiting = onlyAwaiting && reason.Kind == reduce.ReasonAwaitingAcceptance
		}
		if !onlyAwaiting {
			other = append(other, r)
		}
	}
	ready := tasksWith(s, func(r Record) bool { return r.Task.Status == reduce.StatusReady })
	report := &LimitReport{Requested: "none", Offered: len(ready)}
	if limit > 0 {
		report.Requested = limit
		if len(ready) > limit {
			report.Omitted, ready = len(ready)-limit, ready[:limit]
		}
	}
	p.Blocked, p.AwaitingAcceptance, p.Ready, p.Limit = list(other), list(awaiting), list(ready), report
	p.Decisions = openDecisions(s)
}

func closureAttention(c Closure, p *Preset) {
	for _, cycle := range c.Cycles {
		parts := []string{}
		for _, r := range cycle {
			parts = append(parts, fmt.Sprintf("%s@%d", r.RecordID, r.Revision))
		}
		p.Attention = append(p.Attention, Attention{"closure-cycle", c.Root, "mandatory closure", strings.Join(parts, " -> ")})
	}
	for _, n := range append(append([]ClosureNode{}, c.Mandatory...), c.Optional...) {
		if n.Unresolved != nil {
			p.Attention = append(p.Attention, Attention{"unresolved-link", n.Ref, "unresolved " + n.Via[0].Relation, n.Unresolved.Reason})
		}
	}
}

func preset(project store.Project, s reduce.Snapshot, prefix []model.Bundle, request Request, a *Answer) error {
	p := &Preset{Attention: []Attention{}}
	switch request.Command {
	case "instruments":
		instrumentsInto(s, p)
	case "state":
		p = statePreset(s)
	case "now":
		p = nowPreset(s)
	case "todo":
		var err error
		if a.Intake, err = pending(project, s, prefix); err != nil {
			return err
		}
		todoInto(s, p, request.Limit)
	case "context":
		if request.ID == "" {
			claimsAndRulings(s, p)
			break
		}
		root, _ := s.Current(reduce.Ident{Project: project.ID, ID: request.ID})
		a.Records = append(a.Records, describe(s, root))
		c := closure(s, asRef(root.Key), request.Limit)
		p.Closure = &c
		closureAttention(c, p)
		q := propose(request.Context, request.Provider, Question{Operation: OperationRetrieve, Refs: []model.RecordRef{asRef(root.Key)}})
		p.Proposals = &q
	case "continue":
		root, _ := s.Current(reduce.Ident{Project: project.ID, ID: request.ID})
		if root.Kind != model.Task {
			return fmt.Errorf("continue requires a TASK; %s is a %s", request.ID, root.Kind)
		}
		a.Records = append(a.Records, describe(s, root))
		c := continuation(s, root, request, nowPreset(s), statePreset(s))
		p.Continue = &c
		closureAttention(c.Closure, p)
		for _, v := range c.Runs {
			if v.Scope.WithinTaskScope == reduce.TruthFalse {
				p.Attention = append(p.Attention, Attention{"run-outside-task-scope", asRef(root.Key), "run " + string(v.Invocation),
					"inputs outside declared source_paths: " + strings.Join(v.Scope.Outside, ", ")})
			}
		}
	}
	a.Preset = p
	return nil
}
