package reduce

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"datum/internal/model"
)

func TestReviewSelfAdmissionProjection(t *testing.T) {
	for _, outcome := range []string{"accepted", "rejected", "correction-requested"} {
		t.Run(outcome, func(t *testing.T) {
			l := newLedger()
			// Deliberately unsorted, with facts differing within one review.
			packets := []model.PacketRef{
				{CommandID: newID("C"), Digest: newDigest("third")},
				{CommandID: newID("A"), Digest: newDigest("first")},
				{CommandID: newID("B"), Digest: newDigest("second")},
			}
			authors := map[model.ID]model.Actor{
				newID("C"): {UnknownReason: "not recorded"},
				newID("A"): {ID: "reviewer"},
				newID("B"): {ID: "other"},
			}
			states := map[model.ID]model.SelfAdmissionState{
				newID("C"): model.SelfAdmissionUnknown,
				newID("A"): model.SelfAdmissionTrue,
				newID("B"): model.SelfAdmissionFalse,
			}
			// C39: the legacy stored field is decoded but never authority, so
			// it deliberately contradicts every computed answer here.
			stored := map[model.ID]model.SelfAdmissionState{
				newID("C"): model.SelfAdmissionTrue,
				newID("A"): model.SelfAdmissionFalse,
				newID("B"): model.SelfAdmissionTrue,
			}
			review := &model.ReviewAdmit{
				Packets: packets, Outcome: outcome, Actor: model.Actor{ID: "reviewer"},
				Reason: "  self-admitted prose is not authoritative\n", SelfAdmission: stored, Authors: authors,
			}
			l.add(t, review)
			s := mustReplay(t, l.bundles())
			all := s.Reviews()
			if len(all) != len(packets) {
				t.Fatalf("got %d reviews, want %d", len(all), len(packets))
			}
			for i, packet := range packets {
				key := ReviewKey{Project: testProject, CommandID: packet.CommandID}
				got, ok := s.Review(key)
				if !ok || got.SelfAdmission != states[packet.CommandID] {
					t.Fatalf("packet %d comparison was not computed from its author and admitter: %+v", i, got)
				}
				if got.Packet != packet || got.Outcome != outcome || got.Reason != review.Reason || got.Actor != review.Actor {
					t.Fatalf("projection changed the admitted review: %+v", got)
				}
				if got.Origin != (Origin{Sequence: 1, EventIndex: 0}) {
					t.Fatalf("wrong review origin: %+v", got.Origin)
				}
			}
			for i, tag := range []string{"A", "B", "C"} {
				if all[i].Key.CommandID != newID(tag) {
					t.Fatalf("reviews are not deterministically ordered: %+v", all)
				}
				got, ok := s.Review(all[i].Key)
				if !ok || !reflect.DeepEqual(all[i], got) {
					t.Fatalf("list and lookup disagree: %+v, %+v", all[i], got)
				}
			}
			// Both access paths must leave the computed comparison immutable.
			all[0].SelfAdmission = model.SelfAdmissionFalse
			all[0].Reason = "changed"
			all[0].Actor.ID = "changed"
			got, _ := s.Review(ReviewKey{Project: testProject, CommandID: newID("A")})
			got.SelfAdmission = model.SelfAdmissionUnknown
			if again := s.Reviews()[0]; again.SelfAdmission != model.SelfAdmissionTrue || again.Reason != review.Reason || again.Actor != review.Actor {
				t.Fatalf("caller mutated the snapshot: %+v", again)
			}
		})
	}
}

