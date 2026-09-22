package query

// The continue brief: NOW + STATE + one task's closure, attempts, runs and
// the caller's fresh workspace observation. It is generated on each call and
// written nowhere; there is no handoff record. The observation (HEAD, dirty,
// time) is supplied by the caller, because this package does not run git or
// read a clock, and an observation nobody made is UNKNOWN, never defaulted.

import (
	"time"

	"datum/internal/model"
	"datum/internal/reduce"
)

// Observation is the caller's fresh look at the workspace.
type Observation struct {
	Head       model.Availability[model.GitHead] `json:"head"`
	Dirty      model.Availability[bool]          `json:"dirty"`
	ObservedAt model.Availability[time.Time]     `json:"observed_at"`
}

func unobserved(reason string) Observation {
	return Observation{Head: model.Availability[model.GitHead]{State: model.Unknown, Reason: reason},
		Dirty:      model.Availability[bool]{State: model.Unknown, Reason: reason},
		ObservedAt: model.Availability[time.Time]{State: model.Unknown, Reason: reason}}
}

type AttemptView struct {
	Attempt      reduce.AttemptKey   `json:"attempt"`
	TaskRevision model.Revision      `json:"task_revision"`
	Holder       any                 `json:"holder"` // Actor or Unknown
	Live         bool                `json:"live"`
	Outcome      any                 `json:"outcome"` // attempt outcome or Unknown
	Reason       any                 `json:"reason"`
	NextAction   any                 `json:"next_action"`
	DeliveryRefs []model.ArtifactRef `json:"delivery_refs"`
}

type Continuation struct {
	Task      model.RecordRef `json:"task"`
	Observed  Observation     `json:"observed"`
	Progress  any             `json:"progress"` // model.TaskProgress or Unknown
	Attempts  []AttemptView   `json:"attempts"`
	Runs      []RunView       `json:"runs"`
	Closure   Closure         `json:"closure"`
	Now       *Preset         `json:"now"`
	State     *Preset         `json:"state"`
	Handoff   string          `json:"handoff"`
	Generated string          `json:"generated"`
}

func attemptView(a reduce.Attempt) AttemptView {
	none := unknown("no terminal receipt admitted for this attempt")
	v := AttemptView{Attempt: a.Key, TaskRevision: a.TaskRevision, Holder: actor(a.Actor), Live: a.Live(),
		Outcome: none, Reason: none, NextAction: none, DeliveryRefs: []model.ArtifactRef{}}
	if t := a.Terminal; t != nil {
		v.Outcome, v.DeliveryRefs = t.Outcome, nonNil(t.DeliveryRefs)
		v.Reason = unknown("terminal receipt recorded no reason")
		if t.Reason != "" {
			v.Reason = t.Reason
		}
		v.NextAction = unknown("terminal receipt recorded no next action")
		if t.NextAction != "" {
			v.NextAction = t.NextAction
		}
	}
	return v
}

func continuation(s reduce.Snapshot, task reduce.Record, request Request, now, state *Preset) Continuation {
	ref := asRef(task.Key)
	c := Continuation{Task: ref, Progress: unknown("the task's current revision records no progress"),
		Attempts: []AttemptView{}, Closure: closure(s, ref, request.Limit), Now: now, State: state,
		Handoff:   "none: this brief is generated on invocation and no handoff record was written",
		Generated: "disposable export; canonical state is the ledger at this answer's watermark"}
	c.Observed = unobserved("the caller supplied no workspace observation")
	if request.Observed != nil {
		c.Observed = *request.Observed
	}
	if task.Task.Progress != nil {
		c.Progress = *task.Task.Progress
	}
	id := reduce.Ident{Project: ref.Project, ID: ref.RecordID}
	for _, a := range s.Attempts(id) {
		c.Attempts = append(c.Attempts, attemptView(a))
	}
	c.Runs, _ = runs(s, func(inv reduce.Invocation) bool {
		return inv.Attempt.Project == ref.Project && inv.Attempt.Task == ref.RecordID
	})
	return c
}
