package write

// U12 operations the gate enables — claim/instrument revision, trust withdrawal,
// criterion fixing, invocation start/seal and proof — and the proof checks that
// need artifact bytes, ledger times or the intake inbox live here. Operations
// that stay disabled (decisions, supersession, correction, closure, review,
// disposal) and the admission transaction itself do not.

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
const proofIntakeLimit = "Proof family completeness covered the ledger and this machine's intake inbox only; invocations captured elsewhere and never delivered here could not be checked."

func gateProofOperation(event model.TypedEvent, author model.Actor) (*model.Provenance, error) {
	switch e := event.(type) {
	case *model.ClaimRevise:
		return &e.Provenance, nil
	case *model.InstrumentRevise:
		return &e.Provenance, gateValidation(e.Replacement.Validation)
	case *model.TrustWithdraw, *model.InvocationStart, *model.InvocationSeal:
		// Withdrawal only removes support. Invocation facts are checked by the
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
	switch event.(type) {
	case *model.ClaimRevise, *model.InstrumentRevise, *model.CriterionFix, *model.InvocationStart, *model.InvocationSeal:
		out := []model.ArtifactRef{}
		gateWalkArtifacts(reflect.ValueOf(event), &out)
		return out
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