// TestReviewSelfAdmissionComputedStates is C39 through the ledger: the answer
// comes from the recorded author and the admitter, never from a stored field.
func TestReviewSelfAdmissionComputedStates(t *testing.T) {
	known := func(id string) model.Actor { return model.Actor{ID: id} }
	unknown := func(reason string) model.Actor { return model.Actor{UnknownReason: reason} }
	ptr := func(a model.Actor) *model.Actor { return &a }
	for _, tc := range []struct {
		name     string
		author   *model.Actor // nil: legacy review without an authors map
		admitter model.Actor
		stored   model.SelfAdmissionState // "" writes no stored field
		want     model.SelfAdmissionState
	}{
		{"same known actor", ptr(known("reviewer")), known("reviewer"), "", model.SelfAdmissionTrue},
		{"distinct known actors", ptr(known("other")), known("reviewer"), "", model.SelfAdmissionFalse},
		{"ids differ only by case", ptr(known("Reviewer")), known("reviewer"), "", model.SelfAdmissionFalse},
		{"unknown author", ptr(unknown("not recorded")), known("reviewer"), "", model.SelfAdmissionUnknown},
		{"unknown admitter", ptr(known("reviewer")), unknown("not recorded"), "", model.SelfAdmissionUnknown},
		{"two unknowns with the same reason", ptr(unknown("not recorded")), unknown("not recorded"), "", model.SelfAdmissionUnknown},
		{"missing author is not the admitter", nil, known("reviewer"), "", model.SelfAdmissionUnknown},
		{"stored true without authors", nil, known("reviewer"), model.SelfAdmissionTrue, model.SelfAdmissionUnknown},
		{"stored true over distinct actors", ptr(known("other")), known("reviewer"), model.SelfAdmissionTrue, model.SelfAdmissionFalse},
		{"stored false over the same actor", ptr(known("reviewer")), known("reviewer"), model.SelfAdmissionFalse, model.SelfAdmissionTrue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLedger()
			packet := model.PacketRef{CommandID: newID("A"), Digest: newDigest("packet")}
			review := &model.ReviewAdmit{Packets: []model.PacketRef{packet}, Outcome: "accepted", Actor: tc.admitter, Reason: "reviewed"}
			if tc.author != nil {
				review.Authors = map[model.ID]model.Actor{packet.CommandID: *tc.author}
			}
			if tc.stored != "" {
				review.SelfAdmission = map[model.ID]model.SelfAdmissionState{packet.CommandID: tc.stored}
			}
			l.add(t, review)
			got, ok := mustReplay(t, l.bundles()).Review(ReviewKey{Project: testProject, CommandID: packet.CommandID})
			if !ok || got.SelfAdmission != tc.want {
				t.Fatalf("self-admission = %q, want %q: %+v", got.SelfAdmission, tc.want, got)
			}
		})
	}
}

// A malformed actor cannot reach the ledger, so the comparison is asked
// directly: an actor naming both an id and an unknown reason is not known.
func TestSelfAdmissionMalformedActorIsUnknown(t *testing.T) {
	both := model.Actor{ID: "reviewer", UnknownReason: "also unknown"}
	for _, pair := range [][2]model.Actor{{both, both}, {both, {ID: "reviewer"}}, {{ID: "other"}, both}, {{}, {}}} {
		if got := selfAdmission(pair[0], pair[1]); got != model.SelfAdmissionUnknown {
			t.Fatalf("selfAdmission(%+v, %+v) = %q, want unknown", pair[0], pair[1], got)
		}
	}
	if got := selfAdmission(model.Actor{ID: "a"}, model.Actor{ID: "b"}); got != model.SelfAdmissionFalse {
		t.Fatalf("control: distinct known actors = %q, want false", got)
	}
}

