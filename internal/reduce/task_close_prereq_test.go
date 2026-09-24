package reduce

// Tests for the success-closure prerequisite rule: a task.close with outcome
// success is refused unless every prerequisite is satisfied at the closure's
// ledger position. Every case is driven through Apply and through Replay,
// because a rule enforced at one entrance only is a rule the other one skips.
// Projection of prerequisites themselves is tested in task_test.go and
// prerequisites_test.go.

import (
	"strings"
	"testing"
	"time"

	"whosaidso/internal/model"
)

// closeBothWays appends the closure to l and folds it twice: once by Apply
// onto the replayed prefix, once by Replay of the whole ledger. It returns both
// errors and the Replay snapshot when that succeeded.
func closeBothWays(t *testing.T, l *ledgerBuilder, closure *model.TaskClose) (applyErr, replayErr error, s Snapshot) {
	t.Helper()
	prefix := mustReplay(t, l.bundles())
	b := l.add(t, closure)
	_, applyErr = Apply(prefix, b)
	s, replayErr = Replay(l.bundles())
	return applyErr, replayErr, s
}

func wantRefusedBothWays(t *testing.T, l *ledgerBuilder, closure *model.TaskClose) {
	t.Helper()
	applyErr, replayErr, _ := closeBothWays(t, l, closure)
	for name, err := range map[string]error{"Apply": applyErr, "Replay": replayErr} {
		f := wantFault(t, err, CodeInvalidTransition)
		if f.Path != "outcome" || !strings.Contains(f.Detail, "prerequisite") {
			t.Fatalf("%s refused for the wrong reason: %v", name, err)
		}
	}
}

func wantClosedBothWays(t *testing.T, l *ledgerBuilder, closure *model.TaskClose) {
	t.Helper()
	applyErr, replayErr, s := closeBothWays(t, l, closure)
	if applyErr != nil || replayErr != nil {
		t.Fatalf("closure refused: Apply %v, Replay %v", applyErr, replayErr)
	}
	p, _ := s.Task(ident(closure.Task))
	if p.Status != StatusClosed || p.Outcome != closure.Outcome {
		t.Fatalf("status %s outcome %s, want CLOSED %s (reasons %v)", p.Status, p.Outcome, closure.Outcome, reasonKinds(p))
	}
}

func consumer(opts ...taskOpt) *model.TaskCreate {
	return &model.TaskCreate{Provenance: provenance("lane-b"), ID: newID("TSKB"), Spec: taskSpec(opts...)}
}

func closeWith(outcome model.ClosureOutcome) *model.TaskClose {
	return &model.TaskClose{Task: ref(newID("TSKB"), 1), Outcome: outcome, Authority: closeAuthority(),
		AcceptanceWitnessRefs: []model.AcceptanceWitness{}, DeliveryWitnessRefs: []model.ArtifactRef{}}
}

// prereqCase builds a ledger where TSKB's single prerequisite of one kind is
// satisfied or not, and nothing else about TSKB differs.
type prereqCase struct {
	kind  string
	build func(t *testing.T, satisfied bool) *ledgerBuilder
}

func prereqCases() []prereqCase {
	return []prereqCase{
		{"task-success", func(t *testing.T, satisfied bool) *ledgerBuilder {
			l := goodLedger(t) // TSKA has a live attempt: not closed
			if satisfied {
				l = closedTaskLedger(t)
			}
			l.add(t, consumer(withPrerequisite("task-success", ref(newID("TSKA"), 1), "forbid", nil)))
			return l
		}},
		{"claim-proof", func(t *testing.T, satisfied bool) *ledgerBuilder {
			l := proofLedger(t, satisfied) // MEASURED unless satisfied
			l.add(t, consumer(withPrerequisite("claim-proof", ref(newID("CMA1"), 1), "forbid", nil)))
			return l
		}},
		{"decision-approved", func(t *testing.T, satisfied bool) *ledgerBuilder {
			d := ref(newID("DCSA"), 1)
			l := newLedger()
			l.add(t, &model.DecisionOpen{ID: d.RecordID, Provenance: provenance("author"), Spec: decisionSpec()})
			if satisfied {
				l.add(t, dispose(d, "approved"))
			}
			l.add(t, consumer(withPrerequisite("decision-approved", d, "forbid", nil)))
			return l
		}},
	}
}

