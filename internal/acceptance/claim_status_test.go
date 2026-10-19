package acceptance_test

// whosaidso check claim through the built binary: a takeover replaces the
// prior claim (TAKEN-OVER, exit 1), an admitted receipt ends a claim (ENDED,
// exit 1), and only a claim nothing replaced or ended is HELD (exit 0). The
// lost-at-a-merge case is internal/query/claim_test.go's and the many-machine
// test's.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/query"
)

func (w *flowWorld) attemptClaim(attempt model.ID) (string, bool) {
	w.t.Helper()
	out, err := w.cli(nil, "check", "claim", "--json", string(attempt))
	var a query.ClaimAnswer
	if jerr := json.Unmarshal(out, &a); jerr != nil {
		w.t.Fatalf("check claim --json printed %q (%v): %v", out, err, jerr)
	}
	return a.Status, err == nil
}

func TestCheckClaimTakenOverAndEnded(t *testing.T) {
	t.Parallel()
	w := flowNew(t)
	task := w.task("hand the work over")
	if status, ok := w.attemptClaim(task.attempt); status != query.ClaimHeld || !ok {
		t.Fatalf("control: a fresh claim is HELD with exit 0, got %s ok=%v", status, ok)
	}
	stopped := []byte(`{"stopped":"the first agent confirmed it stopped"}`)
	w.put("confirmations/stopped.json", stopped)
	second := w.id()
	w.mustAdmit("agent2", &model.TaskTakeover{Task: task.ref, Actor: model.Actor{ID: "agent2"}, AttemptID: second, PriorAttemptID: task.attempt,
		StoppedConfirmationRef: pvPin(stopped, "confirmations/stopped.json")})
	if status, ok := w.attemptClaim(task.attempt); status != query.ClaimTakenOver || ok {
		t.Fatalf("a claim a takeover replaced is TAKEN-OVER with a nonzero exit, got %s ok=%v", status, ok)
	}
	if status, ok := w.attemptClaim(second); status != query.ClaimHeld || !ok {
		t.Fatalf("the takeover's claim is HELD, got %s ok=%v", status, ok)
	}
	stop, err := w.handback(task.attempt, "stopped")
	if err != nil || w.review("accepted", stop) != nil {
		t.Fatalf("control: the prior holder's receipt admits: %v", err)
	}
	if status, ok := w.attemptClaim(task.attempt); status != query.ClaimEnded || ok {
		t.Fatalf("a claim with an admitted receipt is ENDED with a nonzero exit, got %s ok=%v", status, ok)
	}
	if status, ok := w.attemptClaim(w.id()); status != query.ClaimUnknown || ok {
		t.Fatalf("an attempt id no claim minted is UNKNOWN, got %s", status)
	}
	out, _ := w.cli(nil, "check", "claim", string(task.attempt))
	if !strings.Contains(string(out), "ENDED") || !strings.Contains(string(out), "next:") {
		t.Fatalf("the text answer names the status and the next step: %s", out)
	}
}