// Datum's own bundles 3 and 4 store "true" without recording packet authors.
// Under C39 they read UNKNOWN; their committed bytes are not rewritten.
func TestReviewSelfAdmissionCommittedHistoryIsUnknown(t *testing.T) {
	paths, err := filepath.Glob("../../.datum/events/*.json")
	if err != nil || len(paths) < 4 {
		t.Fatalf("committed history missing: %v %v", paths, err)
	}
	var bundles []model.Bundle
	storedTrue := 0
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		bundle, err := model.DecodeBundle(data)
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range bundle.Events {
			if raw.Type != "review.admit" {
				continue
			}
			e, err := model.DecodeEvent(raw)
			if err != nil {
				t.Fatal(err)
			}
			for _, state := range e.(*model.ReviewAdmit).SelfAdmission {
				if state == model.SelfAdmissionTrue {
					storedTrue++
				}
			}
		}
		bundles = append(bundles, bundle)
	}
	if storedTrue < 2 {
		t.Fatalf("control: committed bundles 3 and 4 should still store true, found %d", storedTrue)
	}
	// The subject is bundles 1-4, admitted before packet authors were recorded.
	// Later bundles record authors, so their reviews may read true or false.
	var legacy []Review
	for _, review := range mustReplay(t, bundles).Reviews() {
		if review.Origin.Sequence <= 4 {
			legacy = append(legacy, review)
		}
	}
	if len(legacy) != 4 {
		t.Fatalf("committed reviews in bundles 1-4 missing: %+v", legacy)
	}
	for _, review := range legacy {
		if review.SelfAdmission != model.SelfAdmissionUnknown {
			t.Fatalf("committed review without a recorded author must read unknown: %+v", review)
		}
	}
}

func TestReviewSelfAdmissionLegacyIgnoresProse(t *testing.T) {
	for _, reason := range []string{
		"Self-admitted: true.", "Self-admitted: false.", "Self-admitted: unknown.", "no machine-generated suffix",
	} {
		t.Run(reason, func(t *testing.T) {
			l := newLedger()
			packet := model.PacketRef{CommandID: newID("A"), Digest: newDigest("packet")}
			l.add(t, &model.ReviewAdmit{
				Packets: []model.PacketRef{packet}, Outcome: "accepted", Actor: model.Actor{ID: "reviewer"}, Reason: reason,
			})
			s := mustReplay(t, l.bundles())
			got, ok := s.Review(ReviewKey{Project: testProject, CommandID: packet.CommandID})
			if !ok || got.SelfAdmission != model.SelfAdmissionUnknown || got.Reason != reason {
				t.Fatalf("legacy prose was treated as a fact: %+v", got)
			}
			if all := s.Reviews(); len(all) != 1 || !reflect.DeepEqual(all[0], got) {
				t.Fatalf("legacy review missing from audit inventory: %+v", all)
			}
		})
	}
}

func TestReviewSelfAdmissionCommittedSequenceOneReplay(t *testing.T) {
	data, err := os.ReadFile("../../.datum/events/00000001-01M3408ER2RFD597S5KPXMYP4P.json")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := model.DecodeBundle(data)
	if err != nil {
		t.Fatal(err)
	}
	s := mustReplay(t, []model.Bundle{bundle})
	if s.Watermark().Sequence != 1 || len(s.Records()) == 0 {
		t.Fatal("committed history did not replay")
	}
	all := s.Reviews()
	if len(all) != 1 || all[0].SelfAdmission != model.SelfAdmissionUnknown {
		t.Fatalf("committed legacy review must remain explicitly unknown: %+v", all)
	}
	for _, raw := range bundle.Events {
		if raw.Type != "review.admit" {
			continue
		}
		e, err := model.DecodeEvent(raw)
		if err != nil {
			t.Fatal(err)
		}
		if all[0].Reason != e.(*model.ReviewAdmit).Reason {
			t.Fatal("committed reason was rewritten")
		}
	}
}

func TestReviewSelfAdmissionEmptySnapshot(t *testing.T) {
	var s Snapshot
	if got := s.Reviews(); got == nil || len(got) != 0 {
		t.Fatalf("empty snapshot should have an empty audit inventory: %+v", got)
	}
	if got, ok := s.Review(ReviewKey{Project: testProject, CommandID: newID("A")}); ok || !reflect.DeepEqual(got, Review{}) {
		t.Fatalf("missing packet invented a review: %+v", got)
	}
}

