package reduce

// Tests for packet_author.go, each through both Replay and Apply: a
// criterion.fix's author and a proof.admit's judgment actor must be the
// identified author the bundle's review recorded for that event's packet.

import (
	"testing"
	"time"

	"whosaidso/internal/model"
)

// authoredReview attributes the one event before it to a packet whose recorded
// author is author. A review without authors or event_packets cannot be
// written at all (TestReviewWithoutRequiredFieldsIsRefused).
func authoredReview(author *model.Actor) *model.ReviewAdmit {
	packet := newID("PKTA")
	return &model.ReviewAdmit{Packets: []model.PacketRef{{CommandID: packet, Digest: newDigest("authored")}}, Outcome: "accepted",
		Actor: model.Actor{ID: "coordinator"}, Reason: "author fixture", EventPackets: []model.ID{packet},
		Authors: map[model.ID]model.Actor{packet: *author}, CapturedAt: map[model.ID]model.Availability[time.Time]{packet: knownAt(baseTime)}}
}

func actor(id string) *model.Actor         { return &model.Actor{ID: id} }
func unknownActor(why string) *model.Actor { return &model.Actor{UnknownReason: why} }

func TestCriterionAuthorIsThePacketAuthor(t *testing.T) {
	for _, tc := range []struct {
		name      string
		criterion model.Actor
		packet    *model.Actor
		review    bool // false: no review attributes it
		code      string
	}{
		{"control: the packet author fixed it", model.Actor{ID: "lane-a"}, actor("lane-a"), true, ""},
		{"another known author", model.Actor{ID: "lane-a"}, actor("lane-b"), true, CodeAttributionMismatch},
		{"two unknown actors", model.Actor{UnknownReason: "not recorded"}, unknownActor("not recorded"), true, CodeAttributionMismatch},
		{"known criterion author, unknown packet author", model.Actor{ID: "lane-a"}, unknownActor("not recorded"), true, CodeAttributionMismatch},
		{"no review attributes it", model.Actor{ID: "lane-a"}, nil, false, CodeAttributionMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := goodLedger(t)
			claim := ref(newID("CMA1"), 1)
			l.add(t, &model.ClaimAssert{ID: claim.RecordID, Provenance: provenance("lane-a"), Spec: claimSpec()})
			fix := fixProofCriterion(claim)
			fix.Author = tc.criterion
			events := []model.TypedEvent{fix}
			if tc.review {
				events = append(events, authoredReview(tc.packet))
			}
			l.bare = true
			l.add(t, events...)
			wantBoth(t, l, tc.code)
		})
	}
}

func TestProofJudgmentIsThePacketAuthor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		packet *model.Actor
		review bool // false: no review attributes it
		code   string
	}{
		{"control: the packet author judged it", actor("reviewer"), true, ""},
		{"another known author", actor("lane-b"), true, CodeAttributionMismatch},
		{"unknown packet author", unknownActor("not recorded"), true, CodeAttributionMismatch},
		{"no review attributes it", nil, false, CodeAttributionMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := proofLedger(t, false)
			events := []model.TypedEvent{admitProof(ref(newID("CMA1"), 1), newID("RNA"))} // judged by "reviewer"
			if tc.review {
				events = append(events, authoredReview(tc.packet))
			}
			l.bare = true
			l.add(t, events...)
			wantBoth(t, l, tc.code)
		})
	}
}
