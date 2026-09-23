package reduce

// The one proof family checker lives here, shared by Replay and admission's
// Apply: family membership (same exact claim revision, same criterion id, a
// criterion revision no later than the proof's), classification of each
// listed member as exact, earlier-revision or rejected-only (R10.3), closure
// over the admitted prefix and over invocations later in the same bundle, and
// U12's rule that a rejected start or seal must be byte-identical to the
// admitted one. Support rules for exact members stay in proof_admission.go;
// artifact evaluation and pending intake stay in internal/write.

import (
	"fmt"
	"sort"

	"datum/internal/model"
)

// Proof family refusal codes. Admission reports these same codes because it
// reaches them through Apply.
const (
	// CodeIncompleteFamily is a proof omitting a sealed family member.
	CodeIncompleteFamily = "incomplete-family"
	// CodeRejectedFamilyMember is a rejected run a proof omitted, misused as
	// support or contradiction, or whose recorded bytes differ from the admitted run.
	CodeRejectedFamilyMember = "rejected-family-member"
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

// MemberClass is how a proof's family holds one listed run.
type MemberClass string

const (
	// MemberOutside is not a member of this proof's family.
	MemberOutside MemberClass = ""
	// MemberExact is an admitted run under the proof's exact criterion revision.
	MemberExact MemberClass = "exact"
	// MemberEarlier is an admitted run under an earlier revision: set aside, never support.
	MemberEarlier MemberClass = "earlier"
	// MemberRejected is a run only a non-accepted review recorded: set aside, never support.
	MemberRejected MemberClass = "rejected"
)

// setAside is the only honest disposition of an earlier-revision or
// rejected-only member: it cannot support this revision or be weighed as its
// contradiction, only be accounted for with a reason under the judgment.
func setAside(disposition string) bool {
	return disposition == "inapplicable" || disposition == "inconclusive"
}

// ProofMember classifies one run for a proof of criterion. It is the same
// classification the reducer admitted the proof under, so the artifact
// evaluator evaluates exactly the exact-revision members.
func (s Snapshot) ProofMember(criterion model.CriterionRef, ref model.InvocationRef) (Invocation, MemberClass) {
	inv, class := s.inner().memberClass(criterion, invocationKey(ref))
	return deepCopy(inv), class
}

// RejectedRecorded reports whether a non-accepted review recorded ref: a run a
// proof may name although the ledger never admitted it.
func (s Snapshot) RejectedRecorded(ref model.InvocationRef) bool {
	return s.inner().rejectedRecorded(invocationKey(ref))
}

func (s *state) memberClass(criterion model.CriterionRef, key InvocationKey) (Invocation, MemberClass) {
	if inv, ok := s.invocations[key]; ok {
		member, earlier := CriterionFamily(inv.Start.CriterionRef, criterion)
		switch {
		case !member:
			return inv, MemberOutside
		case earlier:
			return inv, MemberEarlier
		}
		return inv, MemberExact
	}
	for _, r := range s.rejectedFacts() {
		if r.Review.Project == key.Project && r.Fact.InvocationID == key.InvocationID {
			if member, _ := CriterionFamily(r.Fact.CriterionRef, criterion); member {
				return Invocation{}, MemberRejected
			}
		}
	}
	return Invocation{}, MemberOutside
}

// rejectedFacts are the admitted prefix's rejected facts plus the whole
// candidate bundle's, in ledger order. Reviews already applied from the
// candidate bundle come from its inventory, not twice.
func (s *state) rejectedFacts() []RejectedFact {
	var reviews []Review // usually none are rejected; never size for all reviews
	for _, r := range s.reviews {
		if r.Outcome != "accepted" && (s.bundle == nil || r.Origin.Sequence <= s.watermark.Sequence) {
			reviews = append(reviews, r)
		}
	}
	sort.Slice(reviews, func(i, j int) bool {
		if reviews[i].Origin != reviews[j].Origin {
			return reviews[i].Origin.before(reviews[j].Origin)
		}
		return reviews[i].Key.CommandID < reviews[j].Key.CommandID
	})
	var out []RejectedFact
	for _, r := range reviews {
		for _, fact := range r.Invocations {
			out = append(out, RejectedFact{Review: r.Key, Outcome: r.Outcome, Origin: r.Origin, Fact: fact})
		}
	}
	if s.bundle != nil {
		out = append(out, s.bundle.rejected...)
	}
	return out
}

// rejectedRecorded is sequential: only reviews already applied count, so a
// review later in the same bundle never resolves a reference.
func (s *state) rejectedRecorded(key InvocationKey) bool {
	for _, review := range s.reviews {
		if review.Outcome == "accepted" || review.Key.Project != key.Project {
			continue
		}
		for _, inv := range review.Invocations {
			if inv.InvocationID == key.InvocationID {
				return true
			}
		}
	}
	return false
}

// checkFamilyClosure: the family is every admitted invocation carrying this
// criterion or an earlier revision of it, including ones this same bundle
// admits after the proof, not the members a proposer chose to list. An
// unsealed member has no result yet, so proof waits for its reconciliation
// rather than treating it as absent.
func (s *state) checkFamilyClosure(b model.Bundle, idx int, e *model.ProofAdmit, listed map[InvocationKey]string) error {
	refuse := func(key InvocationKey, sealed bool, later string) error {
		if !sealed {
			return faultAt(CodeInvalidTransition, b.Sequence, idx, "evidence",
				fmt.Sprintf("family member %s%s has no admitted seal; proof waits for reconciliation", key.InvocationID, later))
		}
		return faultAt(CodeIncompleteFamily, b.Sequence, idx, "evidence",
			fmt.Sprintf("proof omits sealed family member %s%s", key.InvocationID, later))
	}
	for _, inv := range s.invocationsSorted() {
		if member, _ := CriterionFamily(inv.Start.CriterionRef, e.CriterionRef); !member {
			continue
		}
		sealed := inv.Seal != nil || s.bundle.laterSeal(inv.Key, idx) != nil
		if _, ok := listed[inv.Key]; !sealed || !ok {
			if err := s.refuseProof(refuse(inv.Key, sealed, "")); err != nil {
				return err
			}
		}
	}
	for _, start := range s.bundle.laterStarts(idx) {
		if member, _ := CriterionFamily(start.Envelope.CriterionRef, e.CriterionRef); member {
			key := InvocationKey{Project: b.Project, InvocationID: start.Envelope.InvocationID}
			if err := s.refuseProof(refuse(key, s.bundle.laterSeal(key, idx) != nil, " (admitted later in this bundle)")); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkRejectedFamily: a rejected run carrying the criterion never leaves the
// family. If the ledger never admitted it, the proof must list it (its
// disposition is checked with the other members). If the ledger did admit it,
// before the proof or later in this bundle, the recorded start or seal must be
// the admitted one byte for byte, so a substitute reading cannot stand in.
func (s *state) checkRejectedFamily(b model.Bundle, idx int, e *model.ProofAdmit, listed map[InvocationKey]string) error {
	for _, r := range s.rejectedFacts() {
		if member, _ := CriterionFamily(r.Fact.CriterionRef, e.CriterionRef); !member {
			continue
		}
		key := InvocationKey{Project: r.Review.Project, InvocationID: r.Fact.InvocationID}
		env, admitted := s.admittedEnvelope(key, r.Fact.Event, idx)
		if !admitted {
			if _, ok := listed[key]; !ok {
				if err := s.refuseProof(faultAt(CodeRejectedFamilyMember, b.Sequence, idx, "evidence",
					fmt.Sprintf("%s invocation %s carries this criterion and the proof omits it; a rejected run stays in the family", r.Outcome, key.InvocationID))); err != nil {
					return err
				}
			}
			continue
		}
		same := false
		if env != nil {
			digest, err := model.EnvelopeDigest(*env)
			if err != nil {
				return faultAt(CodeInvalidField, b.Sequence, idx, "evidence", err.Error())
			}
			same = digest == r.Fact.EnvelopeDigest
		}
		if !same {
			if err := s.refuseProof(faultAt(CodeRejectedFamilyMember, b.Sequence, idx, "evidence",
				fmt.Sprintf("%s %s of invocation %s differs from the admitted one; a rejected run stays in the family", r.Outcome, r.Fact.Event, key.InvocationID))); err != nil {
				return err
			}
		}
	}
	return nil
}

// admittedEnvelope is the admitted start or seal of key, from the prefix or
// later in this bundle. admitted reports whether any start was admitted; a nil
// envelope with admitted true is a seal nobody admitted, which is not the same.
func (s *state) admittedEnvelope(key InvocationKey, event string, idx int) (*model.InvocationEnvelope, bool) {
	start, seal := s.bundle.laterStart(key, idx), s.bundle.laterSeal(key, idx)
	if inv, ok := s.invocations[key]; ok {
		start = &inv.Start
		if inv.Seal != nil {
			seal = inv.Seal
		}
	}
	if start == nil {
		return nil, false
	}
	if event == "invocation.seal" {
		return seal, true
	}
	return start, true
}
