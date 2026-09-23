package reduce

// The R14.1 verdict rules that proof_admission.go applies member by member
// live here: which revision a refutation may judge, and the bar every counted
// member meets, supporting or contradicting. Which member counts under which
// verdict, and the family rules, stay in proof_admission.go and proof_family.go.

import "datum/internal/model"

// laterCriterionRevision reports whether a revision of ref's criterion later
// than ref's is admitted on the same claim revision. Revisions need not be
// contiguous, so every admitted criterion is asked.
func (s *state) laterCriterionRevision(ref model.CriterionRef) bool {
	want := criterionKey(ref)
	for key := range s.criteria {
		if key.Project == want.Project && key.Claim == want.Claim && key.ClaimRevision == want.ClaimRevision &&
			key.CriterionID == want.CriterionID && key.CriterionRevision > want.CriterionRevision {
			return true
		}
	}
	return false
}

// countedMember is the bar every counted member meets, supporting or
// contradicting: a completed observation from a validated local instrument,
// with no unresolved support loss of its own.
func (s *state) countedMember(b model.Bundle, idx int, e *model.ProofAdmit, inv Invocation) error {
	instrument, ok := s.records[recordKey(inv.Start.InstrumentRef)]
	if !completedObservation(inv, e.Claim) || !ok || instrument.Kind != model.Instrument || instrument.Instrument.Validation.State != model.Known {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, "evidence", "a counted member must be a completed observation from a validated local instrument")
	}
	if len(s.supportLosses(invocationNode(inv.Key))) != 0 {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, "evidence", "observation has unresolved support loss")
	}
	return nil
}
