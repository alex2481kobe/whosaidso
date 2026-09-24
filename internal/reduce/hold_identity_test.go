package reduce

// Tests for a hold's identity: a hold belongs to its task, found by (task id,
// blocker id) whatever task revision a reference names, so it survives an
// amendment and is cleared against the task's current revision. Every case is
// folded by Apply onto the prefix and by Replay of the whole ledger. Admission's
// side of the same rule is tested in internal/write/hold_identity_test.go.

import (
	"strings"
	"testing"

	"whosaidso/internal/model"
)

// heldThenAmended holds HDA1 on TSKA revision 1, then amends TSKA to revision 2.
func heldThenAmended(t *testing.T) *ledgerBuilder {
	l := newLedger()
	l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
	l.add(t, &model.BlockerHold{Task: ref(newID("TSKA"), 1), BlockerID: newID("HDA1"),
		Reason: model.BlockerAwaitingAcceptance, Actor: model.Actor{ID: "owner"}, Criterion: "owner reads the delivery"})
	l.add(t, &model.TaskAmend{Provenance: provenance("lane-a"), Target: ref(newID("TSKA"), 1), Replacement: taskSpec()})
	return l
}

func clearAt(task model.ID, rev model.Revision, blocker model.ID) *model.BlockerClear {
	r := ref(task, rev)
	return &model.BlockerClear{Task: r, BlockerID: blocker, HoldRef: model.BlockerRef{Task: r, BlockerID: blocker}, ResolvingWitness: blobRef("delivery-read")}
}

func successAt(rev model.Revision) *model.TaskClose { return closeSuccess(newID("TSKA"), rev) }

// foldBothWays appends events as one bundle and returns Apply's and Replay's errors.
func foldBothWays(t *testing.T, l *ledgerBuilder, events ...model.TypedEvent) (applyErr, replayErr error, s Snapshot) {
	t.Helper()
	prefix := mustReplay(t, l.bundles())
	b := l.add(t, events...)
	_, applyErr = Apply(prefix, b)
	s, replayErr = Replay(l.bundles())
	return applyErr, replayErr, s
}

func TestHoldSurvivesAmendmentAndClearsAtCurrentRevision(t *testing.T) {
	// Good control: the hold placed on revision 1 is cleared against revision 2.
	l := heldThenAmended(t)
	applyErr, replayErr, s := foldBothWays(t, l, clearAt(newID("TSKA"), 2, newID("HDA1")), successAt(2))
	if applyErr != nil || replayErr != nil {
		t.Fatalf("clear at the current revision refused: Apply %v, Replay %v", applyErr, replayErr)
	}
	p, _ := s.Task(ident(ref(newID("TSKA"), 2)))
	if p.Status != StatusClosed || p.Outcome != model.ClosureSuccess {
		t.Fatalf("status %s outcome %s, want CLOSED success", p.Status, p.Outcome)
	}
	hold, ok := s.Hold(model.BlockerRef{Task: ref(newID("TSKA"), 2), BlockerID: newID("HDA1")})
	if !ok || hold.Open() || hold.TaskRevision != 1 {
		t.Fatalf("Hold lookup at revision 2: %+v %v, want the cleared hold placed on revision 1", hold, ok)
	}
}

func TestAmendedHoldStillBlocksSuccess(t *testing.T) {
	l := heldThenAmended(t)
	applyErr, replayErr, _ := foldBothWays(t, l, successAt(2))
	for name, err := range map[string]error{"Apply": applyErr, "Replay": replayErr} {
		f := wantFault(t, err, CodeInvalidTransition)
		if !strings.Contains(f.Detail, string(newID("HDA1"))) {
			t.Fatalf("%s: refusal must name the hold that survived the amendment: %v", name, err)
		}
	}
}

func TestHoldClearRefusals(t *testing.T) {
	cases := []struct {
		name   string
		events func(t *testing.T, l *ledgerBuilder) []model.TypedEvent
		check  func(t *testing.T, err error)
	}{
		{"stale-task-revision", func(t *testing.T, l *ledgerBuilder) []model.TypedEvent {
			return []model.TypedEvent{clearAt(newID("TSKA"), 1, newID("HDA1"))}
		}, func(t *testing.T, err error) { wantConflict(t, err) }},
		{"never-held-blocker", func(t *testing.T, l *ledgerBuilder) []model.TypedEvent {
			return []model.TypedEvent{clearAt(newID("TSKA"), 2, newID("HDA9"))}
		}, func(t *testing.T, err error) { wantFault(t, err, CodeUnknownReference) }},
		{"held-on-another-task", func(t *testing.T, l *ledgerBuilder) []model.TypedEvent {
			l.add(t, &model.TaskCreate{Provenance: provenance("lane-b"), ID: newID("TSKB"), Spec: taskSpec()})
			return []model.TypedEvent{clearAt(newID("TSKB"), 1, newID("HDA1"))}
		}, func(t *testing.T, err error) { wantFault(t, err, CodeUnknownReference) }},
		{"already-cleared", func(t *testing.T, l *ledgerBuilder) []model.TypedEvent {
			l.add(t, clearAt(newID("TSKA"), 2, newID("HDA1")))
			return []model.TypedEvent{clearAt(newID("TSKA"), 2, newID("HDA1"))}
		}, func(t *testing.T, err error) {
			if f := wantFault(t, err, CodeInvalidTransition); !strings.Contains(f.Detail, "already cleared") {
				t.Fatalf("wrong refusal: %v", err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := heldThenAmended(t)
			applyErr, replayErr, _ := foldBothWays(t, l, c.events(t, l)...)
			c.check(t, applyErr)
			c.check(t, replayErr)
		})
	}
}
