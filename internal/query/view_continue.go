package query

// The continue view: resume any record. A task gets the
// rich continuation (progress, attempts, runs, what is owed); every kind gets
// its mandatory closure, optional one-hop context, the caller's fresh
// workspace observation and its attention. Each record body appears once, in
// records, keyed by exact revision; sections name records by ref. Each run
// appears once, in runs. Project-wide work lives in todo and show, not here.

import (
	"sort"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
)

type ContinueAnswer struct {
	ViewHeader
	Record   *model.RecordRef  `json:"record,omitempty"` // the root; its body is records[id@revision]
	Records  map[string]Detail `json:"records"`          // id@revision -> detail, each once
	Progress any               `json:"progress,omitempty"`
	Attempts *[]AttemptView    `json:"attempts,omitempty"`
	Runs     *[]RunDetail      `json:"runs,omitempty"` // the task's runs, then runs a claim in records observes
	Owed     *Owed             `json:"owed,omitempty"`
	// Amendments are every amendment of the root since its creation, oldest
	// first, each with what it changed: a plan item removed at any revision
	// stays visible here with the amendment and review that removed it.
	Amendments *[]Amendment `json:"amendments,omitempty"`
	Closure    *ClosureRefs `json:"closure,omitempty"`
	Context    *ContextRefs `json:"context,omitempty"`
	Observed   *Observation `json:"observed,omitempty"`
	Attention  []Attention  `json:"attention"`
	Handoff    string       `json:"handoff,omitempty"`
	Generated  string       `json:"generated,omitempty"`
}

// ClosureRef is one exact referenced revision: resolved ones have their body
// in records; an unresolved one says why and stays visible.
type ClosureRef struct {
	Ref        model.RecordRef `json:"ref"`
	Via        []Edge          `json:"via"`
	Unresolved *Unknown        `json:"unresolved,omitempty"`
}

// ClosureRefs is the mandatory closure: no limit ever cuts it.
type ClosureRefs struct {
	Root      model.RecordRef     `json:"root"`
	Mandatory []ClosureRef        `json:"mandatory"`
	Cycles    [][]model.RecordRef `json:"cycles"`
}

// ContextRefs are the optional one-hop topic refs; only resolved ones are cut.
type ContextRefs struct {
	Refs  []ClosureRef `json:"refs"`
	Limit LimitReport  `json:"limit"`
}

// Owed is what a task waits on and who owes it; Items are its prerequisites
// (a plan's items) with each target's current status.
type Owed struct {
	Status    reduce.TaskStatus `json:"status"`
	NextActor any               `json:"next_actor"`
	Reasons   []OwedReason      `json:"reasons"`
	Items     []OwedItem        `json:"items"`
}

type OwedReason struct {
	Kind         string           `json:"kind"`
	Detail       string           `json:"detail"`
	WaitingActor any              `json:"waiting_actor"`
	BlockerID    model.ID         `json:"blocker_id,omitempty"`
	Target       *model.RecordRef `json:"target,omitempty"`
}

type OwedItem struct {
	Index     int             `json:"index"`
	Kind      string          `json:"kind"`
	Target    model.RecordRef `json:"target"`
	Satisfied reduce.Truth    `json:"satisfied"`
	Waived    bool            `json:"waived"`
	Status    any             `json:"status"` // the target's current status, or Unknown
}

func closureRefs(nodes []ClosureNode) []ClosureRef {
	out := make([]ClosureRef, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, ClosureRef{Ref: n.Ref, Via: n.Via, Unresolved: n.Unresolved})
	}
	return out
}

