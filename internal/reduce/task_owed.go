package reduce

// This file holds what a task still owes: witnessed closure, prerequisite
// truth, the blocked reasons and who they wait on. The projection that
// assembles these into a status lives in task.go.

import (
	"fmt"
	"sort"

	"whosaidso/internal/model"
)

// witnessed checks a success closure against the acceptance criteria of the
// revision it closed. A witness pinned to criterion revision 1 does not satisfy
// criterion revision 2: that is a stale witness, and accepting it would let an
// old sign-off certify a contract that has since changed.
func (s *state) witnessed(rec Record, c Closure) (missing []model.AcceptanceCriterion, ok bool) {
	for _, want := range rec.Task.AcceptanceCriteria {
		found := false
		for _, w := range c.AcceptanceWitnesses {
			if w.CriterionID == want.ID && w.CriterionRevision == want.Revision {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, want)
		}
	}
	// Delivery is a separate requirement from acceptance: a criterion can be
	// judged met while nothing was actually handed over.
	return missing, len(missing) == 0 && len(c.DeliveryWitnesses) > 0
}

// closedSuccess is the dependency rule, and it is deliberately not a call back
// into task(): a prerequisite chain can be cyclic, and this predicate needs no
// recursion. Nothing here reads status == CLOSED on its own, because cancelled,
// withdrawn and waived are closed too.
func (s *state) closedSuccess(target model.RecordRef) (Truth, string) {
	if target.Project != s.project {
		return TruthUnknown, crossProject(target)
	}
	rec, ok := s.records[recordKey(target)]
	if !ok {
		return TruthUnknown, fmt.Sprintf("no admitted revision %d of %s", target.Revision, target.RecordID)
	}
	if rec.Kind != model.Task {
		return TruthFalse, fmt.Sprintf("%s is a %s, not a TASK", target.RecordID, rec.Kind)
	}
	who := ident(target)
	c, closed := s.closed[who]
	if !closed {
		return TruthFalse, "the dependency is not closed"
	}
	for _, a := range s.attemptsFor(who) {
		if a.Live() {
			return TruthFalse, fmt.Sprintf("attempt %s has no terminal receipt", a.Key.Attempt)
		}
	}
	if c.Outcome != model.ClosureSuccess {
		return TruthFalse, fmt.Sprintf("closed %s, which is not success", c.Outcome)
	}
	// The producer must be CLOSED at its current revision; a historical
	// witness alone does not establish that its current obligation is met.
	current := s.records[RecordKey{Project: who.Project, ID: who.ID, Revision: s.current[who]}]
	if _, ok := s.witnessed(current, c); !ok {
		return TruthFalse, "the dependency's current revision is not witnessed closed"
	}
	// The witness must apply to the revision the consumer required, not merely
	// to whatever revision happened to be closed. rec is that exact revision.
	if missing, ok := s.witnessed(rec, c); !ok {
		if len(missing) > 0 {
			return TruthFalse, fmt.Sprintf("closure does not witness acceptance criterion %s revision %d",
				missing[0].ID, missing[0].Revision)
		}
		return TruthFalse, "closure carries no delivery witness"
	}
	return TruthTrue, fmt.Sprintf("task %s revision %d closed as success with its acceptance and delivery witnessed", target.RecordID, target.Revision)
}

func (s *state) prerequisites(rec Record) []PrerequisiteResult {
	out := make([]PrerequisiteResult, 0, len(rec.Task.Prerequisites))
	for i, r := range rec.Task.Prerequisites {
		res := PrerequisiteResult{Index: i, Kind: r.Kind, Target: r.Target}
		switch r.Kind {
		case "task-success":
			res.Truth, res.Detail = s.closedSuccess(r.Target)
		case "claim-proof":
			res.Truth, res.Detail = s.claimProof(r.Target)
		case "decision-approved":
			res.Truth, res.Detail = s.decisionApproved(r.Target)
		default:
			// The model refuses any other kind at decode; an unrecognised one
			// is UNKNOWN, never TRUE, so it blocks rather than dispatches.
			res.Truth = TruthUnknown
			res.Detail = "unrecognised prerequisite kind " + r.Kind
		}
		// A waiver counts only where the consumer explicitly permits one AND
		// cites authority. A "forbid" policy is not overridable by anyone, and
		// the waiver stays visible rather than being folded into TRUE.
		if res.Truth != TruthTrue && r.WaiverPolicy == "allow-with-authority" && r.Authority != nil {
			res.Waived = true
			res.Waiver = r.Authority
		}
		out = append(out, res)
	}
	return out
}

