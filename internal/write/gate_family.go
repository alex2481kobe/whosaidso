package write

// Proof admission's evaluation-family checks live here: closure over admitted,
// pending and rejected invocations carrying the criterion, per-member dispositions
// against the member's own computed verdict, criterion satisfaction, and
// re-resolution of each supporting instrument's validation artifact.
// Enabling operations, criterion freezing and the transaction do not.

import (
	"context"
	"fmt"
	"reflect"

	"datum/internal/evidence"
	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

func gateProofFamily(ctx context.Context, project store.Project, after reduce.Snapshot, e *model.ProofAdmit) error {
	criterion, ok := after.Criterion(e.CriterionRef)
	if !ok {
		return admissionFault("unknown-reference", "criterion_ref", "proof names no admitted criterion")
	}
	carries := func(env model.InvocationEnvelope) bool {
		return env.CriterionRef.State == model.Known && env.CriterionRef.Value != nil && *env.CriterionRef.Value == e.CriterionRef
	}
	listed := map[model.InvocationRef]bool{}
	for _, member := range e.Evidence {
		listed[member.InvocationRef] = true
	}
	// Every admitted member, including ones this same set orders after the proof.
	for _, inv := range after.Invocations() {
		if !carries(inv.Start) {
			continue
		}
		if inv.Seal == nil {
			return admissionFault("pending-reconciliation", "evidence", fmt.Sprintf("family member %s has no admitted seal", inv.Key.InvocationID))
		}
		if !listed[model.InvocationRef{Project: inv.Key.Project, InvocationID: inv.Key.InvocationID}] {
			return admissionFault("incomplete-family", "evidence", fmt.Sprintf("proof omits sealed family member %s", inv.Key.InvocationID))
		}
	}
	if err := gateIntakeFamily(project, after, carries); err != nil {
		return err
	}
	resolver := evidence.NewResolver(project.Root)
	supports := []evidence.Observation{}
	for i, member := range e.Evidence {
		path := fmt.Sprintf("evidence[%d]", i)
		inv, ok := after.Invocation(reduce.InvocationKey{Project: member.InvocationRef.Project, InvocationID: member.InvocationRef.InvocationID})
		if !ok || inv.Seal == nil {
			return admissionFault("pending-reconciliation", path, "member has no admitted seal")
		}
		observation, err := resolver.Observe(ctx, criterion.Fix, *inv.Seal)
		if err != nil {
			return err
		}
		own, err := evidence.Evaluate(criterion.Fix, []evidence.Observation{observation})
		if err != nil {
			return err
		}
		// FALSE is a computed counterexample. Only "contradicts" names it
		// honestly, and the reducer refuses every unresolved contradiction.
		if own.Verdict == evidence.False && member.Disposition != "contradicts" {
			return admissionFault("counterevidence-unresolved", path+".disposition", "member fails the criterion: "+own.Reason)
		}
		if member.Disposition != "supports" {
			continue
		}
		if err := gateInstrumentValidation(ctx, resolver, after, inv.Start.InstrumentRef, path); err != nil {
			return err
		}
		supports = append(supports, observation)
	}
	// Every supporting member must be TRUE and comparable with the others;
	// one UNKNOWN or incomparable member leaves the family unsatisfied.
	family, err := evidence.Evaluate(criterion.Fix, supports)
	if err != nil {
		return err
	}
	if family.Verdict != evidence.True {
		return admissionFault("criterion-unsatisfied", "evidence", fmt.Sprintf("supporting family is %s: %s", family.Verdict, family.Reason))
	}
	return nil
}

// gateInstrumentValidation re-resolves the validation artifact at proof time.
// UNKNOWN validation, or KNOWN validation whose bytes no longer resolve inside
// the root, is not applicable validation.
func gateInstrumentValidation(ctx context.Context, resolver *evidence.Resolver, after reduce.Snapshot, ref model.RecordRef, path string) error {
	record, ok := after.Record(ref)
	if !ok || record.Instrument == nil || record.Instrument.Validation.State != model.Known || record.Instrument.Validation.Value == nil {
		return admissionFault("validation-unknown", path, "supporting member's instrument has no known validation")
	}
	artifact := record.Instrument.Validation.Value.Ref
	resolved, err := resolver.Resolve(ctx, artifact)
	if err != nil {
		return admissionFault("validation-unavailable", path, "instrument validation artifact does not resolve: "+err.Error())
	}
	reading, err := evidence.Select(resolved, artifact.Selector)
	if err != nil || reading.Kind == evidence.ReadingAbsent {
		return admissionFault("validation-unavailable", path, "instrument validation selector reads nothing")
	}
	return nil
}

// gateIntakeFamily: durable intake carrying the criterion belongs to the family.
// Unreviewed packets must be admitted in this set. A rejected or correction-
// requested packet cannot make its run vanish: the ledger must hold that same
// invocation with identical start or seal bytes (so it is in the admitted family
// and dispositioned there), or the proof is refused.
func gateIntakeFamily(project store.Project, after reduce.Snapshot, carries func(model.InvocationEnvelope) bool) error {
	intake, err := store.ReadIntake(project, nil)
	if err != nil {
		return err
	}
	for _, packet := range intake {
		review, reviewed := after.Review(reduce.ReviewKey{Project: project.ID, CommandID: packet.CommandID})
		if reviewed && review.Outcome == "accepted" {
			continue
		}
		for _, raw := range packet.Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				return err
			}
			var env *model.InvocationEnvelope
			sealed := false
			switch t := event.(type) {
			case *model.InvocationStart:
				env = &t.Envelope
			case *model.InvocationSeal:
				env, sealed = &t.Envelope, true
			}
			if env == nil || !carries(*env) {
				continue
			}
			path := "intake/" + string(packet.CommandID)
			if !reviewed {
				return admissionFault("pending-reconciliation", path,
					fmt.Sprintf("unadmitted intake carries invocation %s of this criterion; admit it in the same set", env.InvocationID))
			}
			inv, ok := after.Invocation(reduce.InvocationKey{Project: project.ID, InvocationID: env.InvocationID})
			same := ok && (!sealed && reflect.DeepEqual(inv.Start, *env) || sealed && inv.Seal != nil && reflect.DeepEqual(*inv.Seal, *env))
			if !same {
				return admissionFault("rejected-family-member", path,
					fmt.Sprintf("%s packet carries invocation %s of this criterion and the ledger does not hold it; a rejected run stays in the family", review.Outcome, env.InvocationID))
			}
		}
	}
	return nil
}
