package reduce

// Who may accept a task and who did lives here: a task that names an
// accepter can be closed only by a packet that actor wrote, and its accepter
// removed or changed only by a packet that actor wrote, checked on Apply
// and Replay alike from the authors the bundle's review records; the closer
// is projected from the ledger, and so is whether the closer also wrote one
// of its attempt receipts (closer_authored_receipt: visible, never blocking). Closure effect, witnesses and
// task status stay in task.go and task_events.go.

import (
	"fmt"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// CodeAccepterMismatch is a task.close whose packet author is not the actor
// the task names as its accepter. Admission reports this same code because it
// reaches the check through Apply.
const CodeAccepterMismatch = "accepter-mismatch"

// checkAccepter refuses a closure of a task that names an accepter unless the
// packet carrying it was written by that actor. An unattributed packet or an
// unknown author never matches.
func (s *state) checkAccepter(b model.Bundle, idx int, rec Record) error {
	accepter := rec.Task.Accepter
	if accepter == nil {
		return nil
	}
	author, ok := s.bundle.packetAuthor(idx)
	if !ok || !model.SameActor(*accepter, author) {
		return faultAt(CodeAccepterMismatch, b.Sequence, idx, "task",
			fmt.Sprintf("the task names %s as its accepter; only a packet %s wrote can close it", accepter.ID, accepter.ID))
	}
	return nil
}

// checkAccepterChange refuses a task amendment that removes or replaces the
// accepter the amended revision names, unless the packet carrying it was
// written by that accepter. Otherwise whoever may amend could name someone
// else, or no one, and then close the task past the one actor it waits on.
// Naming an accepter where none was named, keeping the same one, and every
// other change stay open to any author; the accepter still judges the result.
func (s *state) checkAccepterChange(b model.Bundle, idx int, prior, next *model.TaskSpec) error {
	if prior == nil || prior.Accepter == nil {
		return nil
	}
	accepter := *prior.Accepter
	if next != nil && next.Accepter != nil && model.SameActor(accepter, *next.Accepter) {
		return nil
	}
	author, ok := s.bundle.packetAuthor(idx)
	if !ok || !model.SameActor(accepter, author) {
		return faultAt(CodeAccepterMismatch, b.Sequence, idx, "replacement.accepter",
			fmt.Sprintf("the task names %s as its accepter; only a packet %s wrote can remove or change it", accepter.ID, accepter.ID))
	}
	return nil
}

// closerAuthoredReceipt compares the closer with the authors of the task's
// attempt receipts, the holders who reported their attempt done or stopped.
// Two unknowns never match, and a task with no receipt has no known author.
func (s *state) closerAuthoredReceipt(closer model.Actor, attempts []Attempt) Truth {
	if !knownActor(closer) {
		return TruthUnknown
	}
	receipts, unknown := 0, false
	for _, a := range attempts {
		if a.Terminal == nil {
			continue
		}
		receipts++
		doer := s.eventAuthor(a.Terminal.Origin).Author
		if model.SameActor(doer, closer) {
			return TruthTrue
		}
		unknown = unknown || !knownActor(doer)
	}
	if receipts == 0 || unknown {
		return TruthUnknown
	}
	return TruthFalse
}

// acceptingActor is who an acceptance waits on: the named accepter, or else
// the actor the caller would otherwise wait on.
func acceptingActor(spec *model.TaskSpec, otherwise model.Actor) model.Actor {
	if spec.Accepter != nil {
		return *spec.Accepter
	}
	return otherwise
}
