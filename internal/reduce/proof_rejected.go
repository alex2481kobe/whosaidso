package reduce

// Rejected criterion family members (R10.3) live here: finding invocations that
// a rejected or correction-requested review recorded, and the only way a proof
// may name one. Admitted family closure and support rules stay in
// proof_admission.go; review bookkeeping stays in events.go.

import (
	"datum/internal/model"
)

// rejectedMembers returns the invocations that a non-accepted review recorded
// as carrying criterion and that the ledger never admitted. They are family
// members a proof must account for, decided from the ledger alone.
func (s *state) rejectedMembers(criterion model.CriterionRef) map[InvocationKey]bool {
	out := map[InvocationKey]bool{}
	for _, review := range s.reviews {
		if review.Outcome == "accepted" {
			continue
		}
		for _, inv := range review.Invocations {
			if inv.CriterionRef.State != model.Known || inv.CriterionRef.Value == nil || *inv.CriterionRef.Value != criterion {
				continue
			}
			key := InvocationKey{Project: review.Key.Project, InvocationID: inv.InvocationID}
			if _, admitted := s.invocations[key]; !admitted {
				out[key] = true
			}
		}
	}
	return out
}

// rejectedRecorded reports whether any non-accepted review recorded ref.
func (s *state) rejectedRecorded(ref model.InvocationRef) bool {
	for _, review := range s.reviews {
		if review.Outcome == "accepted" || review.Key.Project != ref.Project {
			continue
		}
		for _, inv := range review.Invocations {
			if inv.InvocationID == ref.InvocationID {
				return true
			}
		}
	}
	return false
}

// rejectedMemberDisposition is the only honest disposition of a run that was
// never admitted: its reading is not canonical, so it cannot support or be
// weighed as a contradiction, only set aside with a reason under the judgment.
func rejectedMemberDisposition(disposition string) bool {
	return disposition == "inapplicable" || disposition == "inconclusive"
}

// rejectedEvidence decides a same-project invocation reference that names no
// admitted invocation. The first result is false unless a proof names a run a
// review recorded, leaving the unknown-reference refusal to the caller. A recorded rejected run
// is accepted only as a proof member of the criterion it carried, dispositioned
// inapplicable or inconclusive; it can never be counted as support.
func (s *state) rejectedEvidence(b model.Bundle, idx int, e model.TypedEvent, ref model.InvocationRef, path string) (bool, error) {
	proof, ok := e.(*model.ProofAdmit)
	if !ok || !s.rejectedRecorded(ref) {
		return false, nil
	}
	refuse := func(detail string) (bool, error) {
		return true, faultAt(CodeInvalidTransition, b.Sequence, idx, path, detail)
	}
	if !s.rejectedMembers(proof.CriterionRef)[invocationKey(ref)] {
		return refuse("the rejected run did not carry this proof's criterion")
	}
	for _, member := range proof.Evidence {
		if member.InvocationRef == ref && rejectedMemberDisposition(member.Disposition) {
			return true, nil
		}
	}
	return refuse("a rejected run can only be dispositioned inapplicable or inconclusive, never support")
}
