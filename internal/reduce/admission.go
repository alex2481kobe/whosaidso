package reduce

// Event ownership, revision/reference checks, reverse links, and refusal types live here.
// Bundle sequencing and event-specific state transitions do not.

import (
	"fmt"

	"whosaidso/internal/model"
)

// Fault codes. The code and the location are the invariant. The diagnostic
// text is not, so tests assert on codes.
const (
	// CodeLedgerDiscontinuity is a broken sequence or predecessor chain.
	CodeLedgerDiscontinuity = "ledger-discontinuity"
	// CodeProjectMismatch is a bundle from another project in this ledger.
	CodeProjectMismatch = "project-mismatch"
	// CodeDuplicateCommand is a second bundle under an admitted transaction id.
	CodeDuplicateCommand = "duplicate-command"
	// CodeUnknownVersion is an envelope this build cannot account for.
	CodeUnknownVersion = "unknown-version"
	// CodeInvalidField is a structurally impossible event in context.
	CodeInvalidField = "invalid-field"
	// CodeUnknownReference names something no admitted bundle established.
	CodeUnknownReference = "unknown-reference"
	// CodeDuplicateRecord mints an identity that already exists.
	CodeDuplicateRecord = "duplicate-record"
	// CodeInvalidTransition is a legal event applied to a state that forbids it.
	CodeInvalidTransition = "invalid-transition"
	// CodeRevisionConflict is a write built on a stale view. See Conflict.
	CodeRevisionConflict = "revision-conflict"
)

// Conflict is the typed refusal for a write whose expected revision no longer
// matches. It carries the bundle sequence and the event index because "someone
// amended this first" is only actionable if you can say which write lost and
// exactly where. Which one loses is decided by ledger sequence, never by the
// order a directory happened to enumerate.
type Conflict struct {
	Target     model.RecordRef `json:"target"`
	Expected   model.Revision  `json:"expected"`
	Actual     model.Revision  `json:"actual"`
	Sequence   uint64          `json:"sequence"`
	EventIndex int             `json:"event_index"`
	Path       string          `json:"path"`
}

// Code lets a caller branch on the same vocabulary model.Fault uses.
func (c *Conflict) Code() string { return CodeRevisionConflict }

func (c *Conflict) Error() string {
	return fmt.Sprintf("%s at %s in event %d (sequence %d): %s expected revision %d, admitted revision is %d",
		CodeRevisionConflict, c.Path, c.EventIndex, c.Sequence, c.Target.RecordID, c.Expected, c.Actual)
}

func faultAt(code string, seq uint64, idx int, path, detail string) *model.Fault {
	return &model.Fault{Code: code, Sequence: seq, EventIndex: idx, Path: path, Detail: detail}
}

// checkSubject refuses a bundle that authors or mutates another project's
// record. Referring across projects is by design. Writing across them is not,
// and one ledger holding a foreign record makes both projects wrong.
func (s *state) checkSubject(b model.Bundle, idx int, e model.TypedEvent) error {
	var project model.ProjectID
	var path string
	switch t := e.(type) {
	case *model.TaskAmend:
		project, path = t.Target.Project, "target.project"
	case *model.ClaimRevise:
		project, path = t.Target.Project, "target.project"
	case *model.DecisionRevise:
		project, path = t.Target.Project, "target.project"
	case *model.InstrumentRevise:
		project, path = t.Target.Project, "target.project"
	case *model.TaskStart:
		project, path = t.Task.Project, "task.project"
	case *model.TaskTakeover:
		project, path = t.Task.Project, "task.project"
	case *model.AttemptTerminal:
		project, path = t.Task.Project, "task.project"
	case *model.TaskClose:
		project, path = t.Task.Project, "task.project"
	case *model.BlockerHold:
		project, path = t.Task.Project, "task.project"
	case *model.BlockerClear:
		project, path = t.Task.Project, "task.project"
	case *model.CriterionFix:
		project, path = t.Claim.Project, "claim.project"
	case *model.ProofAdmit:
		project, path = t.Claim.Project, "claim.project"
	case *model.DecisionDispose:
		project, path = t.Decision.Project, "decision.project"
	case *model.Supersede:
		project, path = t.Prior.Project, "prior.project"
	case *model.TrustWithdraw:
		project, path = t.Instrument.Project, "instrument.project"
	case *model.InvocationStart:
		project, path = t.Envelope.ExecutionSourceIdentity.Project, "envelope.execution_source_identity.project"
	case *model.InvocationSeal:
		project, path = t.Envelope.ExecutionSourceIdentity.Project, "envelope.execution_source_identity.project"
	default:
		return nil
	}
	if project != b.Project {
		return faultAt(CodeInvalidField, b.Sequence, idx, path,
			fmt.Sprintf("a bundle in %q cannot author into %q", b.Project, project))
	}
	return nil
}

