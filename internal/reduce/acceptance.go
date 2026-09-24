package reduce

// Who may accept a task and who did (R15.1) lives here: a task that names an
// accepter can be closed only by a packet that actor wrote, checked on Apply
// and Replay alike from the authors the bundle's review records; the closer
// is projected from the ledger, and so is whether the closer also did the
// work (self-accepted: visible, never blocking). Closure effect, witnesses and
// task status stay in task.go and task_events.go.

import (
	"fmt"

	"whosaidso/internal/model"
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

// selfAccepted compares the closer with the authors of the task's attempt
// receipts, the actors who reported the work done or stopped. Two unknowns
// never match, and a task with no receipt has no known doer.
func (s *state) selfAccepted(closer model.Actor, attempts []Attempt) Truth {
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
