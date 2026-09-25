package reduce

// Tests that a stopped handback finds its bundled hold by the hold's identity,
// the task record id, whichever task revision the hold and the receipt name
// across an amendment in the same bundle; the hold restrictions still apply.
// The single-revision table is in handback_test.go.

import (
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestStoppedHandbackFindsItsHoldAcrossAmendment(t *testing.T) {
	hold := func(task model.ID, rev model.Revision) *model.BlockerHold {
		return &model.BlockerHold{Task: ref(task, rev), BlockerID: newID("HDA1"), Reason: model.BlockerResume,
			Actor: model.Actor{ID: "agent-b"}, Criterion: "the owner rules on the boundary"}
	}
	receipt := func(outcome model.AttemptOutcome, rev model.Revision) *model.AttemptTerminal {
		r := receiptATTA(outcome)
		r.Task.Revision = rev
		return r
	}
	amend := &model.TaskAmend{Provenance: provenance("agent-a"), Target: ref(newID("TSKA"), 1), Replacement: taskSpec()}
	blocked := model.AttemptBlockedMidTask
	elsewhere := hold(newID("TSKA"), 2)
	elsewhere.Task.Project = "test/elsewhere"
	for _, tc := range []struct {
		name   string
		events []model.TypedEvent
		code   string
	}{
		{"control: hold on r1, amend, receipt on r2", []model.TypedEvent{hold(newID("TSKA"), 1), amend, receipt(blocked, 2)}, ""},
		{"control: receipt on r1, amend, hold on r2", []model.TypedEvent{receipt(blocked, 1), amend, hold(newID("TSKA"), 2)}, ""},
		{"the hold is another task's", []model.TypedEvent{hold(newID("TSKB"), 1), amend, receipt(blocked, 2)}, CodeMissingHold},
		{"the hold is the same record id in another project", []model.TypedEvent{receipt(blocked, 1), amend, elsewhere}, CodeMissingHold},
		{"the bundle clears the hold", []model.TypedEvent{hold(newID("TSKA"), 1), amend, receipt(blocked, 2), clearAt(newID("TSKA"), 2, newID("HDA1"))}, CodeMissingHold},
		{"out of scope still may not amend its task", []model.TypedEvent{hold(newID("TSKA"), 1), amend, receipt(model.AttemptOutOfScope, 2)}, CodeInvalidTransition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := handbackLedger(t, model.Actor{ID: "agent-a"})
			l.add(t, &model.TaskCreate{Provenance: provenance("agent-a"), ID: newID("TSKB"), Spec: taskSpec()})
			authors := make([]string, len(tc.events))
			for i := range authors {
				authors[i] = "agent-a"
			}
			l.add(t, authored(l.seq+1, tc.events, authors...)...)
			wantBoth(t, l, tc.code)
		})
	}
}
