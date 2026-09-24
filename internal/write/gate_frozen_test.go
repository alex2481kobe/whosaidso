package write

// Criterion freezing against the capture time: a run's criterion
// must be admitted before intake captured the run's start, the authored
// started_at may not claim a start after that capture, and review.admit
// records each packet's capture time so the check replays from the ledger.
// Proof family rules live in gate_proof_test.go and gate_family_test.go.

import (
	"testing"
	"time"

	"whosaidso/internal/model"
	"whosaidso/internal/store"
)

// A start captured before its criterion existed is refused even when its
// authored started_at is honest, and a start whose started_at claims a time
// after its own capture is refused whatever criterion it names.
func TestCriterionFrozenAgainstCapture(t *testing.T) {
	for _, route := range []string{"control", "captured-before-criterion", "dated-after-capture", "dated-without-criterion"} {
		t.Run(route, func(t *testing.T) {
			w := newProofWorld(t, true)
			code := ""
			criterion := w.criterion
			env := w.envelope(criterion)
			switch route {
			case "captured-before-criterion":
				fix := w.fixEvent(w.claim)
				criterion = model.CriterionRef{Claim: w.claim, CriterionID: fix.CriterionID, Revision: fix.Revision}
				env = w.envelope(criterion)
				start := w.f.capture(nil, &model.InvocationStart{Envelope: env})
				time.Sleep(2 * time.Millisecond)
				w.f.accept(w.f.capture(nil, fix))
				w.f.refuse(w.f.request(start), "criterion-not-frozen")
				return
			case "dated-after-capture":
				env.StartedAt = env.StartedAt.Add(time.Hour)
				code = "start-after-capture"
			case "dated-without-criterion":
				env.CriterionRef = proofUnknown[model.CriterionRef]("exploratory run")
				env.StartedAt = env.StartedAt.Add(time.Hour)
				code = "start-after-capture"
			}
			start := w.f.capture(nil, &model.InvocationStart{Envelope: env})
			if code != "" {
				w.f.refuse(w.f.request(start), code)
				return
			}
			bundle := w.f.accept(start)
			packets, err := store.ReadIntake(w.f.project, []model.ID{start.CommandID})
			if err != nil {
				t.Fatal(err)
			}
			event, err := model.DecodeEvent(bundle.Events[len(bundle.Events)-1])
			if err != nil {
				t.Fatal(err)
			}
			review := event.(*model.ReviewAdmit)
			if got := review.CapturedAt[start.CommandID]; got.State != model.Known || !got.Value.Equal(packets[0].CapturedAt) || len(review.CapturedAt) != 1 {
				t.Fatalf("review recorded capture %v, intake stamped %v", review.CapturedAt, packets[0].CapturedAt)
			}
		})
	}
}
