package reduce

// Task prerequisites, attempts, closures, and blocker transitions live here.
// Derived task status and acceptance answers remain in task.go.
// This complete lifecycle group is kept together, a little over 200 lines.

import (
	"fmt"
	"strings"

	"whosaidso/internal/model"
)

// checkPrerequisites refuses a typed prerequisite pointed at the wrong kind of
// record. A task-success prerequisite naming a CLAIM would evaluate to UNKNOWN
// forever, which reads as "waiting" when it is really "malformed".
func (s *state) checkPrerequisites(b model.Bundle, idx int, spec model.TaskSpec) error {
	for i, r := range spec.Prerequisites {
		if r.Target.Project != b.Project {
			continue // resolves on read
		}
		rec, ok := s.records[recordKey(r.Target)]
		if !ok {
			continue // the reference check already refused, or it is cross-project
		}
		var want model.Kind
		switch r.Kind {
		case "task-success":
			want = model.Task
		case "claim-proof":
			want = model.Claim
		case "decision-approved":
			want = model.Decision
		}
		if rec.Kind != want {
			return faultAt(CodeInvalidField, b.Sequence, idx,
				fmt.Sprintf("prerequisites[%d].target", i),
				fmt.Sprintf("%s prerequisite must name a %s, not a %s", r.Kind, want, rec.Kind))
		}
	}
	return nil
}

func (s *state) taskAt(b model.Bundle, idx int, ref model.RecordRef, path string) (Record, error) {
	rec, ok := s.records[recordKey(ref)]
	if !ok {
		return Record{}, faultAt(CodeUnknownReference, b.Sequence, idx, path,
			fmt.Sprintf("no admitted revision %d of %s", ref.Revision, ref.RecordID))
	}
	if rec.Kind != model.Task {
		return Record{}, faultAt(CodeInvalidTransition, b.Sequence, idx, path,
			fmt.Sprintf("%s is a %s", ref.RecordID, rec.Kind))
	}
	if _, closed := s.closed[ident(ref)]; closed {
		return Record{}, faultAt(CodeInvalidTransition, b.Sequence, idx, path, "the task is closed")
	}
	return rec, nil
}

func (s *state) start(b model.Bundle, idx int, o Origin, e *model.TaskStart) error {
	if _, err := s.taskAt(b, idx, e.Task, "task"); err != nil {
		return err
	}
	key := AttemptKey{Project: b.Project, Task: e.Task.RecordID, Attempt: e.AttemptID}
	if _, ok := s.attemptOwner[Ident{Project: b.Project, ID: e.AttemptID}]; ok {
		return faultAt(CodeDuplicateRecord, b.Sequence, idx, "attempt_id", "attempt already admitted")
	}
	// A second start while an attempt is live would give one task two owners
	// with no record that the first was displaced. That is what takeover is,
	// and takeover carries the confirmation that the prior writer stopped.
	for _, a := range s.attemptsFor(ident(e.Task)) {
		if a.Live() {
			return faultAt(CodeInvalidTransition, b.Sequence, idx, "attempt_id",
				fmt.Sprintf("attempt %s is still live; use takeover", a.Key.Attempt))
		}
	}
	if err := s.requireReady(b, idx, e.Task); err != nil {
		return err
	}
	s.attempts[key] = Attempt{
		Key:          key,
		TaskRevision: e.Task.Revision,
		Actor:        e.Actor,
		Started:      o,
	}
	s.attemptOwner[Ident{Project: b.Project, ID: e.AttemptID}] = key
	return nil
}

