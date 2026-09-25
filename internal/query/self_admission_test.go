package query

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// uncaptured records every packet's capture time as unknown: these reviews
// carry no events, so no start is bounded by it.
func uncaptured(refs []model.PacketRef) map[model.ID]model.Availability[time.Time] {
	out := map[model.ID]model.Availability[time.Time]{}
	for _, r := range refs {
		out[r.CommandID] = model.Availability[time.Time]{State: model.Unknown, Reason: "not recorded"}
	}
	return out
}

// authoredBy records one known author for every packet.
func authoredBy(id string, refs []model.PacketRef) map[model.ID]model.Actor {
	out := map[model.ID]model.Actor{}
	for _, r := range refs {
		out[r.CommandID] = model.Actor{ID: id}
	}
	return out
}

func TestSelfAdmissionAuditThreeStates(t *testing.T) {
	p := testProject(t)
	// The answer is computed from each packet's recorded author and the
	// admitter; the reason prose claims otherwise and is never read.
	authors := []model.Actor{{ID: "reviewer"}, {ID: "other"}, {UnknownReason: "author not recorded"}}
	expected := map[model.ID]string{}
	// Different facts in one review must not borrow a sibling's comparison.
	for i, outcome := range []string{"accepted", "rejected", "correction-requested"} {
		refs := []model.PacketRef{}
		recorded := map[model.ID]model.Actor{}
		for j := 2; j >= 0; j-- {
			id := testID(10*i + j + 1)
			refs = append(refs, model.PacketRef{CommandID: id, Digest: model.HashBytes([]byte(id))})
			recorded[id] = authors[j]
			expected[id] = []string{"true", "false", "UNKNOWN"}[j]
		}
		appendEvents(t, p, 100+i, &model.ReviewAdmit{Packets: refs, Outcome: outcome,
			Actor: model.Actor{ID: "reviewer"}, Reason: "Self-admitted: true.", Authors: recorded,
			CapturedAt: uncaptured(refs), EventPackets: []model.ID{}})
	}
	unrecorded := model.PacketRef{CommandID: testID(31), Digest: model.HashBytes([]byte("unrecorded"))}
	appendEvents(t, p, 103, &model.ReviewAdmit{Packets: []model.PacketRef{unrecorded}, Outcome: "rejected",
		Actor: model.Actor{ID: "reviewer"}, Reason: "Self-admitted: true.",
		Authors:    map[model.ID]model.Actor{unrecorded.CommandID: {UnknownReason: "not recorded at admission"}},
		CapturedAt: uncaptured([]model.PacketRef{unrecorded}), EventPackets: []model.ID{}})
	unknown := model.PacketRef{CommandID: testID(32), Digest: model.HashBytes([]byte("unknown actor"))}
	expected[unrecorded.CommandID], expected[unknown.CommandID] = "UNKNOWN", "UNKNOWN"
	appendEvents(t, p, 104, &model.ReviewAdmit{Packets: []model.PacketRef{unknown}, Outcome: "accepted",
		Actor: model.Actor{UnknownReason: "identity not supplied"}, Reason: "Self-admitted: false.",
		// Two unknowns with the same reason never match.
		Authors:    map[model.ID]model.Actor{unknown.CommandID: {UnknownReason: "identity not supplied"}},
		CapturedAt: uncaptured([]model.PacketRef{unknown}), EventPackets: []model.ID{}})
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
	pending := todoOf(t, p).PacketsNotAccepted
	if len(pending) != 7 {
		t.Fatalf("rejected/correction reviews vanished: %+v", pending)
	}
	if pending[6].Review.SelfAdmission != "UNKNOWN" {
		t.Fatalf("pending review with an unrecorded author lost UNKNOWN: %+v", pending[6])
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
