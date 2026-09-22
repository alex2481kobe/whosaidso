package reduce

import (
	"os"
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
			states := map[model.ID]model.SelfAdmissionState{
				newID("C"): model.SelfAdmissionUnknown,
				newID("A"): model.SelfAdmissionTrue,
				newID("B"): model.SelfAdmissionFalse,
			}
			review := &model.ReviewAdmit{
				Packets: packets, Outcome: outcome, Actor: model.Actor{ID: "reviewer"},
				Reason: "  self-admitted prose is not authoritative\n", SelfAdmission: states,
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
					t.Fatalf("packet %d fact was lost or reassigned: %+v", i, got)
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
			// Both access paths must leave the stored comparison immutable.
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
	data, err := os.ReadFile("../../record/events/00000001-01M3408ER2RFD597S5KPXMYP4P.json")
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
	if got, ok := s.Review(ReviewKey{Project: testProject, CommandID: newID("A")}); ok || got != (Review{}) {
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
		SelfAdmission: map[model.ID]model.SelfAdmissionState{
			known.CommandID: model.SelfAdmissionTrue, independent.CommandID: model.SelfAdmissionFalse,
		},
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
		SelfAdmission: map[model.ID]model.SelfAdmissionState{packet.CommandID: model.SelfAdmissionTrue},
	}
	l.add(t, review)
	before := mustReplay(t, l.bundles())
	review.SelfAdmission[packet.CommandID] = model.SelfAdmissionFalse
	l.add(t, review)
	if _, err := Replay(l.bundles()); err == nil {
		t.Fatal("a second disposition rewrote self-admission")
	}
	if got := before.Reviews()[0].SelfAdmission; got != model.SelfAdmissionTrue {
		t.Fatalf("failed replay changed the earlier snapshot: %q", got)
	}
}
