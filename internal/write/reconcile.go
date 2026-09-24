package write

// Reconciliation of a dead runner lives here: an attributable seal recording
// that the invocation's terminal observation is UNKNOWN, built from the admitted
// start and carrying no reading, and the admission rule that refuses such a
// seal while a real one is pending. The gate rule that refuses any
// UNKNOWN-outcome seal carrying an observation lives in gate_proof.go. Run and
// its real seal do not.

import (
	"context"
	"fmt"
	"time"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
)

// ReconcileRequest names an admitted, unsealed invocation whose observer did
// not survive to seal it, and who is reconciling it and why.
type ReconcileRequest struct {
	Author       model.Actor
	InvocationID model.ID
	Reason       string
}

// Reconcile captures (never admits) a seal whose outcome and every observed
// field are UNKNOWN. Admitting it ends the block on the invocation's criterion
// family without claiming the process produced anything.
func Reconcile(ctx context.Context, project store.Project, r ReconcileRequest) (model.PacketRef, error) {
	if model.Blank(r.Author.ID) || model.Blank(r.Reason) {
		return model.PacketRef{}, fmt.Errorf("reconcile: an identified author and a reason are required")
	}
	loaded, err := store.Load(project)
	if err != nil {
		return model.PacketRef{}, err
	}
	snapshot := loaded.Snapshot()
	inv, ok := snapshot.Invocation(reduce.InvocationKey{Project: project.ID, InvocationID: r.InvocationID})
	if !ok {
		return model.PacketRef{}, fmt.Errorf("reconcile: invocation %s has no admitted start", r.InvocationID)
	}
	if inv.Seal != nil {
		return model.PacketRef{}, fmt.Errorf("reconcile: invocation %s is already sealed", r.InvocationID)
	}
	intake, err := store.ReadIntake(project, nil)
	if err != nil {
		return model.PacketRef{}, err
	}
	for _, packet := range intake {
		if _, reviewed := snapshot.Review(reduce.ReviewKey{Project: project.ID, CommandID: packet.CommandID}); reviewed {
			continue
		}
		for _, raw := range packet.Events {
			if event, err := model.DecodeEvent(raw); err == nil {
				if seal, ok := event.(*model.InvocationSeal); ok && seal.Envelope.InvocationID == r.InvocationID {
					return model.PacketRef{}, fmt.Errorf("reconcile: a real seal for %s is pending in intake packet %s; admit it instead", r.InvocationID, packet.CommandID)
				}
			}
		}
	}
	why := fmt.Sprintf("observer did not survive to seal; reconciled by %s: %s", r.Author.ID, r.Reason)
	env := inv.Start
	env.Outcome = model.Availability[model.ProcessOutcome]{State: model.Unknown, Reason: why}
	env.ObservedAt = model.Availability[time.Time]{State: model.Unknown, Reason: why}
	env.OutputRefs = model.Availability[[]model.ArtifactRef]{State: model.Unknown, Reason: why}
	env.ConfigEffective = model.Availability[map[string]model.Availability[model.Scalar]]{State: model.Unknown, Reason: why}
	env.ConditionsObserved = model.Availability[map[string]model.Availability[model.Scalar]]{State: model.Unknown, Reason: why}
	env.Isolation = model.Availability[model.Isolation]{State: model.Unknown, Reason: why}
	env.Visual = model.Availability[model.VisualObservation]{State: model.Unknown, Reason: why}
	event, err := model.EncodeEvent(&model.InvocationSeal{StartRef: model.InvocationRef{Project: project.ID, InvocationID: r.InvocationID}, Envelope: env})
	if err != nil {
		return model.PacketRef{}, err
	}
	return store.WriteIntake(ctx, project, store.IntakeRequest{Author: r.Author, Events: []model.Event{event}})
}

// gatePendingRealSeal: an UNKNOWN-outcome seal records that nobody observed the
// end of the run. It is refused at admission while a seal with a known outcome
// for the same invocation waits unreviewed in intake, since admitting it would
// shut the real observation out as already sealed. Reconcile checks this when
// it captures; the gate repeats it because the real seal can arrive later and
// an UNKNOWN seal can be captured by hand. Packets reviewed in this admission
// set are not pending. The intake read is the admission's one shared
// inventory (pendingIntake, gate_family.go).
func gatePendingRealSeal(intake *pendingIntake, after reduce.Snapshot, env model.InvocationEnvelope) error {
	if env.Outcome.State != model.Unknown {
		return nil
	}
	packets, err := intake.all()
	if err != nil {
		return err
	}
	for _, packet := range packets {
		if _, reviewed := after.Review(reduce.ReviewKey{Project: intake.project.ID, CommandID: packet.CommandID}); reviewed {
			continue
		}
		for _, raw := range packet.Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				continue
			}
			if seal, ok := event.(*model.InvocationSeal); ok && seal.Envelope.InvocationID == env.InvocationID && seal.Envelope.Outcome.State != model.Unknown {
				return admissionFault("pending-real-seal", "intake/"+string(packet.CommandID),
					fmt.Sprintf("a seal with a known outcome for %s is pending review; review it before admitting an UNKNOWN outcome", env.InvocationID))
			}
		}
	}
	return nil
}
