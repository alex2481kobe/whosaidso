// The open-hold success closure (review final-2 adoption review 2026-09-24,
// finding 1) belongs here: a task.close with outcome success while a hold on
// the task is open, through admission, the admission dry run and ledger-only
// replay, and how show and continue display a hold a non-success close left
// open. Other acceptance specifications do not.
package acceptance_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
)

// openHoldWorld is a task whose attempt handed back success and which then got
// an open awaiting-acceptance hold: the documented "withhold acceptance" recipe.
type openHoldWorld struct {
	*flowWorld
	subject  flowTask
	hold     *model.BlockerHold
	witness  model.ArtifactRef
	delivery model.ArtifactRef
}

func openHoldNew(t *testing.T) *openHoldWorld {
	t.Helper()
	w := &openHoldWorld{flowWorld: flowNew(t)}
	w.subject = w.task("deliver the report")
	delivery := []byte(`{"delivered":"report v1"}`)
	w.put("delivery/report.json", delivery)
	w.delivery = pvPin(delivery, "delivery/report.json")
	refs, err := json.Marshal([]model.ArtifactRef{w.delivery})
	if err != nil {
		t.Fatal(err)
	}
	w.put("fixhold-delivery.json", refs)
	packet, err := w.handback(w.subject.attempt, "success", "--delivery-refs", filepath.Join(w.root, "fixhold-delivery.json"))
	if err != nil {
		t.Fatalf("control: a success receipt captures: %v", err)
	}
	if err := w.review("accepted", packet); err != nil {
		t.Fatalf("control: a success receipt admits: %v", err)
	}
	accepted := []byte(`{"accepted":"report v1 meets the criterion"}`)
	w.put("accept/report.json", accepted)
	w.witness = pvPin(accepted, "accept/report.json")
	w.hold = &model.BlockerHold{Task: w.subject.ref, BlockerID: w.id(), Reason: model.BlockerAwaitingAcceptance,
		Actor: model.Actor{ID: "reviewer"}, Criterion: "not accepted until the reviewer has read report v1"}
	w.mustAdmit("reviewer", w.hold)
	return w
}

func (w *openHoldWorld) close(outcome model.ClosureOutcome) *model.TaskClose {
	c := &model.TaskClose{Task: w.subject.ref, Outcome: outcome,
		AcceptanceWitnessRefs: []model.AcceptanceWitness{}, DeliveryWitnessRefs: []model.ArtifactRef{}}
	if outcome == model.ClosureSuccess {
		c.AcceptanceWitnessRefs = []model.AcceptanceWitness{{CriterionID: w.subject.spec.AcceptanceCriteria[0].ID, CriterionRevision: 1, WitnessRef: w.witness}}
		c.DeliveryWitnessRefs = []model.ArtifactRef{w.delivery}
	}
	return c
}

func (w *openHoldWorld) clear() *model.BlockerClear {
	return &model.BlockerClear{Task: w.subject.ref, BlockerID: w.hold.BlockerID,
		HoldRef: model.BlockerRef{Task: w.subject.ref, BlockerID: w.hold.BlockerID}, ResolvingWitness: w.witness}
}

// dryRun is `whosaidso check admission --packet P`: its verdict must be the
// one admission then gives.
func (w *openHoldWorld) dryRun(packet model.ID) (result string, reasons []any) {
	w.t.Helper()
	out, _ := w.cli(nil, "check", "admission", "--json", "--actor", "coordinator", "--packet", string(packet))
	a := flowDecode(w.t, out)
	return flowStr(a, "result"), flowList(a, "reasons")
}

