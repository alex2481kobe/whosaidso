package reduce

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"datum/internal/model"
)

// review builds a complete review.admit alone in its bundle: every required
// field present, one author per packet in order, each captured at baseTime.
func review(outcome string, admitter model.Actor, reason string, packets []model.PacketRef, authors ...model.Actor) *model.ReviewAdmit {
	r := &model.ReviewAdmit{Packets: packets, Outcome: outcome, Actor: admitter, Reason: reason,
		Authors: map[model.ID]model.Actor{}, CapturedAt: map[model.ID]model.Availability[time.Time]{}, EventPackets: []model.ID{}}
	for i, p := range packets {
		r.Authors[p.CommandID] = authors[i]
		r.CapturedAt[p.CommandID] = knownAt(baseTime)
	}
	return r
}

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
			states := map[model.ID]model.SelfAdmissionState{
				newID("C"): model.SelfAdmissionUnknown,
				newID("A"): model.SelfAdmissionTrue,
				newID("B"): model.SelfAdmissionFalse,
			}
			review := review(outcome, model.Actor{ID: "reviewer"}, "  self-admitted prose is not authoritative\n", packets,
				model.Actor{UnknownReason: "not recorded"}, model.Actor{ID: "reviewer"}, model.Actor{ID: "other"})
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
	for _, tc := range []struct {
		name     string
		author   model.Actor
		admitter model.Actor
		want     model.SelfAdmissionState
	}{
		{"same known actor", known("reviewer"), known("reviewer"), model.SelfAdmissionTrue},
		{"distinct known actors", known("other"), known("reviewer"), model.SelfAdmissionFalse},
		{"ids differ only by case", known("Reviewer"), known("reviewer"), model.SelfAdmissionFalse},
		{"unknown author", unknown("not recorded"), known("reviewer"), model.SelfAdmissionUnknown},
		{"unknown admitter", known("reviewer"), unknown("not recorded"), model.SelfAdmissionUnknown},
		{"two unknowns with the same reason", unknown("not recorded"), unknown("not recorded"), model.SelfAdmissionUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLedger()
			packet := model.PacketRef{CommandID: newID("A"), Digest: newDigest("packet")}
			l.add(t, review("accepted", tc.admitter, "reviewed", []model.PacketRef{packet}, tc.author))
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

// Datum's own bundles 1-4 were admitted before packet authors were recorded.
// The R18.2 migration gave each an explicit unknown author and dropped the
// "true" bundles 3 and 4 used to store, so all four read UNKNOWN.
func TestReviewSelfAdmissionCommittedHistoryIsUnknown(t *testing.T) {
	paths, err := filepath.Glob("../../.datum/events/*.json")
	if err != nil || len(paths) < 4 {
		t.Fatalf("committed history missing: %v %v", paths, err)
	}
	var bundles []model.Bundle
	for _, path := range paths[:4] {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		bundle, err := model.DecodeBundle(data)
		if err != nil {
			t.Fatal(err)
		}
		bundles = append(bundles, bundle)
	}
	s := mustReplay(t, bundles)
	reviews := s.Reviews()
	if len(reviews) != 4 {
		t.Fatalf("committed reviews in bundles 1-4 missing: %+v", reviews)
	}
	for _, review := range reviews {
		if review.Author.ID != "" || review.Author.UnknownReason != "not recorded at admission (before R10.1)" {
			t.Fatalf("control: bundles 1-4 must record an explicit unknown author: %+v", review.Author)
		}
		if review.SelfAdmission != model.SelfAdmissionUnknown {
			t.Fatalf("committed review without a known author must read unknown: %+v", review)
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
			l.add(t, review("accepted", model.Actor{ID: "reviewer"}, reason, []model.PacketRef{packet}, model.Actor{UnknownReason: "not recorded"}))
			s := mustReplay(t, l.bundles())
			got, ok := s.Review(ReviewKey{Project: testProject, CommandID: packet.CommandID})
			if !ok || got.SelfAdmission != model.SelfAdmissionUnknown || got.Reason != reason {
				t.Fatalf("reason prose was treated as a fact: %+v", got)
			}
			if all := s.Reviews(); len(all) != 1 || !reflect.DeepEqual(all[0], got) {
				t.Fatalf("review missing from audit inventory: %+v", all)
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
		t.Fatalf("committed review with an unknown author must read unknown: %+v", all)
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
	l.add(t, review("accepted", model.Actor{ID: "reviewer"}, "Self-admitted: true.", []model.PacketRef{legacy},
		model.Actor{UnknownReason: "not recorded at admission (before R10.1)"}))
	earlier := mustReplay(t, l.bundles())
	l.add(t, review("accepted", model.Actor{ID: "reviewer"}, "Self-admitted: false.", []model.PacketRef{known, independent},
		model.Actor{ID: "reviewer"}, model.Actor{ID: "other"}))
	later := mustReplay(t, l.bundles())
	var selected []model.ID
	for _, review := range later.Reviews() {
		if review.SelfAdmission == model.SelfAdmissionTrue {
			selected = append(selected, review.Key.CommandID)
		}
	}
	if !reflect.DeepEqual(selected, []model.ID{known.CommandID}) {
		t.Fatalf("audit confused reason prose or a different actor with a known match: %v", selected)
	}
	if got := earlier.Reviews(); len(got) != 1 || got[0].SelfAdmission != model.SelfAdmissionUnknown {
		t.Fatalf("later admission changed the earlier answer: %+v", got)
	}
	if got := later.Reviews(); len(got) != 3 || got[0].Origin.Sequence != 1 || got[1].Origin.Sequence != 2 {
		t.Fatalf("mixed history lost admission origins: %+v", got)
	}
}

// R18.2: a review never stores self_admission; the field is refused, whatever
// it says.
func TestReviewStoredSelfAdmissionCannotReplay(t *testing.T) {
	packet := model.PacketRef{CommandID: newID("A"), Digest: newDigest("packet")}
	for _, data := range []string{
		`"self_admission":{}`,
		`"self_admission":{"` + string(packet.CommandID) + `":"true"}`,
		`"self_admission":{"` + string(packet.CommandID) + `":"unknown"}`,
	} {
		l := newLedger()
		l.add(t, review("accepted", model.Actor{ID: "reviewer"}, "control", []model.PacketRef{packet}, model.Actor{ID: "author"}))
		if _, err := Replay(l.bundles()); err != nil {
			t.Fatalf("control failed: %v", err)
		}
		raw := &l.out[0].Events[0]
		raw.Data = append(raw.Data[:len(raw.Data)-1], []byte(","+data+"}")...)
		if _, err := Replay(l.bundles()); err == nil {
			t.Fatalf("reducer accepted a stored self-admission: %s", data)
		}
	}
}

func TestReviewSelfAdmissionCannotRewritePriorDisposition(t *testing.T) {
	l := newLedger()
	packet := model.PacketRef{CommandID: newID("A"), Digest: newDigest("packet")}
	review := review("accepted", model.Actor{ID: "reviewer"}, "reviewed", []model.PacketRef{packet}, model.Actor{ID: "reviewer"})
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
