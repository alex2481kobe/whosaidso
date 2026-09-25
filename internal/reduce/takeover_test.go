package reduce

// The dispatch boundary of task.takeover lives here: displacing a live writer
// versus starting new work after the prior attempt is terminal. Takeover
// history retention lives in task_test.go.

import (
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func takeoverOf(prior string) *model.TaskTakeover {
	return &model.TaskTakeover{
		Task: ref(newID("TSKA"), 1), Actor: model.Actor{ID: "agent-b"},
		AttemptID: newID("ATTB"), PriorAttemptID: newID(prior),
		StoppedConfirmationRef: blobRef("prior-stopped"),
	}
}

func terminalATTA(outcome model.AttemptOutcome, reconciliationOwed bool) *model.AttemptTerminal {
	return &model.AttemptTerminal{
		Task: ref(newID("TSKA"), 1), AttemptID: newID("ATTA"), Outcome: outcome,
		Reason: "the prior attempt ended", NextAction: "dispatch again", DeliveryRefs: []model.ArtifactRef{},
		ReconciliationOwed: reconciliationOwed,
	}
}

func holdOnTSKA(reason model.BlockerReason) *model.BlockerHold {
	return &model.BlockerHold{
		Task: ref(newID("TSKA"), 1), BlockerID: newID("HDA1"), Reason: reason,
		Actor: model.Actor{ID: "coordinator"}, Criterion: "owner accepts before any further work",
	}
}

// takeoverRefused requires the same refusal through Apply and through Replay.
func takeoverRefused(t *testing.T, l *ledgerBuilder, event model.TypedEvent) {
	t.Helper()
	before := mustReplay(t, l.out)
	_, err := Apply(before, l.add(t, event))
	if f := wantFault(t, err, CodeInvalidTransition); f.Path != "task" {
		t.Fatalf("Apply refused at %q, want the task's dispatch rule: %v", f.Path, err)
	}
	_, err = Replay(l.out)
	wantFault(t, err, CodeInvalidTransition)
}

// TestTakeoverOfALiveAttemptIsNotDispatch: while an attempt is live the task
// is IN FLIGHT and READY/BLOCKED do not apply (contract statuses 6-8), so an
// open hold does not stop a confirmed-stopped writer from being displaced.
func TestTakeoverOfALiveAttemptIsNotDispatch(t *testing.T) {
	l := goodLedger(t) // ATTA is live
	l.add(t, holdOnTSKA(model.BlockerAwaitingAcceptance))
	l.add(t, takeoverOf("ATTA"))
	if p := projectTask(t, l, newID("TSKA")); p.Status != StatusInFlight {
		t.Fatalf("status = %q, want IN FLIGHT under the new writer", p.Status)
	}
}

// TestTakeoverAfterATerminalAttemptMeetsTheStartRule: once the prior attempt
// has a terminal receipt nobody is displaced, so the takeover is dispatch and
// must find the task READY, with BLOCKED winning, exactly as start does.
func TestTakeoverAfterATerminalAttemptMeetsTheStartRule(t *testing.T) {
	t.Run("control: READY after a stopped attempt", func(t *testing.T) {
		l := goodLedger(t)
		l.add(t, terminalATTA(model.AttemptStopped, false))
		if p := projectTask(t, l, newID("TSKA")); p.Status != StatusReady {
			t.Fatalf("control: status = %q, want READY (reasons %v)", p.Status, reasonKinds(p))
		}
		l.add(t, takeoverOf("ATTA"))
		mustReplay(t, l.out)
	})
	t.Run("acceptance hold", func(t *testing.T) {
		l := goodLedger(t)
		l.add(t, terminalATTA(model.AttemptStopped, false))
		l.add(t, holdOnTSKA(model.BlockerAwaitingAcceptance))
		takeoverRefused(t, l, takeoverOf("ATTA"))
	})
	t.Run("success awaiting acceptance", func(t *testing.T) {
		l := goodLedger(t)
		l.add(t, terminalATTA(model.AttemptSuccess, false))
		takeoverRefused(t, l, takeoverOf("ATTA"))
	})
	t.Run("reconciliation owed", func(t *testing.T) {
		l := goodLedger(t)
		l.add(t, terminalATTA(model.AttemptRunnerDied, true))
		takeoverRefused(t, l, takeoverOf("ATTA"))
	})
	t.Run("another attempt is live", func(t *testing.T) {
		// Naming the ended attempt while a different one is live displaces
		// nobody; the live writer is the one a takeover must name.
		l := goodLedger(t)
		l.add(t, terminalATTA(model.AttemptStopped, false))
		l.add(t, &model.TaskStart{Task: ref(newID("TSKA"), 1), Actor: model.Actor{ID: "agent-c"}, AttemptID: newID("ATTC")})
		takeoverRefused(t, l, takeoverOf("ATTA"))
	})
}
