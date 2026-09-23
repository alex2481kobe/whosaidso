package query

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"datum/internal/model"
	"datum/internal/store"
)

func TestSelfAdmissionAuditThreeStatesAndLegacy(t *testing.T) {
	p := testProject(t)
	states := []model.SelfAdmissionState{model.SelfAdmissionTrue, model.SelfAdmissionFalse, model.SelfAdmissionUnknown}
	// C39: the answer is computed from each packet's recorded author and the
	// admitter. The legacy stored field is rotated so it contradicts every
	// computed answer; a filter that read it would select the wrong packets.
	authors := []model.Actor{{ID: "reviewer"}, {ID: "other"}, {UnknownReason: "author not recorded"}}
	expected := map[model.ID]string{}
	// Different facts in one review must not borrow a sibling's comparison.
	for i, outcome := range []string{"accepted", "rejected", "correction-requested"} {
		refs := []model.PacketRef{}
		stored := map[model.ID]model.SelfAdmissionState{}
		recorded := map[model.ID]model.Actor{}
		for j := 2; j >= 0; j-- {
			id := testID(10*i + j + 1)
			refs = append(refs, model.PacketRef{CommandID: id, Digest: model.HashBytes([]byte(id))})
			stored[id] = states[(j+1)%3]
			recorded[id] = authors[j]
			expected[id] = []string{"true", "false", "UNKNOWN"}[j]
		}
		appendEvents(t, p, 100+i, &model.ReviewAdmit{Packets: refs, Outcome: outcome,
			Actor: model.Actor{ID: "reviewer"}, Reason: "Self-admitted: true.", SelfAdmission: stored, Authors: recorded})
	}
	legacy := model.PacketRef{CommandID: testID(31), Digest: model.HashBytes([]byte("legacy"))}
	appendEvents(t, p, 103, &model.ReviewAdmit{Packets: []model.PacketRef{legacy}, Outcome: "rejected",
		Actor: model.Actor{ID: "reviewer"}, Reason: "Self-admitted: true."})
	unknown := model.PacketRef{CommandID: testID(32), Digest: model.HashBytes([]byte("unknown actor"))}
	expected[legacy.CommandID], expected[unknown.CommandID] = "UNKNOWN", "UNKNOWN"
	appendEvents(t, p, 104, &model.ReviewAdmit{Packets: []model.PacketRef{unknown}, Outcome: "accepted",
		Actor: model.Actor{UnknownReason: "identity not supplied"}, Reason: "Self-admitted: false.",
		SelfAdmission: map[model.ID]model.SelfAdmissionState{unknown.CommandID: model.SelfAdmissionUnknown},
		// Two unknowns with the same reason never match.
		Authors: map[model.ID]model.Actor{unknown.CommandID: {UnknownReason: "identity not supplied"}}})
	before := treeBytes(t, p.Root)
	for _, tc := range []struct {
		filter model.SelfAdmissionState
		ids    []int
	}{
		{"", []int{1, 2, 3, 11, 12, 13, 21, 22, 23, 31, 32}},
		{model.SelfAdmissionTrue, []int{1, 11, 21}},
		{model.SelfAdmissionFalse, []int{2, 12, 22}},
		{model.SelfAdmissionUnknown, []int{3, 13, 23, 31, 32}},
	} {
		t.Run(string(tc.filter), func(t *testing.T) {
			a := view_(t, p, ViewRequest{View: "history", SelfAdmitted: tc.filter}).(*HistoryAnswer)
			if a.Result != "KNOWN" || a.Watermark.Sequence != 5 || a.Watermark.Bundles != 5 || a.Watermark.Events != 5 {
				t.Fatalf("audit lost watermark: %+v", a)
			}
			var ids []model.ID
			for _, review := range a.Reviews {
				ids = append(ids, review.Key.CommandID)
				want := expected[review.Key.CommandID]
				if review.SelfAdmission != want {
					t.Fatalf("comparison must be explicit: got %q want %q", review.SelfAdmission, want)
				}
				if review.Packet.CommandID != review.Key.CommandID || review.Origin.Sequence == 0 || review.Reason == "" {
					t.Fatalf("lost review attribution: %+v", review)
				}
				if review.Key.CommandID == unknown.CommandID && (review.Actor.ID != "" || review.Actor.UnknownReason == "") {
					t.Fatalf("invented actor: %+v", review)
				}
			}
			wantIDs := []model.ID{}
			for _, n := range tc.ids {
				wantIDs = append(wantIDs, testID(n))
			}
			if !reflect.DeepEqual(ids, wantIDs) {
				t.Fatalf("filter %q: got %v want %v", tc.filter, ids, wantIDs)
			}
			if tc.filter == "" && len(a.Events) != 5 || tc.filter != "" && len(a.Events) != 0 {
				t.Fatalf("audit must select packet reviews without attributing event siblings: %+v", a.Events)
			}
			assertViewHonest(t, a)
			var jsonOut bytes.Buffer
			if err := RenderViewJSON(&jsonOut, a); err != nil {
				t.Fatal(err)
			}
			if tc.filter == model.SelfAdmissionUnknown && !strings.Contains(jsonOut.String(), `"self_admission": "UNKNOWN"`) {
				t.Fatalf("unknown must render explicitly: %s", jsonOut.String())
			}
			again, err := ReadView(p, ViewRequest{View: "history", SelfAdmitted: tc.filter})
			if err != nil || !reflect.DeepEqual(ViewAnswer(a), again) {
				t.Fatalf("unstable audit: %v", err)
			}
		})
	}
	pending := todoOf(t, p).IntakePending
	if len(pending) != 7 {
		t.Fatalf("rejected/correction reviews vanished: %+v", pending)
	}
	if pending[6].Review.SelfAdmission != "UNKNOWN" {
		t.Fatalf("legacy pending review lost UNKNOWN: %+v", pending[6])
	}
	if !reflect.DeepEqual(before, treeBytes(t, p.Root)) {
		t.Fatal("audit modified ledger")
	}
}