func TestCloseOpenHoldSuccessRefusedWhileAHoldIsOpen(t *testing.T) {
	t.Parallel()
	w := openHoldNew(t)
	if got := flowStr(w.record(w.subject.ref.RecordID), "task", "status"); got != "BLOCKED" {
		t.Fatalf("control: an open hold must leave the task BLOCKED, got %s", got)
	}
	packet := w.capture("coordinator", w.close(model.ClosureSuccess))
	result, reasons := w.dryRun(packet)
	joined, _ := json.Marshal(reasons)
	if result != "would-refuse" {
		t.Errorf("expected check admission to refuse a success close over open hold %s; got %s", w.hold.BlockerID, result)
	}
	for _, want := range []string{string(w.hold.BlockerID), "(awaiting-acceptance)", "waits on reviewer"} {
		if !strings.Contains(string(joined), want) {
			t.Errorf("the refusal must name the open hold (id, reason, who it waits on); %q missing from %s", want, joined)
		}
	}
	w.refused("task.close success over an open hold", func() error { return w.review("accepted", packet) })
	if got := flowStr(w.record(w.subject.ref.RecordID), "task", "status"); got != "BLOCKED" {
		t.Errorf("the refused success close left the task %s; it must stay BLOCKED on its hold", got)
	}
	// While the task is open its hold is a blocked: line; open hold: is only
	// for a CLOSED task, so the same hold is never printed twice.
	text, err := w.cli(nil, "show", string(w.subject.ref.RecordID))
	if err != nil || strings.Contains(string(text), "open hold:") || !strings.Contains(string(text), "hold "+string(w.hold.BlockerID)) {
		t.Errorf("show of the BLOCKED task must list hold %s once, as blocked:, not as open hold: (%v):\n%s", w.hold.BlockerID, err, text)
	}
}

// A clear earlier in the same packet counts; a clear after the close does not.
func TestCloseOpenHoldClearInTheSameBundle(t *testing.T) {
	t.Parallel()
	t.Run("clear-then-close", func(t *testing.T) {
		w := openHoldNew(t)
		packet := w.capture("coordinator", w.clear(), w.close(model.ClosureSuccess))
		if result, reasons := w.dryRun(packet); result != "would-admit" {
			t.Fatalf("control: check admission must admit a clear followed by the close: %s %v", result, reasons)
		}
		if err := w.review("accepted", packet); err != nil {
			t.Fatalf("control: clearing the hold earlier in the same bundle must let success close: %v", err)
		}
		r := w.record(w.subject.ref.RecordID)
		if flowStr(r, "task", "status") != "CLOSED" || flowStr(r, "task", "outcome") != "success" {
			t.Errorf("expected CLOSED success, got %s %s", flowStr(r, "task", "status"), flowStr(r, "task", "outcome"))
		}
	})
	t.Run("close-then-clear", func(t *testing.T) {
		w := openHoldNew(t)
		packet := w.capture("coordinator", w.close(model.ClosureSuccess), w.clear())
		if result, _ := w.dryRun(packet); result != "would-refuse" {
			t.Errorf("expected check admission to refuse a close before the clear that follows it; got %s", result)
		}
		w.refused("task.close success before its hold's clear", func() error { return w.review("accepted", packet) })
	})
}

