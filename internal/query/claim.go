package query

// Whether the holder of one attempt may keep working: the answer an agent
// asks for after every sync (whosaidso check claim). A claim is a task.start
// or task.takeover; two made on separate branches merge into one canonical
// order, the first applies and the other is set aside (reduce/setaside.go),
// so the loser learns it lost only by reading the merged ledger. This file
// decides the claim's status from one ledger prefix; the CLI only prints it.

import (
	"fmt"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

// Claim statuses. Only ClaimHeld lets the holder keep working.
const (
	ClaimHeld      = "HELD"
	ClaimLost      = "LOST"
	ClaimTakenOver = "TAKEN-OVER"
	ClaimEnded     = "ENDED"
	ClaimUnknown   = "UNKNOWN"
)

// ClaimAnswer is one attempt's status at one watermark. Task and Holder are
// set whenever the claim was found; Current names the attempt that holds the
// task instead, for a lost or taken-over claim, when one does.
type ClaimAnswer struct {
	Project   model.ProjectID  `json:"project"`
	Watermark Watermark        `json:"watermark"`
	Attempt   model.ID         `json:"attempt_id"`
	Status    string           `json:"status"`
	Task      *model.RecordRef `json:"task,omitempty"`
	Holder    *model.Actor     `json:"holder,omitempty"`
	Current   *ClaimHolder     `json:"current,omitempty"`
	Reason    string           `json:"reason"`
	Next      string           `json:"next"`
}

// ClaimHolder is the attempt that holds a task now.
type ClaimHolder struct {
	Attempt model.ID    `json:"attempt_id"`
	Actor   model.Actor `json:"actor"`
}

// ReadClaim answers from the selected ledger prefix.
func ReadClaim(project store.Project, attempt model.ID) (*ClaimAnswer, error) {
	if !model.ValidID(attempt) {
		return nil, fmt.Errorf("%q is not an attempt ULID", attempt)
	}
	source, err := store.Load(project)
	if err != nil {
		return nil, err
	}
	return claimFrom(project, source, attempt)
}

func claimFrom(project store.Project, source Source, attempt model.ID) (*ClaimAnswer, error) {
	s := source.Snapshot()
	a := &ClaimAnswer{Project: project.ID, Watermark: watermarkOf(s), Attempt: attempt}
	for _, r := range currentRecords(s) {
		if r.Kind != model.Task {
			continue
		}
		task := reduce.Ident{Project: r.Key.Project, ID: r.Key.ID}
		for _, at := range s.Attempts(task) {
			if at.Key.Attempt == attempt {
				appliedClaim(a, s, task, at)
				return a, nil
			}
		}
	}
	lost, err := setAsideClaim(s, source, attempt)
	if err != nil {
		return nil, err
	}
	if lost == nil {
		a.Status = ClaimUnknown
		a.Reason = "no claim with this attempt id in this checkout's ledger"
		a.Next = "sync first (git fetch origin && git merge --no-edit origin/main), then ask again; if it is still unknown, the claim was never admitted here"
		return a, nil
	}
	a.Status, a.Task, a.Holder = ClaimLost, &lost.task, &lost.actor
	a.Current = currentHolder(s, reduce.Ident{Project: lost.task.Project, ID: lost.task.RecordID})
	a.Reason = fmt.Sprintf("the bundle that claimed it (sequence %d, command %s) was set aside after a merge: %s", lost.bundle.Sequence, lost.bundle.CommandID, lost.fault)
	a.Next = "stop work on this attempt and hand nothing back on it; " + afterLoss(a.Current)
	return a, nil
}

// appliedClaim decides the status of an attempt replay applied.
func appliedClaim(a *ClaimAnswer, s reduce.Snapshot, task reduce.Ident, at reduce.Attempt) {
	ref := model.RecordRef{Project: task.Project, RecordID: task.ID, Revision: at.TaskRevision}
	actor := at.Actor
	a.Task, a.Holder = &ref, &actor
	switch {
	case at.Terminal != nil:
		a.Status = ClaimEnded
		a.Reason = fmt.Sprintf("its receipt is admitted (outcome %s, sequence %d)", at.Terminal.Outcome, at.Terminal.Origin.Sequence)
		a.Next = "nothing more to record on this attempt; start a new claim to work on the task again"
		return
	}
	for _, other := range s.Attempts(task) {
		if other.Takeover && other.PriorAttempt == at.Key.Attempt {
			a.Status = ClaimTakenOver
			a.Current = &ClaimHolder{Attempt: other.Key.Attempt, Actor: other.Actor}
			a.Reason = fmt.Sprintf("attempt %s took the task over at sequence %d", other.Key.Attempt, other.Started.Sequence)
			a.Next = "stop work on this attempt and hand nothing back on it; " + afterLoss(a.Current)
			return
		}
	}
	if _, closed := s.Closure(task); closed {
		a.Status = ClaimEnded
		a.Reason = "the task is closed"
		a.Next = "stop: there is nothing left to claim"
		return
	}
	a.Status = ClaimHeld
	a.Reason = fmt.Sprintf("the claim applied at sequence %d and nothing ended or replaced it", at.Started.Sequence)
	a.Next = "keep working; ask again after every sync"
}

// afterLoss is what a holder who lost does next.
func afterLoss(current *ClaimHolder) string {
	if current == nil {
		return "no attempt holds the task now: claim it again from a current main if it is still yours to do"
	}
	return fmt.Sprintf("attempt %s by %s holds the task; pick other work", current.Attempt, actorName(current.Actor))
}

func actorName(a model.Actor) string {
	if a.ID == "" {
		return "an unknown actor"
	}
	return a.ID
}

// holding lists a task's attempts that hold it: live, and replaced by no
// takeover. A replaced attempt stays live (it has no receipt) by design.
func holding(s reduce.Snapshot, task reduce.Ident) []reduce.Attempt {
	attempts := s.Attempts(task)
	replaced := map[model.ID]bool{}
	for _, at := range attempts {
		if at.Takeover {
			replaced[at.PriorAttempt] = true
		}
	}
	out := []reduce.Attempt{}
	for _, at := range attempts {
		if at.Live() && !replaced[at.Key.Attempt] {
			out = append(out, at)
		}
	}
	return out
}

// currentHolder is the task's first attempt that holds it.
func currentHolder(s reduce.Snapshot, task reduce.Ident) *ClaimHolder {
	h := holding(s, task)
	if len(h) == 0 {
		return nil
	}
	return &ClaimHolder{Attempt: h[0].Key.Attempt, Actor: h[0].Actor}
}

// lostClaim is a claim carried by a set-aside bundle.
type lostClaim struct {
	bundle model.Bundle
	task   model.RecordRef
	actor  model.Actor
	fault  string
}

// setAsideClaim finds the claim of attempt in a set-aside bundle. Bundles are
// read only when replay set one aside, so a ledger that never clashed pays
// nothing for the question.
func setAsideClaim(s reduce.Snapshot, source Source, attempt model.ID) (*lostClaim, error) {
	if len(s.SetAside()) == 0 {
		return nil, nil
	}
	bundles, err := source.Bundles()
	if err != nil {
		return nil, err
	}
	for _, b := range bundles {
		aside, ok := s.SetAsideBundle(b.Ref())
		if !ok {
			continue
		}
		for _, c := range claimsIn(b) {
			if c.attempt == attempt {
				return &lostClaim{bundle: b, task: c.task, actor: c.actor, fault: aside.Code + ": " + aside.Reason}, nil
			}
		}
	}
	return nil, nil
}

// claimEvent is one task.start or task.takeover a bundle carries.
type claimEvent struct {
	attempt model.ID
	task    model.RecordRef
	actor   model.Actor
}

// claimsIn lists the claims in one bundle. An event that does not decode is
// no claim: replay already reported it.
func claimsIn(b model.Bundle) []claimEvent {
	out := []claimEvent{}
	for _, raw := range b.Events {
		if raw.Type != "task.start" && raw.Type != "task.takeover" {
			continue
		}
		e, err := model.DecodeEvent(raw)
		if err != nil {
			continue
		}
		switch v := e.(type) {
		case *model.TaskStart:
			out = append(out, claimEvent{attempt: v.AttemptID, task: v.Task, actor: v.Actor})
		case *model.TaskTakeover:
			out = append(out, claimEvent{attempt: v.AttemptID, task: v.Task, actor: v.Actor})
		}
	}
	return out
}
