package query

// The pieces of the continue view that are not selection: the caller's fresh
// workspace observation and the attempt view. The observation (HEAD, dirty,
// time) is supplied by the caller, because this package does not run git or
// read a clock, and an observation nobody made is UNKNOWN, never defaulted.
// The view itself is view_continue.go.

import (
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
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