func TestSelfAdmissionFilterValidationAndEmptyAnswers(t *testing.T) {
	p := testProject(t)
	for _, request := range []ViewRequest{
		{View: "show", SelfAdmitted: "true"}, {View: "todo", SelfAdmitted: "unknown"},
		{View: "history", SelfAdmitted: "no"},
		{View: "history", SelfAdmitted: "true", ID: testID(1)},
	} {
		if _, err := ReadView(p, request); err == nil {
			t.Fatalf("invalid filter accepted: %+v", request)
		}
	}
	for _, state := range []model.SelfAdmissionState{"true", "false", "unknown"} {
		a := view_(t, p, ViewRequest{View: "history", SelfAdmitted: state}).(*HistoryAnswer)
		if a.Result != "KNOWN" || a.Reviews == nil || len(a.Reviews) != 0 || a.Watermark.Sequence != 0 {
			t.Fatalf("empty audit must succeed with watermark: %+v", a)
		}
	}
	a := historyOf(t, p, testID(99))
	if a.Result != "UNKNOWN" || a.Watermark.Sequence != 0 {
		t.Fatalf("absent ID changed: %+v", a)
	}
}

func TestSelfAdmissionRealLedgerRemainsUnknown(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	a := view_(t, p, ViewRequest{View: "history", SelfAdmitted: model.SelfAdmissionUnknown}).(*HistoryAnswer)
	// Deliberately no assertion on the watermark or the review COUNT. This
	// test reads the repository's own live ledger, which is the strongest
	// regression evidence available here: real bytes, committed, written by
	// an earlier version of the code. It is also append-only, so pinning its
	// size or head position makes every legitimate record a test failure.
	// The first version asserted sequence == 2 and exactly two reviews, and
	// broke the moment two instruments were declared.
	//
	// Inventory, not invariant, in a repository whose own rule is the
	// opposite. What matters is that a review written BEFORE the
	// SelfAdmission field existed still reports UNKNOWN and is never inferred
	// from the prose still sitting in its reason.
	if len(a.Reviews) == 0 {
		t.Fatalf("the live ledger must still contain legacy reviews: %+v", a)
	}
	found, early := false, 0
	for _, r := range a.Reviews {
		if r.SelfAdmission != "UNKNOWN" {
			t.Fatalf("legacy fact inferred: %+v", r)
		}
		if r.Origin.Sequence == 1 {
			found = strings.Contains(r.Reason, "Self-admitted: true.")
		}
		if r.Origin.Sequence <= 4 {
			early++
		}
	}
	if !found {
		t.Fatal("lost original first-bundle self-admission prose")
	}
	// Sequences 1-4 each dispositioned one packet and are fixed history.
	if early != 4 {
		t.Fatalf("the four committed reviews without recorded authors must all select as unknown, got %d", early)
	}
	// The same inventory-versus-invariant correction as above, and it
	// survives growth: a later review that records its packet authors may
	// genuinely read true or false. Sequences 1-4 recorded no packet authors
	// (3 and 4 store a legacy "true"), so under C39 (self-admission is
	// computed from the recorded author and the admitter, step 7) none of
	// them can be classified either way, and no later record can move them.
	for _, state := range []model.SelfAdmissionState{"true", "false"} {
		selected := view_(t, p, ViewRequest{View: "history", SelfAdmitted: state}).(*HistoryAnswer)
		if selected.Watermark != a.Watermark {
			t.Fatalf("classified read failed for %s: %+v", state, selected)
		}
		for _, r := range selected.Reviews {
			if r.Origin.Sequence <= 4 {
				t.Fatalf("a review without a recorded author was classified as %s, which can only have come from its prose or its stored field: %+v", state, r)
			}
		}
	}
}