// owed collects every structured reason the task cannot be dispatched. An empty
// prerequisite set is vacuously TRUE, so a task with no prerequisites and
// nothing else owed reaches READY.
func (s *state) owed(id Ident, rec Record, closure Closure, closed bool, prereqs []PrerequisiteResult, attempts []Attempt) []BlockedReason {
	reasons := []BlockedReason{}

	// Prerequisite reasons are listed in prerequisite index order (numeric),
	// never by their detail text, where prerequisite 10 would sort before 2.
	ordered := append([]PrerequisiteResult{}, prereqs...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Index < ordered[j].Index })
	for _, r := range ordered {
		if r.Satisfied() {
			continue
		}
		target := r.Target
		reasons = append(reasons, BlockedReason{
			Kind:   ReasonPrerequisite,
			Detail: fmt.Sprintf("prerequisite %d (%s) is %s: %s", r.Index, r.Kind, r.Truth, r.Detail),
			Actor:  s.dependencyActor(target),
			Target: &target,
			Truth:  r.Truth,
			Origin: rec.Origin,
		})
	}

	// An open hold is owed by definition, whatever its typed reason.
	for _, b := range s.openHolds(id) {
		reasons = append(reasons, BlockedReason{
			Kind:      string(b.Reason),
			Detail:    b.Criterion,
			Actor:     b.Actor,
			BlockerID: b.Key.Blocker,
			Truth:     TruthFalse,
			Origin:    b.Held,
		})
	}

	// A successful attempt closes the ATTEMPT, not the obligation. Without an
	// authorised closure the task is awaiting acceptance, and this is the typed
	// queue that exists so it never reads as done.
	if !closed {
		for _, a := range attempts {
			if a.Terminal != nil && a.Terminal.Outcome == model.AttemptSuccess {
				reasons = append(reasons, BlockedReason{
					Kind:   ReasonAwaitingAcceptance,
					Detail: fmt.Sprintf("attempt %s succeeded with no authorised closure", a.Key.Attempt),
					Actor:  acceptingActor(rec.Task, rec.Task.NextActor),
					Truth:  TruthFalse,
					Origin: a.Terminal.Origin,
				})
				break
			}
		}
	} else if closure.Outcome == model.ClosureSuccess {
		// Reached only when predicate 1 fell through on witnesses.
		if missing, ok := s.witnessed(rec, closure); !ok {
			detail := "the success closure carries no delivery witness"
			if len(missing) > 0 {
				detail = fmt.Sprintf("the success closure does not witness acceptance criterion %s revision %d",
					missing[0].ID, missing[0].Revision)
			}
			closer := s.eventAuthor(closure.Origin).Author
			if closure.Authority != nil {
				closer = closure.Authority.Actor
			}
			reasons = append(reasons, BlockedReason{
				Kind:   ReasonAwaitingAcceptance,
				Detail: detail,
				Actor:  acceptingActor(rec.Task, closer),
				Truth:  TruthFalse,
				Origin: closure.Origin,
			})
		}
	}

	// A terminal receipt that declares reconciliation owed keeps the task
	// blocked until an admitted reconciliation clear discharges it. The facet
	// is the writer saying the observed outcome is not settled, and only an
	// admitted event may settle it.
	for _, a := range attempts {
		if a.Terminal == nil || !a.Terminal.ReconciliationOwed {
			continue
		}
		if s.reconciled(id, a.Terminal.Origin) {
			continue
		}
		reasons = append(reasons, BlockedReason{
			Kind:   ReasonReconciliation,
			Detail: fmt.Sprintf("attempt %s terminated %s with reconciliation owed", a.Key.Attempt, a.Terminal.Outcome),
			Actor:  a.Actor,
			Truth:  TruthUnknown,
			Origin: a.Terminal.Origin,
		})
	}

	sort.SliceStable(reasons, func(i, j int) bool {
		if reasons[i].Kind != reasons[j].Kind {
			return reasons[i].Kind < reasons[j].Kind
		}
		if reasons[i].Origin != reasons[j].Origin {
			return reasons[i].Origin.before(reasons[j].Origin)
		}
		if reasons[i].Kind == ReasonPrerequisite {
			return false // stable: keep prerequisite index order from above
		}
		return reasons[i].Detail < reasons[j].Detail
	})
	return reasons
}

// openHolds is the one open-hold evaluation: the task's holds with no admitted
// clear, in ledger order. owed lists them as BLOCKED reasons and a success
// closure is refused while any remain.
func (s *state) openHolds(id Ident) []Blocker {
	out := []Blocker{}
	for _, b := range s.blockersFor(id) {
		if b.Open() {
			out = append(out, b)
		}
	}
	return out
}

// reconciled reports whether a reconciliation hold was cleared after the given
// terminal receipt. Anything earlier settled a different question.
func (s *state) reconciled(id Ident, after Origin) bool {
	for _, b := range s.blockersFor(id) {
		if b.Reason != model.BlockerReconciliation || b.Cleared == nil {
			continue
		}
		if after.before(*b.Cleared) {
			return true
		}
	}
	return false
}

// dependencyActor names whoever must act on an unmet dependency. When the
// dependency is not resolvable here the actor is explicitly unknown rather than
// defaulted to the consuming task's own next actor, which would point the queue
// at the wrong person.
func (s *state) dependencyActor(target model.RecordRef) model.Actor {
	if target.Project != s.project {
		return model.Actor{UnknownReason: crossProject(target)}
	}
	rev, ok := s.current[ident(target)]
	if !ok {
		return model.Actor{UnknownReason: "no admitted revision of the dependency"}
	}
	rec, ok := s.records[RecordKey{Project: target.Project, ID: target.RecordID, Revision: rev}]
	if !ok || rec.Kind != model.Task {
		return model.Actor{UnknownReason: "the dependency names no next actor"}
	}
	return rec.Task.NextActor
}

func waitingActors(reasons []BlockedReason) []model.Actor {
	seen := map[model.Actor]bool{}
	out := []model.Actor{}
	for _, r := range reasons {
		if r.Actor == (model.Actor{}) || seen[r.Actor] {
			continue
		}
		seen[r.Actor] = true
		out = append(out, r.Actor)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].UnknownReason < out[j].UnknownReason
	})
	return out
}