func (s *state) takeover(b model.Bundle, idx int, o Origin, e *model.TaskTakeover) error {
	if _, err := s.taskAt(b, idx, e.Task, "task"); err != nil {
		return err
	}
	prior := AttemptKey{Project: b.Project, Task: e.Task.RecordID, Attempt: e.PriorAttemptID}
	if _, ok := s.attempts[prior]; !ok {
		return faultAt(CodeUnknownReference, b.Sequence, idx, "prior_attempt_id",
			"no admitted prior attempt under this task")
	}
	key := AttemptKey{Project: b.Project, Task: e.Task.RecordID, Attempt: e.AttemptID}
	if _, ok := s.attemptOwner[Ident{Project: b.Project, ID: e.AttemptID}]; ok {
		return faultAt(CodeDuplicateRecord, b.Sequence, idx, "attempt_id", "attempt already admitted")
	}
	// The new holder takes the attempt over in its own packet: nobody is made
	// a holder by someone else's packet.
	if err := s.checkPacketAuthor(b, idx, e.Actor, "actor"); err != nil {
		return err
	}
	// Taking over a live attempt displaces its writer while the task is IN
	// FLIGHT, where READY and BLOCKED do not apply. Once the prior attempt is
	// terminal nobody is displaced: the takeover dispatches new work and meets
	// the same READY rule as start, so nothing owed can be skipped by the verb.
	if !s.attempts[prior].Live() {
		if err := s.requireReady(b, idx, e.Task); err != nil {
			return err
		}
	}
	// The prior attempt is left exactly as it was. A takeover does not write a
	// terminal receipt on someone else's behalf, so an abandoned attempt stays
	// visibly live and the task stays IN FLIGHT until its own holder answers.
	s.attempts[key] = Attempt{
		Key:          key,
		TaskRevision: e.Task.Revision,
		Actor:        e.Actor,
		Takeover:     true,
		PriorAttempt: e.PriorAttemptID,
		Started:      o,
	}
	s.attemptOwner[Ident{Project: b.Project, ID: e.AttemptID}] = key
	return nil
}

// requireReady is the dispatch rule: new work starts only on a READY task.
// BLOCKED wins over READY, so an owed prerequisite, hold, acceptance or
// reconciliation refuses here, where replay enforces it too, not only at one
// admission entrance.
func (s *state) requireReady(b model.Bundle, idx int, task model.RecordRef) error {
	if p, _ := s.taskRevision(recordKey(task)); p.Status != StatusReady {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, "task",
			fmt.Sprintf("task is %s and cannot start until it is READY", p.Status))
	}
	return nil
}

func (s *state) terminal(b model.Bundle, idx int, o Origin, e *model.AttemptTerminal) error {
	key := AttemptKey{Project: b.Project, Task: e.Task.RecordID, Attempt: e.AttemptID}
	a, ok := s.attempts[key]
	if !ok {
		return faultAt(CodeUnknownReference, b.Sequence, idx, "attempt_id",
			"no admitted attempt under this task")
	}
	if a.Terminal != nil {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, "attempt_id",
			"the attempt already has a terminal receipt")
	}
	if err := s.checkReceiptAuthor(b, idx, a.Actor); err != nil {
		return err
	}
	if err := s.checkHandbackHold(b, idx, e); err != nil {
		return err
	}
	a.Terminal = &Terminal{
		Outcome:            e.Outcome,
		Reason:             e.Reason,
		NextAction:         e.NextAction,
		DeliveryRefs:       e.DeliveryRefs,
		CommitsDenied:      e.CommitsDenied,
		ReconciliationOwed: e.ReconciliationOwed,
		Origin:             o,
	}
	s.attempts[key] = a
	return nil
}

func (s *state) close(b model.Bundle, idx int, o Origin, e *model.TaskClose) error {
	rec, err := s.taskAt(b, idx, e.Task, "task")
	if err != nil {
		return err
	}
	if err := s.checkAccepter(b, idx, rec); err != nil {
		return err
	}
	// Whether every attempt is terminal, and whether a success closure has
	// applicable witnesses, are PROJECTION questions, evaluated in order in
	// task.go. They are not refused here: an authorised closure is a fact, and
	// dropping it would hide that someone closed a task with a writer still in
	// it. The admission gate refuses, the reducer records and then projects.
	// Unmet prerequisites and open holds on a success closure are the
	// exceptions, refused below.
	who := ident(e.Task)
	if _, ok := s.closed[who]; ok {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, "task", "the task is already closed")
	}
	if err := s.requirePrerequisites(b, idx, rec, e.Outcome); err != nil {
		return err
	}
	if err := s.requireHoldsCleared(b, idx, who, e.Outcome); err != nil {
		return err
	}
	s.closed[who] = Closure{
		Task:                e.Task,
		Outcome:             e.Outcome,
		Authority:           e.Authority,
		AcceptanceWitnesses: e.AcceptanceWitnessRefs,
		DeliveryWitnesses:   e.DeliveryWitnessRefs,
		Origin:              o,
	}
	return nil
}