// Abandoning held work stays possible, and the hold it left open is shown.
func TestCloseOpenHoldNonSuccessShowsTheOpenHold(t *testing.T) {
	t.Parallel()
	for _, outcome := range []model.ClosureOutcome{model.ClosureCancelled, model.ClosureWithdrawn} {
		t.Run(string(outcome), func(t *testing.T) {
			w := openHoldNew(t)
			packet := w.capture("coordinator", w.close(outcome))
			if result, reasons := w.dryRun(packet); result != "would-admit" {
				t.Fatalf("control: check admission must admit a %s close over an open hold: %s %v", outcome, result, reasons)
			}
			if err := w.review("accepted", packet); err != nil {
				t.Fatalf("a %s close over an open hold must admit: %v", outcome, err)
			}
			r := w.record(w.subject.ref.RecordID)
			if flowStr(r, "task", "status") != "CLOSED" || flowStr(r, "task", "outcome") != string(outcome) {
				t.Errorf("expected CLOSED %s, got %s %s", outcome, flowStr(r, "task", "status"), flowStr(r, "task", "outcome"))
			}
			if flowStr(r, "task", "blockers", 0, "key", "blocker") != string(w.hold.BlockerID) || flowGet(r, "task", "blockers", 0, "cleared") != nil {
				t.Errorf("the record must keep hold %s uncleared, got %v", w.hold.BlockerID, flowGet(r, "task", "blockers"))
			}
			for _, view := range []string{"show", "continue"} {
				text, err := w.cli(nil, view, string(w.subject.ref.RecordID))
				if err != nil {
					t.Fatal(err)
				}
				line := "open hold: " + string(w.hold.BlockerID) + " awaiting-acceptance waits on reviewer"
				if !strings.Contains(string(text), line) {
					t.Errorf("%s of a %s task must list the hold its close left open (%q); got:\n%s", view, outcome, line, text)
				}
			}
			// Brief and JSON agree (continue's observed_at differs per run, so show only).
			w.read("show", string(w.subject.ref.RecordID))
		})
	}
}

// Replay decides from the ledger alone: the same shapes as hand-built bundles.
func TestCloseOpenHoldReplay(t *testing.T) {
	create := reduceCreate(1, reduceSpec(1))
	task := reduceRef(1, 1)
	hold := &model.BlockerHold{Task: task, BlockerID: reduceID(70), Reason: model.BlockerAwaitingAcceptance,
		Actor: model.Actor{ID: "reviewer"}, Criterion: "not accepted until the reviewer has read it"}
	clear := &model.BlockerClear{Task: task, BlockerID: hold.BlockerID,
		HoldRef: model.BlockerRef{Task: task, BlockerID: hold.BlockerID}, ResolvingWitness: reduceArtifact("acceptance")}
	second := &model.BlockerHold{Task: task, BlockerID: reduceID(71), Reason: model.BlockerResume,
		Actor: model.Actor{UnknownReason: "no owner named yet"}, Criterion: "resume once someone rules"}
	first := reduceBundle(t, model.Bundle{}, create, hold)
	one, both := []model.ID{hold.BlockerID}, []model.ID{hold.BlockerID, second.BlockerID}
	cases := []struct {
		name   string
		events []model.TypedEvent
		names  []model.ID // the open holds a refusal must name; none: admits
	}{
		{"success over the open hold", []model.TypedEvent{reduceClose(1, 1, 1, model.ClosureSuccess)}, one},
		{"success over two open holds", []model.TypedEvent{second, reduceClose(1, 1, 1, model.ClosureSuccess)}, both},
		{"close then clear", []model.TypedEvent{reduceClose(1, 1, 1, model.ClosureSuccess), clear}, one},
		{"clear then close", []model.TypedEvent{clear, reduceClose(1, 1, 1, model.ClosureSuccess)}, nil},
		{"cancelled over the open hold", []model.TypedEvent{reduceClose(1, 1, 1, model.ClosureCancelled)}, nil},
		{"withdrawn over the open hold", []model.TypedEvent{reduceClose(1, 1, 1, model.ClosureWithdrawn)}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := reduce.Replay([]model.Bundle{first, reduceBundle(t, first, tc.events...)})
			if len(tc.names) == 0 {
				if err != nil {
					t.Errorf("control: replay must admit; got %v", err)
				}
				return
			}
			if recCode(err) != reduce.CodeInvalidTransition {
				t.Fatalf("expected replay to refuse the success close over open holds %v, as admission does; got %v", tc.names, err)
			}
			for _, id := range tc.names {
				if !strings.Contains(err.Error(), string(id)) {
					t.Errorf("the refusal must name every open hold; %s missing from %v", id, err)
				}
			}
			if len(tc.names) == 2 && !strings.Contains(err.Error(), "waits on UNKNOWN: no owner named yet") {
				t.Errorf("a hold waiting on an unknown actor must say so, never a blank: %v", err)
			}
		})
	}
}
