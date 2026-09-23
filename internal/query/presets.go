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

// taskBuckets holds every current task, described once per query and filed
// under its status, each bucket in currentOf's record order. with hands out a
// fresh slice, so no two answer sections share a backing array; no query reads
// one bucket into two sections, so no described record gains a second owner.
type taskBuckets map[reduce.TaskStatus][]Record

func describeTasks(s reduce.Snapshot) taskBuckets {
	out := taskBuckets{}
	for _, fact := range currentOf(s, model.Task) {
		r := describe(s, fact)
		out[r.Task.Status] = append(out[r.Task.Status], r)
	}
	return out
}

func (b taskBuckets) with(status reduce.TaskStatus) []Record {
	return append([]Record{}, b[status]...)
}

func statePreset(s reduce.Snapshot, tasks taskBuckets) *Preset {
	p := &Preset{Attention: []Attention{}}
	claimsAndRulings(s, p)
	p.Closed = list(tasks.with(reduce.StatusClosed))
	all, notes := runs(s, func(reduce.Invocation) bool { return true })
	p.Runs, p.Attention = list(all), append(p.Attention, notes...)
	return p
}

func nowPreset(s reduce.Snapshot, tasks taskBuckets) *Preset {
	p := &Preset{Attention: []Attention{}}
	flying := tasks.with(reduce.StatusInFlight)
	ids := map[reduce.Ident]bool{}
	for _, r := range flying {
		ids[reduce.Ident{Project: r.Fact.Key.Project, ID: r.Fact.Key.ID}] = true
	}
	live, notes := runs(s, func(inv reduce.Invocation) bool {
		return ids[reduce.Ident{Project: inv.Attempt.Project, ID: inv.Attempt.Task}]
	})
	p.InFlight, p.Decisions, p.Runs = list(flying), openDecisions(s), list(live)
	p.Attention = append(notes, owedNow(tasks)...)
	return p
}

// owedNow is NOW's second half: a BLOCKED task waits on someone acting now,
// the same way an OPEN decision does. Every reason owed on the task itself (an
// open hold of any kind, awaiting acceptance, owed reconciliation) is raised
// with its waiting actor, known or UNKNOWN. A computed unmet prerequisite is
// not: what it waits on is another record, which is listed in its own right.
// The full blocked record stays in TODO; this names it so NOW cannot read empty.
func owedNow(tasks taskBuckets) []Attention {
	out := []Attention{}
	for _, r := range tasks.with(reduce.StatusBlocked) {
		out = append(out, owedBy(asRef(r.Fact.Key), label(r.Fact.Task.Intent), r.Task.Reasons)...)
	}
	return out
}

// owedBy raises one blocked task's owed reasons, as owedNow describes.
func owedBy(ref model.RecordRef, name string, reasons []reduce.BlockedReason) []Attention {
	out := []Attention{}
	for _, reason := range reasons {
		if reason.Kind == reduce.ReasonPrerequisite && reason.BlockerID == "" {
			continue
		}
		why := reason.Kind + ": " + reason.Detail
		if reason.BlockerID != "" {
			why = fmt.Sprintf("%s hold %s: %s", reason.Kind, reason.BlockerID, reason.Detail)
		}
		out = append(out, Attention{Kind: "task-blocked-owed", Ref: ref, Label: name, Reason: why, WaitingActor: actor(reason.Actor)})
	}
	return out
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
func todoInto(s reduce.Snapshot, tasks taskBuckets, p *Preset, limit int) {
	blocked := tasks.with(reduce.StatusBlocked)
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
	ready := tasks.with(reduce.StatusReady)
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
	p.Attention = append(p.Attention, closureNotes(c)...)
}

// closureNotes raises every cycle and every unresolved link, mandatory or optional.
func closureNotes(c Closure) []Attention {
	out := []Attention{}
	for _, cycle := range c.Cycles {
		parts := []string{}
		for _, r := range cycle {
			parts = append(parts, fmt.Sprintf("%s@%d", r.RecordID, r.Revision))
		}
		out = append(out, Attention{Kind: "closure-cycle", Ref: c.Root, Label: "mandatory closure", Reason: strings.Join(parts, " -> ")})
	}
	for _, n := range append(append([]ClosureNode{}, c.Mandatory...), c.Optional...) {
		if n.Unresolved != nil {
			out = append(out, Attention{Kind: "unresolved-link", Ref: n.Ref, Label: "unresolved " + n.Via[0].Relation, Reason: n.Unresolved.Reason})
		}
	}
	return out
}

func preset(project store.Project, s reduce.Snapshot, request Request, a *Answer) error {
	p := &Preset{Attention: []Attention{}}
	switch request.Command {
	case "instruments":
		instrumentsInto(s, p)
	case "state":
		p = statePreset(s, describeTasks(s))
	case "now":
		p = nowPreset(s, describeTasks(s))
	case "todo":
		var err error
		if a.Intake, err = pending(project, s); err != nil {
			return err
		}
		todoInto(s, describeTasks(s), p, request.Limit)
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
	case "continue":
		root, _ := s.Current(reduce.Ident{Project: project.ID, ID: request.ID})
		if root.Kind != model.Task {
			return fmt.Errorf("continue requires a TASK; %s is a %s", request.ID, root.Kind)
		}
		a.Records = append(a.Records, describe(s, root))
		tasks := describeTasks(s)
		c := continuation(s, root, request, nowPreset(s, tasks), statePreset(s, tasks))
		p.Continue = &c
		closureAttention(c.Closure, p)
		for _, v := range c.Runs {
			if v.Scope.WithinTaskScope == reduce.TruthFalse {
				p.Attention = append(p.Attention, Attention{Kind: "run-outside-task-scope", Ref: asRef(root.Key), Label: "run " + string(v.Invocation),
					Reason: "inputs outside declared source_paths: " + strings.Join(v.Scope.Outside, ", ")})
			}
		}
	}
	a.Preset = p
	return nil
}