func TestSuccessCloseRefusedWithAnUnsatisfiedPrerequisite(t *testing.T) {
	for _, tc := range prereqCases() {
		t.Run(tc.kind, func(t *testing.T) {
			// Control: the same closure on the same task closes once the
			// prerequisite is satisfied.
			wantClosedBothWays(t, tc.build(t, true), closeSuccess(newID("TSKB"), 1))
			wantRefusedBothWays(t, tc.build(t, false), closeSuccess(newID("TSKB"), 1))
		})
	}
}

// TestSuccessCloseNeedsEveryPrerequisite: one unmet prerequisite among
// satisfied ones is enough to refuse, and satisfying the last one admits it.
func TestSuccessCloseNeedsEveryPrerequisite(t *testing.T) {
	d := ref(newID("DCSA"), 1)
	build := func(approved bool) *ledgerBuilder {
		l := proofLedger(t, true)
		l.add(t, &model.DecisionOpen{ID: d.RecordID, Provenance: provenance("author"), Spec: decisionSpec()})
		if approved {
			l.add(t, dispose(d, "approved"))
		}
		l.add(t, consumer(
			withPrerequisite("claim-proof", ref(newID("CMA1"), 1), "forbid", nil),
			withPrerequisite("decision-approved", d, "forbid", nil),
		))
		return l
	}
	wantClosedBothWays(t, build(true), closeSuccess(newID("TSKB"), 1))
	wantRefusedBothWays(t, build(false), closeSuccess(newID("TSKB"), 1))
}

// TestNonSuccessCloseIgnoresPrerequisites: cancelling, withdrawing or waiving
// a task whose prerequisites never came true is exactly what those outcomes
// are for, so they keep their existing rules.
func TestNonSuccessCloseIgnoresPrerequisites(t *testing.T) {
	for _, tc := range prereqCases() {
		for _, outcome := range []model.ClosureOutcome{model.ClosureCancelled, model.ClosureWithdrawn, model.ClosureWaived} {
			t.Run(tc.kind+"/"+string(outcome), func(t *testing.T) {
				wantClosedBothWays(t, tc.build(t, false), closeWith(outcome))
			})
		}
	}
}

// TestSuccessCloseHonoursAPermittedWaiver: a waiver the consumer permits and
// cites authority for carries the prerequisite; the same unmet prerequisite
// under "forbid" still refuses. "allow" without authority is refused at decode.
func TestSuccessCloseHonoursAPermittedWaiver(t *testing.T) {
	auth := rulingAuthority("owner")
	build := func(policy string, a *model.Authority) *ledgerBuilder {
		l := goodLedger(t) // TSKA is live, so the dependency is FALSE
		l.add(t, consumer(withPrerequisite("task-success", ref(newID("TSKA"), 1), policy, a)))
		return l
	}
	wantClosedBothWays(t, build("allow-with-authority", &auth), closeSuccess(newID("TSKB"), 1))
	wantRefusedBothWays(t, build("forbid", nil), closeSuccess(newID("TSKB"), 1))
}

// TestSuccessCloseRefusesAPrerequisiteCycle: A waits on B and B waits on A, and
// A waits on itself. Neither can close as success first, and deciding that
// must terminate.
func TestSuccessCloseRefusesAPrerequisiteCycle(t *testing.T) {
	cycle := func(t *testing.T) *ledgerBuilder {
		l := newLedger()
		l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
		l.add(t, consumer(withPrerequisite("task-success", ref(newID("TSKA"), 1), "forbid", nil)))
		l.add(t, &model.TaskAmend{Provenance: provenance("coordinator"), Target: ref(newID("TSKA"), 1),
			Replacement: taskSpec(
				withPrerequisite("task-success", ref(newID("TSKB"), 1), "forbid", nil),
				withPrerequisite("task-success", ref(newID("TSKA"), 1), "forbid", nil),
			)})
		return l
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		t.Run("A", func(t *testing.T) { wantRefusedBothWays(t, cycle(t), closeSuccess(newID("TSKA"), 2)) })
		t.Run("B", func(t *testing.T) { wantRefusedBothWays(t, cycle(t), closeSuccess(newID("TSKB"), 1)) })
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("evaluating a prerequisite cycle did not terminate")
	}
}
