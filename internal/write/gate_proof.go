package write

// U12 operations the gate enables — claim/instrument revision, trust withdrawal,
// criterion fixing, invocation start/seal, proof, task closure, decision
// open/revise/dispose and correction — and the post-replay checks that need
// artifact bytes or pending intake live here. Supersession and
// artifact disposal rules live in gate_supersede.go; review stays disabled; the
// admission transaction itself does not live here. Proof family evaluation
// lives in gate_family.go.

import (
	"context"
	"fmt"
	"reflect"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

func gateProofOperation(event model.TypedEvent, author model.Actor) (*model.Provenance, error) {
	switch e := event.(type) {
	case *model.ClaimRevise:
		return &e.Provenance, nil
	case *model.DecisionOpen:
		return &e.Provenance, nil
	case *model.DecisionRevise:
		return &e.Provenance, nil
	case *model.DecisionDispose:
		// R10.1 as overruled: an agent writes the packet that records a ruling.
		// The packet author stays whoever wrote it and is never checked against
		// or replaced by the authority; author, authority and the exact quote
		// (nonblank by schema) are all recorded: visible, not blocked.
		if model.Blank(e.Authority.Actor.ID) {
			return nil, admissionFault("authority-unavailable", "authority.actor", "a disposition must name the authority that ruled")
		}
		return nil, nil
	case *model.Supersede, *model.ArtifactDispose:
		return nil, gateOwnerActOperation(event)
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
		// reducer, including criterion freezing, and by artifact resolution.
		return nil, nil
	case *model.ProofAdmit:
		// R14.1: every new proof states its verdict. Replay reads a missing
		// one as supports, because proofs admitted before R14.1 carry none and
		// could mean nothing else; this is the entrance that keeps it rare.
		if e.Verdict == "" {
			return nil, admissionFault("verdict-required", "verdict", "a proof must state its verdict: supports or refutes")
		}
		// The judgment must be the identified packet author: see CriterionFix.
		return nil, nil
	case *model.CriterionFix:
		// The criterion author and the proof judgment must be the identified
		// packet author. The reducer checks that from the review's recorded
		// authors, on admission and replay alike (reduce/packet_author.go).
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
		return gateAuthorityArtifacts(event, e.Authority)
	case *model.DecisionDispose:
		return gateAuthorityArtifacts(event, e.Authority)
	case *model.Supersede, *model.ArtifactDispose:
		return gateOwnerActArtifacts(event)
	}
	return nil
}

// gateAuthorityArtifacts walks the payload and also reads the ruling itself
// through the authority's selector.
func gateAuthorityArtifacts(event model.TypedEvent, authority model.Authority) []model.ArtifactRef {
	out := []model.ArtifactRef{}
	gateWalkArtifacts(reflect.ValueOf(event), &out)
	selected := authority.SourceRef
	selected.Selector = authority.Selector
	return append(out, selected)
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

// gateProofs runs after the proposal replays and its artifacts resolve. after
// includes this admission set. Criterion freezing is the reducer's, for every
// start (reduce/freeze.go).
// A dry run records each event's refusal and checks the next event too, and
// asks each proof member's own questions as well (dryProofMembers).
func gateProofs(ctx context.Context, project store.Project, after reduce.Snapshot, packets []model.Packet, dry *dryRun) error {
	// One intake inventory for every check below, read only if one needs it.
	intake := newPendingIntake(project)
	for _, packet := range packets {
		for _, raw := range packet.Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				return err
			}
			switch e := event.(type) {
			case *model.InvocationSeal:
				err = gatePendingRealSeal(intake, after, e.Envelope)
			case *model.ProofAdmit:
				err = gateProofFamily(ctx, project, after, intake, e)
				dry.proofMembers(ctx, project, after, intake, e)
			case *model.TaskClose:
				err = gateClosureEffective(after, e)
			}
			if err := dry.note("proofs", err); err != nil {
				return err
			}
		}
	}
	return nil
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
