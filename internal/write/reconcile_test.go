package write

// Tests for dead-runner reconciliation: an admitted start with no seal stops
// blocking its criterion only through an attributable UNKNOWN-outcome seal that
// carries no reading, and that member can never support a proof.

import (
	"context"
	"strings"
	"testing"
	"time"

	"datum/internal/evidence"
	"datum/internal/model"
	"datum/internal/reduce"
)

// deadRun admits a passing run and a start whose observer never sealed it.
func deadRun(t *testing.T) (*proofWorld, model.InvocationRef, model.InvocationRef, model.InvocationEnvelope) {
	t.Helper()
	w := newProofWorld(t, true)
	pass, s, e := w.run(w.criterion, proofPass)
	env := w.envelope(w.criterion)
	w.f.accept(s, e, w.f.capture(nil, &model.InvocationStart{Envelope: env}))
	return w, pass, model.InvocationRef{Project: w.f.project.ID, InvocationID: env.InvocationID}, env
}

func TestDeadRunnerReconciledStopsBlockingWithoutAReading(t *testing.T) {
	w, pass, dead, _ := deadRun(t)
	proof := func(disposition string) model.PacketRef {
		return w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{pass: "supports", dead: disposition}))
	}
	w.f.refuse(w.f.request(proof("inconclusive")), "invalid-transition")
	ref, err := Reconcile(context.Background(), w.f.project, ReconcileRequest{Author: w.f.author, InvocationID: dead.InvocationID, Reason: "runner host lost power"})
	if err != nil {
		t.Fatal(err)
	}
	w.f.accept(ref)
	inv, _ := w.f.snapshot().Invocation(reduce.InvocationKey{Project: dead.Project, InvocationID: dead.InvocationID})
	if inv.Seal == nil || inv.Seal.Outcome.State != model.Unknown || inv.Seal.OutputRefs.State != model.Unknown || !strings.Contains(inv.Seal.Outcome.Reason, w.f.author.ID) {
		t.Fatalf("reconciliation must record an attributed UNKNOWN terminal observation: %+v", inv.Seal)
	}
	// A reconciled run never supports: it produced nothing.
	w.f.refuse(w.f.request(proof("supports")), "invalid-transition")
	w.f.accept(proof("inconclusive"))
	if w.status(t) != reduce.StatusProven {
		t.Fatal("the reconciled family did not prove on its completed member")
	}
}

func TestReconciliationSealCannotCarryAReading(t *testing.T) {
	for _, field := range []string{"output_refs", "observed_at", "config_effective", "conditions_observed", "unknown-author"} {
		t.Run(field, func(t *testing.T) {
			w, _, _, env := deadRun(t)
			seal := proofSealed(env, proofPass)
			seal.Envelope.Outcome = proofUnknown[model.ProcessOutcome]("observer died")
			unknownMap := proofUnknown[map[string]model.Availability[model.Scalar]]("observer died")
			seal.Envelope.ObservedAt, seal.Envelope.OutputRefs = proofUnknown[time.Time]("observer died"), proofUnknown[[]model.ArtifactRef]("observer died")
			seal.Envelope.ConfigEffective, seal.Envelope.ConditionsObserved = unknownMap, unknownMap
			code := "reconciliation-reading"
			switch field {
			case "output_refs":
				seal.Envelope.OutputRefs = proofKnown([]model.ArtifactRef{proofPin(proofPass, evidence.RunDir(env.InvocationID)+"/"+proofPath)})
			case "observed_at":
				seal.Envelope.ObservedAt = proofKnown(env.StartedAt)
			case "config_effective":
				seal.Envelope.ConfigEffective = proofKnown(map[string]model.Availability[model.Scalar]{})
			case "conditions_observed":
				seal.Envelope.ConditionsObserved = proofKnown(map[string]model.Availability[model.Scalar]{})
			case "unknown-author":
				w.f.author, code = model.Actor{UnknownReason: "not recorded"}, "attribution-unknown"
			}
			w.f.refuse(w.f.request(w.f.capture([][]byte{[]byte(proofPass)}, seal)), code)
		})
	}
}

