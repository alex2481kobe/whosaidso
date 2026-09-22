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
	expected := map[model.ID]string{}
	// Different facts in one review must not borrow a sibling's comparison.
	for i, outcome := range []string{"accepted", "rejected", "correction-requested"} {
		refs := []model.PacketRef{}
		facts := map[model.ID]model.SelfAdmissionState{}
		for j := 2; j >= 0; j-- {
			id := testID(10*i + j + 1)
			refs = append(refs, model.PacketRef{CommandID: id, Digest: model.HashBytes([]byte(id))})
			facts[id] = states[j]
			expected[id] = []string{"true", "false", "UNKNOWN"}[j]
		}
		appendEvents(t, p, 100+i, &model.ReviewAdmit{Packets: refs, Outcome: outcome,
			Actor: model.Actor{ID: "reviewer"}, Reason: "Self-admitted: true.", SelfAdmission: facts})
	}
	legacy := model.PacketRef{CommandID: testID(31), Digest: model.HashBytes([]byte("legacy"))}
	appendEvents(t, p, 103, &model.ReviewAdmit{Packets: []model.PacketRef{legacy}, Outcome: "rejected",
		Actor: model.Actor{ID: "reviewer"}, Reason: "Self-admitted: true."})
	unknown := model.PacketRef{CommandID: testID(32), Digest: model.HashBytes([]byte("unknown actor"))}
	expected[legacy.CommandID], expected[unknown.CommandID] = "UNKNOWN", "UNKNOWN"
	appendEvents(t, p, 104, &model.ReviewAdmit{Packets: []model.PacketRef{unknown}, Outcome: "accepted",
		Actor: model.Actor{UnknownReason: "identity not supplied"}, Reason: "Self-admitted: false.",
		SelfAdmission: map[model.ID]model.SelfAdmissionState{unknown.CommandID: model.SelfAdmissionUnknown}})
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
			a, err := Read(p, Request{Command: "history", SelfAdmitted: tc.filter})
			if err != nil {
				t.Fatal(err)
			}
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
			if tc.filter == "" && len(a.History) != 5 || tc.filter != "" && len(a.History) != 0 {
				t.Fatalf("audit must select packet reviews without attributing event siblings: %+v", a.History)
			}
			var jsonOut, textOut bytes.Buffer
			if err := RenderJSON(&jsonOut, a); err != nil {
				t.Fatal(err)
			}
			if err := RenderText(&textOut, a); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(jsonLeaves(t, jsonOut.Bytes()), textLeaves(t, textOut.String())) {
				t.Fatal("audit renderers disagree")
			}
			if tc.filter == model.SelfAdmissionUnknown && !strings.Contains(textOut.String(), `"SelfAdmission": "UNKNOWN"`) {
				t.Fatalf("unknown must render explicitly: %s", textOut.String())
			}
			again, err := Read(p, Request{Command: "history", SelfAdmitted: tc.filter})
			if err != nil || !reflect.DeepEqual(a, again) {
				t.Fatalf("unstable audit: %v", err)
			}
		})
	}
	pending := readAnswer(t, p, "intake pending", "")
	if len(pending.Intake) != 7 {
		t.Fatalf("rejected/correction reviews vanished: %+v", pending.Intake)
	}
	if pending.Intake[6].Review.SelfAdmission != "UNKNOWN" {
		t.Fatalf("legacy pending review lost UNKNOWN: %+v", pending.Intake[6])
	}
	if !reflect.DeepEqual(before, treeBytes(t, p.Root)) {
		t.Fatal("audit modified ledger")
	}
}

func TestSelfAdmissionFilterValidationAndEmptyAnswers(t *testing.T) {
	p := testProject(t)
	for _, request := range []Request{
		{Command: "show", SelfAdmitted: "true"}, {Command: "intake pending", SelfAdmitted: "unknown"},
		{Command: "task todo", SelfAdmitted: "false"}, {Command: "history", SelfAdmitted: "no"},
		{Command: "history", SelfAdmitted: "true", ID: testID(1)},
	} {
		if _, err := Read(p, request); err == nil {
			t.Fatalf("invalid filter accepted: %+v", request)
		}
	}
	for _, state := range []model.SelfAdmissionState{"true", "false", "unknown"} {
		a, err := Read(p, Request{Command: "history", SelfAdmitted: state})
		if err != nil || a.Result != "KNOWN" || a.Reviews == nil || len(a.Reviews) != 0 || a.Watermark.Sequence != 0 {
			t.Fatalf("empty audit must succeed with watermark: %+v, %v", a, err)
		}
	}
	a := readAnswer(t, p, "history", testID(99))
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
	a, err := Read(p, Request{Command: "history", SelfAdmitted: model.SelfAdmissionUnknown})
	if err != nil {
		t.Fatal(err)
	}
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
	found := false
	for _, r := range a.Reviews {
		if r.SelfAdmission != "UNKNOWN" {
			t.Fatalf("legacy fact inferred: %+v", r)
		}
		if r.Origin.Sequence == 1 {
			found = strings.Contains(r.Reason, "Self-admitted: true.")
		}
	}
	if !found {
		t.Fatal("lost original first-bundle self-admission prose")
	}
	// The same inventory-versus-invariant correction as above. This asserted
	// that NO review is classified true or false, which held only while the
	// ledger contained nothing written after the field existed. Two
	// instruments were then declared and admitted by their own author, which
	// is genuinely self-admitted and correctly recorded as true.
	//
	// The invariant is narrower and survives growth: a review from BEFORE the
	// field existed is never classified either way. Sequences 1 and 2 are
	// those records, and no later record can move them.
	for _, state := range []model.SelfAdmissionState{"true", "false"} {
		selected, err := Read(p, Request{Command: "history", SelfAdmitted: state})
		if err != nil || selected.Watermark != a.Watermark {
			t.Fatalf("classified read failed for %s: %+v %v", state, selected, err)
		}
		for _, r := range selected.Reviews {
			if r.Origin.Sequence <= 2 {
				t.Fatalf("a review predating the field was classified as %s, which can only have come from reading its prose: %+v", state, r)
			}
		}
	}
}