// requirePrerequisites is the success-closure rule: a task closes as success
// only if every prerequisite of the revision it closes is satisfied (TRUE, or
// carried by a permitted waiver) at this ledger position. Unlike witnesses and
// live attempts this is refused, not projected: a success closure over an
// unfinished prerequisite certifies work that could not yet have been done. It
// reuses the READY/BLOCKED evaluation, which never recurses into another task,
// so a prerequisite cycle refuses instead of looping. Replay and Apply both
// reach it through apply. Non-success outcomes keep their own rules.
func (s *state) requirePrerequisites(b model.Bundle, idx int, rec Record, outcome model.ClosureOutcome) error {
	if outcome != model.ClosureSuccess {
		return nil
	}
	for _, r := range s.prerequisites(rec) {
		if !r.Satisfied() {
			return faultAt(CodeInvalidTransition, b.Sequence, idx, "outcome",
				fmt.Sprintf("a success closure needs every prerequisite satisfied; prerequisite %d (%s) is %s: %s",
					r.Index, r.Kind, r.Truth, r.Detail))
		}
	}
	return nil
}

// requireHoldsCleared extends the success-closure rule to holds: a task closes
// as success only if none of its holds is open at this ledger position. A hold
// cleared earlier, including by a blocker.clear earlier in the same bundle, no
// longer counts. It reads the same open-hold set that makes the task BLOCKED
// (openHolds), and like requirePrerequisites it is reached by Apply and Replay
// alike. Cancelled, withdrawn and waived closures may still abandon held work.
func (s *state) requireHoldsCleared(b model.Bundle, idx int, who Ident, outcome model.ClosureOutcome) error {
	if outcome != model.ClosureSuccess {
		return nil
	}
	open := s.openHolds(who)
	if len(open) == 0 {
		return nil
	}
	named := make([]string, 0, len(open))
	for _, h := range open {
		waits := h.Actor.ID
		if waits == "" {
			waits = "UNKNOWN: " + h.Actor.UnknownReason
		}
		named = append(named, fmt.Sprintf("hold %s (%s) waits on %s: %s", h.Key.Blocker, h.Reason, waits, h.Criterion))
	}
	return faultAt(CodeInvalidTransition, b.Sequence, idx, "outcome",
		"a success closure needs every hold cleared first (blocker.clear); open: "+strings.Join(named, "; "))
}

func (s *state) hold(b model.Bundle, idx int, o Origin, e *model.BlockerHold) error {
	if _, err := s.taskAt(b, idx, e.Task, "task"); err != nil {
		return err
	}
	key := BlockerKey{Project: b.Project, Task: e.Task.RecordID, Blocker: e.BlockerID}
	// Reuse is refused even after the hold was cleared. Rebinding a blocker id
	// would rewrite what an already-admitted clear resolved.
	if _, ok := s.blockers[key]; ok {
		return faultAt(CodeDuplicateRecord, b.Sequence, idx, "blocker_id", "blocker id already used on this task")
	}
	s.blockers[key] = Blocker{
		Key:          key,
		TaskRevision: e.Task.Revision,
		Reason:       e.Reason,
		Actor:        e.Actor,
		Criterion:    e.Criterion,
		Held:         o,
	}
	return nil
}

func (s *state) clear(b model.Bundle, idx int, o Origin, e *model.BlockerClear) error {
	key := BlockerKey{Project: b.Project, Task: e.Task.RecordID, Blocker: e.BlockerID}
	held, ok := s.blockers[key]
	if !ok {
		return faultAt(CodeUnknownReference, b.Sequence, idx, "blocker_id", "no admitted hold with this id")
	}
	if held.Cleared != nil {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, "blocker_id", "the hold is already cleared")
	}
	witness := e.ResolvingWitness
	held.Cleared = &o
	held.Witness = &witness
	s.blockers[key] = held
	return nil
}
