package reduce

// Clashes only two branches can make, checked only while a bundle that did
// not see everything before it is tried (setaside.go), so a ledger that
// never split replays exactly as before, its old duplicates included:
//   - a source.intake whose speaker and order a source on a branch it did
//     not see already holds: one message captured twice, or two messages
//     numbered alike (CodeDuplicateSourceOrder);
//   - a decision.dispose of a decision a bundle it did not see already
//     disposed (CodeCompetingDisposition): both stand on a line, as a
//     decision's dispositions, but two branches deciding apart is a clash.
// Admission refuses a new duplicate order on a line (write/gate_source.go).

import (
	"fmt"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

const (
	CodeDuplicateSourceOrder = "duplicate-source-order"
	CodeCompetingDisposition = "competing-disposition"
)

// sourceWithOrder is the earliest source in ledger order, among those keep
// accepts, whose speaker and order match; an unknown speaker matches none.
func (s *state) sourceWithOrder(project model.ProjectID, speaker model.Actor, order uint64, keep func(Origin) bool) (Source, bool) {
	var found Source
	ok := false
	for _, src := range s.sources {
		if src.Key.Project != project || src.Intake.Order != order || !model.SameActor(src.Intake.Speaker, speaker) || !keep(src.Origin) {
			continue
		}
		if !ok || src.Origin.before(found.Origin) {
			found, ok = src, true
		}
	}
	return found, ok
}

func (s *state) unseenSourceOrder(b model.Bundle, idx int, e *model.SourceIntake) error {
	if s.trial == nil {
		return nil
	}
	unseen := func(o Origin) bool { return !s.lineage.sees(s.trial.viewer, o.Sequence) }
	if src, ok := s.sourceWithOrder(b.Project, e.Speaker, e.Order, unseen); ok {
		return faultAt(CodeDuplicateSourceOrder, b.Sequence, idx, "order", fmt.Sprintf(
			"%s's message %d is already captured as source %s at sequence %d, on a branch this bundle did not see",
			e.Speaker.ID, e.Order, src.Key.Source, src.Origin.Sequence))
	}
	return nil
}

func (s *state) competingDisposition(b model.Bundle, idx int, e *model.DecisionDispose) error {
	if s.trial == nil {
		return nil
	}
	for _, o := range s.log.decisionDisposals {
		prior, ok := s.events[o].(*model.DecisionDispose)
		if ok && prior.Decision.Project == e.Decision.Project && prior.Decision.RecordID == e.Decision.RecordID && !s.lineage.sees(s.trial.viewer, o.Sequence) {
			return faultAt(CodeCompetingDisposition, b.Sequence, idx, "decision", fmt.Sprintf(
				"decision %s was already disposed (%s) at sequence %d, on a branch this bundle did not see",
				e.Decision.RecordID, prior.Disposition, o.Sequence))
		}
	}
	return nil
}
