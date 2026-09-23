package write

// Proof admission's artifact evaluation lives here: closure over pending,
// unreviewed intake carrying the criterion or an earlier revision of it, each
// exact-revision member's disposition against its own computed verdict,
// criterion satisfaction, and re-resolution of each supporting instrument's
// validation artifact. Family membership, ledger closure, rejected members and
// earlier revisions are decided once, by the reducer's proof family checker,
// when the proposal replays. Enabling operations, criterion freezing and the
// transaction do not live here.

import (
	"context"
	"fmt"

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
	// The family includes runs under earlier revisions of this criterion: a new
	// revision cannot erase known counterevidence. The ledger's closure was
	// decided when the proposal replayed; unreviewed intake is outside it.
	carries := func(env model.InvocationEnvelope) bool {
		member, _ := reduce.CriterionFamily(env.CriterionRef, e.CriterionRef)
		return member
	}
	if err := gatePendingIntake(project, after, carries); err != nil {
		return err
	}
	resolver := evidence.NewResolver(project.Root)
	supports := []evidence.Observation{}
	for i, member := range e.Evidence {
		path := fmt.Sprintf("evidence[%d]", i)
		// Earlier-revision and rejected-only members are accounted for under the
		// judgment by the reducer's rules, never evaluated or support.
		inv, class := after.ProofMember(e.CriterionRef, member.InvocationRef)
		if class != reduce.MemberExact {
			continue
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

// gatePendingIntake: durable intake nobody has reviewed yet that carries the
// criterion must be admitted in this set, so a proof is not admitted ahead of a
// run waiting in review. This is an admission-time "not yet", never a validity
// rule: reviewed packets, accepted or not, are judged from the ledger alone.
func gatePendingIntake(project store.Project, after reduce.Snapshot, carries func(model.InvocationEnvelope) bool) error {
	intake, err := store.ReadIntake(project, nil)
	if err != nil {
		return err
	}
	for _, packet := range intake {
		if _, reviewed := after.Review(reduce.ReviewKey{Project: project.ID, CommandID: packet.CommandID}); reviewed {
			continue
		}
		for _, raw := range packet.Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				return err
			}
			if env := invocationEnvelope(event); env != nil && carries(*env) {
				return admissionFault("pending-reconciliation", "intake/"+string(packet.CommandID),
					fmt.Sprintf("unadmitted intake carries invocation %s of this criterion; admit it in the same set", env.InvocationID))
			}
		}
	}
	return nil
}

func invocationEnvelope(event model.TypedEvent) *model.InvocationEnvelope {
	switch t := event.(type) {
	case *model.InvocationStart:
		return &t.Envelope
	case *model.InvocationSeal:
		return &t.Envelope
	}
	return nil
}