// checkExpectations runs before reference checking so a stale write gets the
// conflict that names the winner, not a generic missing-reference complaint.
// An expected revision that does not exist at all falls through to the
// reference check, where "no such revision" is the honest answer.
func (s *state) checkExpectations(b model.Bundle, idx int, e model.TypedEvent) error {
	var target model.RecordRef
	var expected model.Revision
	var path string
	switch t := e.(type) {
	// A replacement names the revision it replaces as its target.
	case *model.TaskAmend:
		target, expected, path = t.Target, t.Target.Revision, "target"
	case *model.ClaimRevise:
		target, expected, path = t.Target, t.Target.Revision, "target"
	case *model.DecisionRevise:
		target, expected, path = t.Target, t.Target.Revision, "target"
	case *model.InstrumentRevise:
		target, expected, path = t.Target, t.Target.Revision, "target"

	// These name the task revision the writer acted on: acting on a
	// superseded contract is exactly the stale write the conflict exists to stop.
	case *model.TaskStart:
		target, expected, path = t.Task, t.Task.Revision, "task"
	case *model.TaskTakeover:
		target, expected, path = t.Task, t.Task.Revision, "task"
	case *model.AttemptTerminal:
		target, expected, path = t.Task, t.Task.Revision, "task"
	case *model.TaskClose:
		target, expected, path = t.Task, t.Task.Revision, "task"
	case *model.BlockerHold:
		target, expected, path = t.Task, t.Task.Revision, "task"
	case *model.BlockerClear:
		target, expected, path = t.Task, t.Task.Revision, "task"
	default:
		return nil
	}
	actual, ok := s.current[ident(target)]
	if !ok {
		return nil
	}
	if actual != expected {
		return &Conflict{
			Target:     target,
			Expected:   expected,
			Actual:     actual,
			Sequence:   b.Sequence,
			EventIndex: idx,
			Path:       path,
		}
	}
	return nil
}

// checkReferences resolves every same-project reference the model walker finds
// and returns them, schema-validated, for recordReferrers: the walk and its
// schema validation run once per admitted event.
// Cross-project links are exempt by design: they resolve on read and may dangle,
// and reporting an unresolved one is not the same as treating it as absent.
func (s *state) checkReferences(b model.Bundle, idx int, e model.TypedEvent) ([]model.Reference, error) {
	refs, err := model.SameProjectReferences(e, b.Project)
	if err != nil {
		if f, ok := err.(*model.Fault); ok {
			return nil, faultAt(f.Code, b.Sequence, idx, f.Path, f.Detail)
		}
		return nil, faultAt(CodeInvalidField, b.Sequence, idx, "event", err.Error())
	}
	for _, r := range refs {
		switch {
		case r.Record != nil:
			if _, ok := s.records[recordKey(*r.Record)]; !ok {
				return nil, faultAt(CodeUnknownReference, b.Sequence, idx, r.Path,
					fmt.Sprintf("no admitted revision %d of %s", r.Record.Revision, r.Record.RecordID))
			}
		case r.Criterion != nil:
			if _, ok := s.criteria[criterionKey(*r.Criterion)]; !ok {
				return nil, faultAt(CodeUnknownReference, b.Sequence, idx, r.Path,
					fmt.Sprintf("no admitted criterion %s revision %d", r.Criterion.CriterionID, r.Criterion.Revision))
			}
		case r.Invocation != nil:
			if _, ok := s.invocations[invocationKey(*r.Invocation)]; !ok {
				// R10.3: a proof may name a run a non-accepted review recorded;
				// the proof family checker decides whether and how it belongs.
				if _, proof := e.(*model.ProofAdmit); proof && s.rejectedRecorded(invocationKey(*r.Invocation)) {
					continue
				}
				return nil, faultAt(CodeUnknownReference, b.Sequence, idx, r.Path,
					fmt.Sprintf("no admitted invocation %s", r.Invocation.InvocationID))
			}
		case r.Blocker != nil:
			if _, ok := s.blockers[blockerKey(*r.Blocker)]; !ok {
				return nil, faultAt(CodeUnknownReference, b.Sequence, idx, r.Path,
					fmt.Sprintf("no admitted blocker %s", r.Blocker.BlockerID))
			}
		}
	}
	return refs, nil
}

// recordReferrers indexes the references checkReferences returned for this
// same event; it never walks the event a second time.
func (s *state) recordReferrers(b model.Bundle, idx int, e model.TypedEvent, refs []model.Reference) {
	r := Referrer{Origin: Origin{Sequence: b.Sequence, EventIndex: idx}, Type: e.EventType()}
	for _, ref := range refs {
		r.Path = ref.Path
		switch {
		case ref.Record != nil:
			addReferrer(s.reverseRecord, recordKey(*ref.Record), r)
		case ref.Criterion != nil:
			addReferrer(s.reverseCriterion, criterionKey(*ref.Criterion), r)
		case ref.Invocation != nil:
			addReferrer(s.reverseInvocation, invocationKey(*ref.Invocation), r)
		case ref.Blocker != nil:
			addReferrer(s.reverseBlocker, blockerKey(*ref.Blocker), r)
		}
	}
}