func continueView(s reduce.Snapshot, h ViewHeader, root reduce.Record, r ViewRequest) *ContinueAnswer {
	d := newDetailer(s)
	ref := asRef(root.Key)
	a := &ContinueAnswer{ViewHeader: h, Record: &ref, Records: map[string]Detail{}, Attention: []Attention{},
		Handoff:   "none: this brief is generated on invocation and no handoff record was written",
		Generated: "disposable export; canonical state is the ledger at this answer's watermark"}
	observed := unobserved("the caller supplied no workspace observation")
	if r.Observed != nil {
		observed = *r.Observed
	}
	a.Observed = &observed
	c := closureWith(s, ref, r.Limit, locateOnly)
	a.Closure = &ClosureRefs{Root: c.Root, Mandatory: closureRefs(c.Mandatory), Cycles: c.Cycles}
	a.Context = &ContextRefs{Refs: closureRefs(c.Optional), Limit: c.Limit}
	a.Attention = append(a.Attention, closureNotes(c)...)
	changes := amendments(s, reduce.Ident{Project: root.Key.Project, ID: root.Key.ID})
	a.Amendments = &changes
	rootDetail, notes := d.detail(root)
	a.Records[recordKey(ref)] = rootDetail
	a.Attention = append(a.Attention, notes...)
	for _, n := range append(append([]ClosureNode{}, c.Mandatory...), c.Optional...) {
		if fact, ok := s.Record(n.Ref); ok && n.Unresolved == nil {
			if _, done := a.Records[recordKey(n.Ref)]; !done {
				a.Records[recordKey(n.Ref)], _ = d.detail(fact)
			}
		}
	}
	runs, seen := []RunDetail{}, map[model.ID]bool{}
	if root.Kind == model.Task {
		continueTask(s, root, rootDetail, a)
		for _, inv := range s.Invocations() {
			if inv.Attempt.Project == ref.Project && inv.Attempt.Task == ref.RecordID {
				run := runDetail(s, inv)
				runs, seen[inv.Key.InvocationID] = append(runs, run), true
				if note, outside := runAttention(run.RunView); outside {
					note.Ref = ref // named at the continued revision, as the continue read did
					a.Attention = append(a.Attention, note)
				}
			}
		}
	}
	if decision := rootDetail.Decision; decision != nil && decision.Status == reduce.StatusOpen {
		a.Attention = append(a.Attention, Attention{Kind: "decision-open", Ref: ref, Label: rootDetail.Label,
			Reason: "the decision is OPEN and authorises no work until ruled", WaitingActor: decision.WaitingActor})
	}
	for _, key := range sortedKeys(a.Records) {
		claim := a.Records[key].Claim
		if claim == nil {
			continue
		}
		for _, id := range claim.Observations {
			if seen[id] {
				continue
			}
			if inv, ok := s.Invocation(reduce.InvocationKey{Project: s.Project(), InvocationID: id}); ok {
				runs, seen[id] = append(runs, runDetail(s, inv)), true
			}
		}
	}
	a.Runs = &runs
	return a
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// continueTask fills the task-only sections from the root task.
func continueTask(s reduce.Snapshot, root reduce.Record, v Detail, a *ContinueAnswer) {
	a.Progress = unknown("the task's current revision records no progress")
	if root.Task.Progress != nil {
		a.Progress = *root.Task.Progress
	}
	attempts := []AttemptView{}
	for _, at := range s.Attempts(reduce.Ident{Project: root.Key.Project, ID: root.Key.ID}) {
		attempts = append(attempts, attemptView(at))
	}
	a.Attempts = &attempts
	owed := &Owed{Status: v.Task.Status, NextActor: v.Task.ExpectedNextActor, Reasons: []OwedReason{}, Items: []OwedItem{}}
	for _, reason := range v.Task.Reasons {
		owed.Reasons = append(owed.Reasons, OwedReason{Kind: reason.Kind, Detail: reason.Detail, WaitingActor: actor(reason.Actor),
			BlockerID: reason.BlockerID, Target: reason.Target})
	}
	for _, p := range v.Task.Prerequisites {
		owed.Items = append(owed.Items, OwedItem{Index: p.Index, Kind: p.Kind, Target: p.Target, Satisfied: p.Truth,
			Waived: p.Waived, Status: statusOf(s, p.Target)})
	}
	a.Owed = owed
	if v.Task.Status == reduce.StatusBlocked {
		a.Attention = append(a.Attention, owedBy(v.Ref, v.Label, v.Task.Reasons)...)
	}
}

// statusOf is the target identity's current status, or why it has none here.
func statusOf(s reduce.Snapshot, target model.RecordRef) any {
	if target.Project != s.Project() {
		return unknown("cross-project reference to " + string(target.Project) + " is resolved on read, and that ledger is not read here")
	}
	id := reduce.Ident{Project: target.Project, ID: target.RecordID}
	if p, ok := s.Task(id); ok {
		return p.Status
	}
	if p, ok := s.Claim(id); ok {
		return p.Status
	}
	if p, ok := s.Decision(id); ok {
		return p.Status
	}
	return unknown("no admitted task, claim or decision " + string(target.RecordID) + " carries a status here")
}
