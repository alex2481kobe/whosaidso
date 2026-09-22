package write

// U12 operations the gate enables — claim/instrument revision, trust withdrawal,
// criterion fixing, invocation start/seal, proof, task closure, decision
// open/revise and correction — and the post-replay checks that need artifact
// bytes, ledger times or the intake inbox live here. Operations that stay
// disabled (decision disposition, supersession, review, disposal) and the admission
// transaction itself do not. Proof family evaluation lives in gate_family.go.

import (
	"context"
	"fmt"
	"reflect"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

// proofIntakeLimit is recorded with every admitted proof. Completeness is
// checked against what this machine holds, never against intake it never saw.
const proofIntakeLimit = "Proof family completeness covered the ledger and this machine's intake inbox only, including rejected packets whose bytes remain there; invocations captured elsewhere and never delivered here could not be checked."

func gateProofOperation(event model.TypedEvent, author model.Actor) (*model.Provenance, error) {
	switch e := event.(type) {
	case *model.ClaimRevise:
		return &e.Provenance, nil
	case *model.DecisionOpen:
		return &e.Provenance, nil
	case *model.DecisionRevise:
		return &e.Provenance, nil
	case *model.TaskClose:
		// Authority is checked against its durable carrier in gateCloseAuthority;
		// whether the closure takes effect is checked after replay.
		return nil, nil
	case *model.InstrumentRevise:
		return &e.Provenance, gateValidation(e.Replacement.Validation)
	case *model.InvocationSeal:
		return nil, gateReconciliation(e.Envelope, author)
	case *model.TrustWithdraw, *model.Correction, *model.InvocationStart:
		// Withdrawal and correction only remove support, attributed to the
		// packet author in the review. Invocation facts are checked by the
		// reducer, by criterion freezing and by artifact resolution.
		return nil, nil
	case *model.CriterionFix:
		if e.Author != author {
			return nil, admissionFault("attribution-mismatch", "author", "criterion author must match the immutable packet author")
		}
		return nil, nil
	case *model.ProofAdmit:
		// A named judgment is the packet's own identified author, never a name
		// the author writes in for someone else, and never unknown attribution.
		if !model.SameActor(e.Judgment.Actor, author) {
			return nil, admissionFault("attribution-mismatch", "judgment.actor", "proof judgment must be the identified packet author")
		}
		return nil, nil
	}
	return nil, admissionFault("unavailable-until-integrated", "event.type", string(event.EventType())+" is not enabled by the admission gate")
}

// gateValidation requires a version beside every known validation. The artifact
// itself is resolved with every other accepted artifact, so it cannot be absent.
func gateValidation(v model.Availability[model.InstrumentValidation]) error {
	if v.State == model.Known && (v.Value == nil || model.Blank(v.Value.Version)) {
		return admissionFault("invalid-field", "spec.validation.version", "known validation needs a pinned artifact and version")
	}
	return nil
}

func gateProofProvides(project model.ProjectID, event model.TypedEvent) (gateKey, bool) {
	switch e := event.(type) {
	case *model.ClaimRevise:
		target := e.Target
		target.Revision++
		return gateKey{Record: target}, true
	case *model.InstrumentRevise:
		target := e.Target
		target.Revision++
		return gateKey{Record: target}, true
	case *model.DecisionOpen:
		return gateKey{Record: model.RecordRef{Project: project, RecordID: e.ID, Revision: 1}}, true
	case *model.DecisionRevise:
		target := e.Target
		target.Revision++
		return gateKey{Record: target}, true
	case *model.CriterionFix:
		return gateKey{Criterion: model.CriterionRef{Claim: e.Claim, CriterionID: e.CriterionID, Revision: e.Revision}}, true
	case *model.InvocationStart:
		return gateKey{Invocation: model.InvocationRef{Project: project, InvocationID: e.Envelope.InvocationID}}, true
	case *model.InvocationSeal:
		return gateKey{Sealed: e.StartRef}, true
	}
	return gateKey{}, false
}

func gateSealNeeds(event model.TypedEvent) []model.InvocationRef {
	proof, ok := event.(*model.ProofAdmit)
	if !ok {
		return nil
	}
	out := make([]model.InvocationRef, len(proof.Evidence))
	for i, member := range proof.Evidence {
		out[i] = member.InvocationRef
	}
	return out
}

// gateProofArtifacts walks the whole typed payload of each newly enabled
// operation, so no artifact field can skip resolution and root containment.
func gateProofArtifacts(event model.TypedEvent) []model.ArtifactRef {
	switch e := event.(type) {
	case *model.ClaimRevise, *model.InstrumentRevise, *model.CriterionFix, *model.InvocationStart, *model.InvocationSeal, *model.DecisionOpen, *model.DecisionRevise, *model.Correction:
		out := []model.ArtifactRef{}
		gateWalkArtifacts(reflect.ValueOf(event), &out)
		return out
	case *model.TaskClose:
		out := []model.ArtifactRef{}
		gateWalkArtifacts(reflect.ValueOf(event), &out)
		// The ruling itself is read through the authority's selector too.
		selected := e.Authority.SourceRef
		selected.Selector = e.Authority.Selector
		return append(out, selected)
	}
	return nil
}

func gateWalkArtifacts(value reflect.Value, out *[]model.ArtifactRef) {
	if value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if !value.IsNil() {
			gateWalkArtifacts(value.Elem(), out)
		}
		return
	}
	if value.Type() == reflect.TypeOf(model.ArtifactRef{}) {
		*out = append(*out, value.Interface().(model.ArtifactRef))
		return
	}
	switch value.Kind() {
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if value.Type().Field(i).IsExported() {
				gateWalkArtifacts(value.Field(i), out)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			gateWalkArtifacts(value.Index(i), out)
		}
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			gateWalkArtifacts(iter.Value(), out)
		}
	}
}

