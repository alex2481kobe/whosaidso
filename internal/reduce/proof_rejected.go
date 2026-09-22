package reduce

// Criterion family membership beyond the exact revision lives here: runs that a
// rejected or correction-requested review recorded (R10.3), runs under earlier
// revisions of the same criterion, and the only way a proof may name either.
// Admitted family closure and support rules stay in proof_admission.go; review
// bookkeeping stays in events.go.

import (
	"datum/internal/model"
)

// CriterionFamily reports whether a run carrying ref belongs to the evaluation
// family of a proof under criterion, and whether it does only through an
// earlier revision. A new revision cannot erase known counterevidence, so runs
// under earlier revisions of the same criterion on the same claim revision stay
// members; they can only be dispositioned inapplicable or inconclusive.
func CriterionFamily(ref model.Availability[model.CriterionRef], criterion model.CriterionRef) (member, earlier bool) {
	if ref.State != model.Known || ref.Value == nil {
		return false, false
	}
	v := *ref.Value
	if v.Claim != criterion.Claim || v.CriterionID != criterion.CriterionID || v.Revision > criterion.Revision {
		return false, false
	}
	return true, v.Revision < criterion.Revision
}

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
			if member, _ := CriterionFamily(inv.CriterionRef, criterion); !member {
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
// never admitted, or that tested an earlier criterion revision: it cannot
// support this revision or be weighed as its contradiction, only be set aside
// with a reason under the judgment.
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