func TestReconcileRefusesWhatIsNotADeadRunner(t *testing.T) {
	w, pass, dead, env := deadRun(t)
	ctx := context.Background()
	for name, r := range map[string]ReconcileRequest{
		"unknown-author": {Author: model.Actor{UnknownReason: "not recorded"}, InvocationID: dead.InvocationID, Reason: "lost"},
		"no-reason":      {Author: w.f.author, InvocationID: dead.InvocationID, Reason: " "},
		"already-sealed": {Author: w.f.author, InvocationID: pass.InvocationID, Reason: "lost"},
		"not-admitted":   {Author: w.f.author, InvocationID: w.f.id(), Reason: "lost"},
	} {
		if _, err := Reconcile(ctx, w.f.project, r); err == nil {
			t.Errorf("%s: reconcile was not refused", name)
		}
	}
	// A real seal waiting in intake must be admitted, not papered over.
	w.f.capture([][]byte{[]byte(proofFail)}, proofSealed(env, proofFail))
	if _, err := Reconcile(ctx, w.f.project, ReconcileRequest{Author: w.f.author, InvocationID: dead.InvocationID, Reason: "lost"}); err == nil || !strings.Contains(err.Error(), "pending") {
		t.Fatalf("reconcile hid a pending real seal: %v", err)
	}
}

// handUnknown is an UNKNOWN-outcome seal captured by hand, with no reading.
func handUnknown(env model.InvocationEnvelope) *model.InvocationSeal {
	return &model.InvocationSeal{StartRef: model.InvocationRef{Project: env.ExecutionSourceIdentity.Project, InvocationID: env.InvocationID}, Envelope: env}
}

// The pending-real-seal rule is enforced at admission, not only by Reconcile:
// a slow runner's seal can arrive after reconciliation was captured, and an
// UNKNOWN seal can be captured by hand.
func TestUnknownSealRefusedWhileARealSealIsPending(t *testing.T) {
	for _, route := range []string{"reconcile-then-real-seal", "hand-captured", "other-unknown-pending", "real-seal-rejected"} {
		t.Run(route, func(t *testing.T) {
			w, _, dead, env := deadRun(t)
			var unknown model.PacketRef
			if route == "reconcile-then-real-seal" {
				ref, err := Reconcile(context.Background(), w.f.project, ReconcileRequest{Author: w.f.author, InvocationID: dead.InvocationID, Reason: "presumed dead"})
				if err != nil {
					t.Fatal(err)
				}
				unknown = ref
			} else {
				unknown = w.f.capture(nil, handUnknown(env))
			}
			switch route {
			case "other-unknown-pending":
				// Another UNKNOWN seal is not a real observation and blocks nothing.
				w.f.capture(nil, handUnknown(env))
				w.f.accept(unknown)
				return
			case "real-seal-rejected":
				real := w.f.request(w.f.capture([][]byte{[]byte(proofFail)}, proofSealed(env, proofFail)))
				real.Outcome = "rejected"
				if _, err := Admit(context.Background(), w.f.project, real); err != nil {
					t.Fatal(err)
				}
				w.f.accept(unknown)
				return
			}
			real := w.f.capture([][]byte{[]byte(proofFail)}, proofSealed(env, proofFail))
			w.f.refuse(w.f.request(unknown), "pending-real-seal")
			// The real observation is still admissible and becomes the seal.
			w.f.accept(real)
			inv, _ := w.f.snapshot().Invocation(reduce.InvocationKey{Project: dead.Project, InvocationID: dead.InvocationID})
			if inv.Seal == nil || inv.Seal.Outcome.State != model.Known {
				t.Fatalf("the real seal was shut out: %+v", inv.Seal)
			}
		})
	}
}