// gateProofs runs after the proposal replays and its artifacts resolve. before
// is the admitted prefix; after includes this admission set.
func gateProofs(ctx context.Context, project store.Project, prefix []model.Bundle, before, after reduce.Snapshot, packets []model.Packet) error {
	for _, packet := range packets {
		for _, raw := range packet.Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				return err
			}
			switch e := event.(type) {
			case *model.InvocationStart:
				if err := gateCriterionFrozen(prefix, before, project.ID, e.Envelope); err != nil {
					return err
				}
			case *model.ProofAdmit:
				if err := gateProofFamily(ctx, project, after, e); err != nil {
					return err
				}
			case *model.TaskClose:
				if err := gateClosureEffective(after, e); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// gateCriterionFrozen: a run may name a criterion only if that criterion was
// admitted in an earlier bundle recorded before the run started. Ledger order
// alone is not enough, because one admission set can order a late criterion first.
func gateCriterionFrozen(prefix []model.Bundle, before reduce.Snapshot, project model.ProjectID, env model.InvocationEnvelope) error {
	if env.CriterionRef.State != model.Known || env.CriterionRef.Value == nil {
		return nil
	}
	ref := *env.CriterionRef.Value
	criterion, ok := before.Criterion(ref)
	if ref.Claim.Project != project || !ok {
		return admissionFault("criterion-not-frozen", "envelope.criterion_ref", "the criterion was not admitted here before this admission set")
	}
	for _, bundle := range prefix {
		if bundle.Sequence == criterion.Origin.Sequence {
			if !bundle.RecordedAt.Before(env.StartedAt) {
				return admissionFault("criterion-not-frozen", "envelope.criterion_ref",
					fmt.Sprintf("criterion admitted at %s, not before the run started at %s", bundle.RecordedAt.Format("2006-01-02T15:04:05.999999999Z07:00"), env.StartedAt.Format("2006-01-02T15:04:05.999999999Z07:00")))
			}
			return nil
		}
	}
	return admissionFault("criterion-not-frozen", "envelope.criterion_ref", "the criterion's admitting bundle is not in the prefix")
}

// gateProofLimit is the review-reason suffix recording proofIntakeLimit.
func gateProofLimit(outcome string, packets []model.Packet) string {
	if outcome != "accepted" {
		return ""
	}
	for _, packet := range packets {
		for _, raw := range packet.Events {
			if raw.Type == "proof.admit" {
				return "\n" + proofIntakeLimit
			}
		}
	}
	return ""
}

// gateCloseAuthority: a closure cites a named authority whose exact words are an
// admitted or bundled source.intake spoken by that actor about this revision.
func gateCloseAuthority(event model.TypedEvent, sources []reduce.Source) error {
	e, ok := event.(*model.TaskClose)
	if !ok {
		return nil
	}
	if model.Blank(e.Authority.Actor.ID) {
		return admissionFault("authority-unavailable", "authority.actor", "unknown attribution cannot close a task")
	}
	if !containsAdmissionRef(e.Authority.Scope.ContextRefs, e.Task) {
		return admissionFault("authority-scope", "authority.scope.context_refs", "closure authority must name the exact task revision")
	}
	for _, source := range sources {
		if source.Intake.Speaker == e.Authority.Actor && sameAdmissionArtifact(source.Intake.SourceRef, e.Authority.SourceRef) && containsAdmissionRef(source.Intake.Referents, e.Task) {
			return nil
		}
	}
	return admissionFault("authority-unavailable", "authority.source_ref", "no admitted or bundled carrier matches the source, speaker and exact task revision")
}

// gateClosureEffective refuses a closure the projection would not honour: a
// writer still holds an attempt, or success lacks witnesses for the revision.
func gateClosureEffective(after reduce.Snapshot, e *model.TaskClose) error {
	task, ok := after.Task(reduce.Ident{Project: e.Task.Project, ID: e.Task.RecordID})
	if !ok || task.Status != reduce.StatusClosed {
		return admissionFault("closure-ineffective", "task", fmt.Sprintf("closure would leave the task %s: live attempts %d, owed %v", task.Status, len(task.LiveAttempts), task.Reasons))
	}
	return nil
}

// gateReconciliation: a seal with an UNKNOWN outcome records that the terminal
// observation was never made (a dead runner). It must carry no reading at all,
// and whoever reconciled it must be identified.
func gateReconciliation(env model.InvocationEnvelope, author model.Actor) error {
	if env.Outcome.State != model.Unknown {
		return nil
	}
	if model.Blank(author.ID) {
		return admissionFault("attribution-unknown", "packet.author", "a reconciliation seal needs an identified author")
	}
	for _, f := range []struct {
		name  string
		state model.AvailabilityState
	}{
		{"observed_at", env.ObservedAt.State}, {"output_refs", env.OutputRefs.State}, {"config_effective", env.ConfigEffective.State},
		{"conditions_observed", env.ConditionsObserved.State}, {"isolation", env.Isolation.State}, {"visual", env.Visual.State},
	} {
		if f.state != model.Unknown {
			return admissionFault("reconciliation-reading", "envelope."+f.name, "a seal with an unknown outcome cannot carry an observation")
		}
	}
	return nil
}
