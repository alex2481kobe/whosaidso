package reduce

// Tests for handback.go's ledger rules, through Replay and Apply alike: who
// may write a receipt or take an attempt over, and the hold a stopped handback
// must carry in its own bundle. Each table opens with a control that folds.

import (
	"testing"

	"whosaidso/internal/model"
)

func receiptATTA(outcome model.AttemptOutcome) *model.AttemptTerminal {
	return &model.AttemptTerminal{Task: ref(newID("TSKA"), 1), AttemptID: newID("ATTA"), Outcome: outcome,
		Reason: "the lane stopped", NextAction: "resume it", DeliveryRefs: []model.ArtifactRef{}}
}

// handbackLedger is TSKA with attempt ATTA held by holder.
func handbackLedger(t *testing.T, holder model.Actor) *ledgerBuilder {
	t.Helper()
	l := newLedger()
	l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
	l.add(t, authored(l.seq+1, []model.TypedEvent{&model.TaskStart{Task: ref(newID("TSKA"), 1), Actor: holder, AttemptID: newID("ATTA")}}, holder.ID)...)
	return l
}

func TestReceiptIsTheHoldersOwnPacket(t *testing.T) {
	laneA, unknown := model.Actor{ID: "lane-a"}, model.Actor{UnknownReason: "the holder was not recorded"}
	for _, tc := range []struct {
		name   string
		holder model.Actor
		author string // "" records the packet author as unknown
		code   string
	}{
		{"control: the holder writes it", laneA, "lane-a", ""},
		{"a stranger writes it", laneA, "stranger", CodeAttributionMismatch},
		{"an unknown author writes it", laneA, "", CodeAttributionMismatch},
		{"control: a named author for an unknown holder", unknown, "lane-b", ""},
		{"two unknowns never match", unknown, "", CodeAttributionMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := handbackLedger(t, tc.holder)
			l.add(t, authored(l.seq+1, []model.TypedEvent{receiptATTA(model.AttemptStopped)}, tc.author)...)
			wantBoth(t, l, tc.code)
		})
	}
	// No review attributes the receipt at all: it has no author, so it is
	// nobody's receipt.
	l := handbackLedger(t, laneA)
	l.bare = true
	l.add(t, receiptATTA(model.AttemptStopped))
	wantBoth(t, l, CodeAttributionMismatch)
}

func TestTakeoverIsTheNewHoldersOwnPacket(t *testing.T) {
	for _, tc := range []struct{ author, code string }{{"lane-b", ""}, {"lane-a", CodeAttributionMismatch}} {
		l := handbackLedger(t, model.Actor{ID: "lane-a"})
		l.add(t, authored(l.seq+1, []model.TypedEvent{takeoverOf("ATTA")}, tc.author)...)
		wantBoth(t, l, tc.code)
	}
}

func TestStoppedHandbackCarriesItsHold(t *testing.T) {
	hold := func(reason model.BlockerReason, actor string) *model.BlockerHold {
		named := model.Actor{ID: actor}
		if actor == "" {
			named = model.Actor{UnknownReason: "nobody was named to resume it"}
		}
		return &model.BlockerHold{Task: ref(newID("TSKA"), 1), BlockerID: newID("HDA1"), Reason: reason,
			Actor: named, Criterion: "the owner rules on the boundary"}
	}
	clear := &model.BlockerClear{Task: ref(newID("TSKA"), 1), BlockerID: newID("HDA1"),
		HoldRef: model.BlockerRef{Task: ref(newID("TSKA"), 1), BlockerID: newID("HDA1")}, ResolvingWitness: blobRef("ruling")}
	amend := &model.TaskAmend{Provenance: provenance("lane-a"), Target: ref(newID("TSKA"), 1), ExpectedRevision: 1, Replacement: taskSpec()}
	for _, tc := range []struct {
		name    string
		outcome model.AttemptOutcome
		with    []model.TypedEvent
		code    string
	}{
		{"control: stopped needs no hold", model.AttemptStopped, nil, ""},
		{"control: blocked with its hold", model.AttemptBlockedMidTask, []model.TypedEvent{hold(model.BlockerPrerequisite, "owner")}, ""},
		{"blocked without a hold", model.AttemptBlockedMidTask, nil, CodeMissingHold},
		{"blocked with a hold this bundle clears", model.AttemptBlockedMidTask, []model.TypedEvent{hold(model.BlockerPrerequisite, "owner"), clear}, CodeMissingHold},
		{"control: out of scope with a named resume hold", model.AttemptOutOfScope, []model.TypedEvent{hold(model.BlockerResume, "lane-b")}, ""},
		{"out of scope with a hold of another reason", model.AttemptOutOfScope, []model.TypedEvent{hold(model.BlockerPrerequisite, "lane-b")}, CodeMissingHold},
		{"out of scope with a resume hold naming nobody", model.AttemptOutOfScope, []model.TypedEvent{hold(model.BlockerResume, "")}, CodeMissingHold},
		{"out of scope amending its own task", model.AttemptOutOfScope, []model.TypedEvent{hold(model.BlockerResume, "lane-b"), amend}, CodeInvalidTransition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := handbackLedger(t, model.Actor{ID: "lane-a"})
			events := append([]model.TypedEvent{receiptATTA(tc.outcome)}, tc.with...)
			authors := make([]string, len(events))
			for i := range authors {
				authors[i] = "lane-a"
			}
			l.add(t, authored(l.seq+1, events, authors...)...)
			wantBoth(t, l, tc.code)
		})
	}
	// A hold admitted in an earlier bundle is not this handback's hold.
	l := handbackLedger(t, model.Actor{ID: "lane-a"})
	l.add(t, hold(model.BlockerPrerequisite, "owner"))
	l.add(t, authored(l.seq+1, []model.TypedEvent{receiptATTA(model.AttemptBlockedMidTask)}, "lane-a")...)
	wantBoth(t, l, CodeMissingHold)
}