func TestReviewSelfAdmissionMixedHistoryAudit(t *testing.T) {
	l := newLedger()
	legacy := model.PacketRef{CommandID: newID("A"), Digest: newDigest("legacy")}
	known := model.PacketRef{CommandID: newID("B"), Digest: newDigest("known")}
	independent := model.PacketRef{CommandID: newID("C"), Digest: newDigest("independent")}
	l.add(t, &model.ReviewAdmit{
		Packets: []model.PacketRef{legacy}, Outcome: "accepted",
		Actor: model.Actor{ID: "reviewer"}, Reason: "Self-admitted: true.",
	})
	earlier := mustReplay(t, l.bundles())
	l.add(t, &model.ReviewAdmit{
		Packets: []model.PacketRef{known, independent}, Outcome: "accepted",
		Actor: model.Actor{ID: "reviewer"}, Reason: "Self-admitted: false.",
		Authors: map[model.ID]model.Actor{known.CommandID: {ID: "reviewer"}, independent.CommandID: {ID: "other"}},
	})
	later := mustReplay(t, l.bundles())
	var selected []model.ID
	for _, review := range later.Reviews() {
		if review.SelfAdmission == model.SelfAdmissionTrue {
			selected = append(selected, review.Key.CommandID)
		}
	}
	if !reflect.DeepEqual(selected, []model.ID{known.CommandID}) {
		t.Fatalf("audit confused legacy prose or a different actor with a known match: %v", selected)
	}
	if got := earlier.Reviews(); len(got) != 1 || got[0].SelfAdmission != model.SelfAdmissionUnknown {
		t.Fatalf("later admission changed the earlier answer: %+v", got)
	}
	if got := later.Reviews(); len(got) != 3 || got[0].Origin.Sequence != 1 || got[1].Origin.Sequence != 2 {
		t.Fatalf("mixed history lost admission origins: %+v", got)
	}
}

func TestReviewSelfAdmissionMalformedFactCannotReplay(t *testing.T) {
	packet := model.PacketRef{CommandID: newID("A"), Digest: newDigest("packet")}
	for _, data := range []string{
		`"self_admission":{}`, // An explicit field cannot silently become legacy.
		`"self_admission":{"` + string(packet.CommandID) + `":"yes"}`,
		`"self_admission":{"` + string(newID("B")) + `":"true"}`,
	} {
		l := newLedger()
		l.add(t, &model.ReviewAdmit{
			Packets: []model.PacketRef{packet}, Outcome: "accepted",
			Actor: model.Actor{ID: "reviewer"}, Reason: "control",
		})
		if _, err := Replay(l.bundles()); err != nil {
			t.Fatalf("control failed: %v", err)
		}
		raw := &l.out[0].Events[0]
		raw.Data = append(raw.Data[:len(raw.Data)-1], []byte(","+data+"}")...)
		if _, err := Replay(l.bundles()); err == nil {
			t.Fatalf("reducer accepted malformed fact: %s", data)
		}
	}
}

func TestReviewSelfAdmissionCannotRewritePriorDisposition(t *testing.T) {
	l := newLedger()
	packet := model.PacketRef{CommandID: newID("A"), Digest: newDigest("packet")}
	review := &model.ReviewAdmit{
		Packets: []model.PacketRef{packet}, Outcome: "accepted",
		Actor: model.Actor{ID: "reviewer"}, Reason: "reviewed",
		Authors: map[model.ID]model.Actor{packet.CommandID: {ID: "reviewer"}},
	}
	l.add(t, review)
	before := mustReplay(t, l.bundles())
	review.Authors = map[model.ID]model.Actor{packet.CommandID: {ID: "other"}}
	l.add(t, review)
	if _, err := Replay(l.bundles()); err == nil {
		t.Fatal("a second disposition rewrote self-admission")
	}
	if got := before.Reviews()[0].SelfAdmission; got != model.SelfAdmissionTrue {
		t.Fatalf("failed replay changed the earlier snapshot: %q", got)
	}
}
