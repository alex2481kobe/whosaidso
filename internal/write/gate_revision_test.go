package write

// Proof family tests for criterion revisions: re-fixing a criterion cannot
// erase known counterevidence, so a proof under revision 2 must account for
// runs under revision 1, as inapplicable or inconclusive and never as support,
// and the gate never evaluates them against the new revision. Rejected runs
// live in gate_rejected_test.go.

import (
	"testing"

	"datum/internal/model"
	"datum/internal/reduce"
)

// reviseCriterion admits revision 2 of the world's criterion and returns it.
func (w *proofWorld) reviseCriterion() model.CriterionRef {
	fix := w.fixEvent(w.claim)
	fix.CriterionID, fix.Revision = w.criterion.CriterionID, 2
	w.f.accept(w.f.capture(nil, fix))
	return model.CriterionRef{Claim: w.claim, CriterionID: fix.CriterionID, Revision: 2}
}

func TestProofUnderANewCriterionRevisionAccountsForEarlierRuns(t *testing.T) {
	w := newProofWorld(t, true)
	failed, s1, e1 := w.run(w.criterion, proofFail)
	w.f.accept(s1, e1)
	rev2 := w.reviseCriterion()
	passed, s2, e2 := w.run(rev2, proofPass)
	w.f.accept(s2, e2)
	for _, disposition := range []string{"omitted", "supports", "contradicts"} {
		members := map[model.InvocationRef]string{passed: "supports", failed: disposition}
		if disposition == "omitted" {
			delete(members, failed)
		}
		w.f.refuse(w.f.request(w.f.capture(nil, w.proof(rev2, members))), "invalid-transition")
	}
	if w.status(t) != reduce.StatusMeasured {
		t.Fatal("re-fixing the criterion erased the failing run")
	}
	// Accounted for, and never evaluated against revision 2 as counterevidence.
	w.f.accept(w.f.capture(nil, w.proof(rev2, map[model.InvocationRef]string{passed: "supports", failed: "inapplicable"})))
	if w.status(t) != reduce.StatusProven {
		t.Fatal("a new revision with its earlier run dispositioned did not prove")
	}
}

func TestProofUnderANewRevisionWaitsForPendingEarlierRuns(t *testing.T) {
	w := newProofWorld(t, true)
	rev2 := w.reviseCriterion()
	passed, s, e := w.run(rev2, proofPass)
	w.f.accept(s, e)
	// A run under revision 1 waits unreviewed in intake.
	w.run(w.criterion, proofFail)
	w.f.refuse(w.f.request(w.f.capture(nil, w.proof(rev2, map[model.InvocationRef]string{passed: "supports"}))), "pending-reconciliation")
}
