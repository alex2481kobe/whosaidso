package write

// Rejected criterion family members (R10.3) live here: extracting the
// invocation facts a rejected or correction-requested review records, and the
// proof gate's closure over those ledger facts, including U12's byte-identity
// rule. Admitted and pending family closure stays in gate_family.go; the
// reducer's reference and closure checks stay in internal/reduce.

import (
	"fmt"

	"datum/internal/model"
	"datum/internal/reduce"
)

// reviewedInvocations extracts, for a packet set that is not accepted, every
// invocation.start/seal it carried. An event that does not decode as a typed
// event is not an invocation the closed schema can name, so it records nothing.
func reviewedInvocations(outcome string, packets []model.Packet) ([]model.ReviewedInvocation, error) {
	if outcome == "accepted" {
		return nil, nil
	}
	var out []model.ReviewedInvocation
	for _, packet := range packets {
		for _, raw := range packet.Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				continue
			}
			env := invocationEnvelope(event)
			if env == nil {
				continue
			}
			digest, err := envelopeDigest(*env)
			if err != nil {
				return nil, err
			}
			out = append(out, model.ReviewedInvocation{Packet: packet.CommandID, Event: string(event.EventType()),
				InvocationID: env.InvocationID, CriterionRef: env.CriterionRef, EnvelopeDigest: digest})
		}
	}
	return out, nil
}

func envelopeDigest(env model.InvocationEnvelope) (model.Digest, error) {
	data, err := model.Encode(env)
	if err != nil {
		return "", err
	}
	return model.HashBytes(data), nil
}

// gateRejectedMembers runs before replay, so a proof that omits a run the
// ledger recorded as rejected, or dispositions it as anything but inapplicable
// or inconclusive, is refused with its own code. A run proposed in this same
// set is admitted with the proof and belongs to the admitted family instead.
func gateRejectedMembers(snapshot reduce.Snapshot, event model.TypedEvent, providers map[gateKey]int) error {
	proof, ok := event.(*model.ProofAdmit)
	if !ok {
		return nil
	}
	dispositions := map[model.InvocationRef]string{}
	for _, member := range proof.Evidence {
		dispositions[member.InvocationRef] = member.Disposition
	}
	for _, review := range snapshot.Reviews() {
		if review.Outcome == "accepted" {
			continue
		}
		for _, fact := range review.Invocations {
			if fact.CriterionRef.State != model.Known || fact.CriterionRef.Value == nil || *fact.CriterionRef.Value != proof.CriterionRef {
				continue
			}
			ref := model.InvocationRef{Project: review.Key.Project, InvocationID: fact.InvocationID}
			_, admitted := snapshot.Invocation(reduce.InvocationKey{Project: ref.Project, InvocationID: ref.InvocationID})
			if _, proposed := providers[gateKey{Invocation: ref}]; admitted || proposed {
				continue
			}
			path := "intake/" + string(review.Key.CommandID)
			switch dispositions[ref] {
			case "inapplicable", "inconclusive":
			case "":
				return admissionFault("rejected-family-member", path,
					fmt.Sprintf("%s invocation %s carries this criterion and the proof omits it; a rejected run stays in the family", review.Outcome, fact.InvocationID))
			default:
				return admissionFault("rejected-family-member", path,
					fmt.Sprintf("%s invocation %s can only be dispositioned inapplicable or inconclusive, never %s", review.Outcome, fact.InvocationID, dispositions[ref]))
			}
		}
	}
	return nil
}

// gateRejectedFamily: a rejected run carrying the criterion never leaves the
// family, and the ledger alone decides it. Omission and disposition are checked
// before replay by gateRejectedMembers and again by the reducer. What needs the
// replayed set is U12's identity rule: if the ledger also
// admitted that invocation, the rejected start or seal must be byte-identical
// to the admitted one, or the proof is refused. The returned set names the
// rejected-only members, which are accounted for but never evaluated.
func gateRejectedFamily(after reduce.Snapshot, e *model.ProofAdmit, carries func(model.InvocationEnvelope) bool) (map[model.InvocationRef]bool, error) {
	rejected := map[model.InvocationRef]bool{}
	for _, review := range after.Reviews() {
		if review.Outcome == "accepted" {
			continue
		}
		for _, fact := range review.Invocations {
			if !carries(model.InvocationEnvelope{CriterionRef: fact.CriterionRef}) {
				continue
			}
			ref := model.InvocationRef{Project: review.Key.Project, InvocationID: fact.InvocationID}
			inv, admitted := after.Invocation(reduce.InvocationKey{Project: ref.Project, InvocationID: ref.InvocationID})
			if !admitted {
				rejected[ref] = true
				continue
			}
			same, err := sameAdmittedEnvelope(inv, fact)
			if err != nil {
				return nil, err
			}
			if !same {
				return nil, admissionFault("rejected-family-member", "intake/"+string(review.Key.CommandID),
					fmt.Sprintf("%s %s of invocation %s differs from the admitted one; a rejected run stays in the family", review.Outcome, fact.Event, fact.InvocationID))
			}
		}
	}
	return rejected, nil
}

// sameAdmittedEnvelope compares a rejected fact with the admitted start or seal
// of the same invocation. A rejected seal with no admitted seal is not the same.
func sameAdmittedEnvelope(inv reduce.Invocation, fact model.ReviewedInvocation) (bool, error) {
	env := &inv.Start
	if fact.Event == "invocation.seal" {
		env = inv.Seal
	}
	if env == nil {
		return false, nil
	}
	digest, err := envelopeDigest(*env)
	return err == nil && digest == fact.EnvelopeDigest, err
}

// gateRejectedRecorded reports whether a non-accepted review recorded ref.
func gateRejectedRecorded(snapshot reduce.Snapshot, ref model.InvocationRef) bool {
	for _, review := range snapshot.Reviews() {
		if review.Outcome == "accepted" || review.Key.Project != ref.Project {
			continue
		}
		for _, fact := range review.Invocations {
			if fact.InvocationID == ref.InvocationID {
				return true
			}
		}
	}
	return false
}
