package reduce

// Replay of review.admit packet authors and event attribution (R10.1 revised).
// Admission writing them is tested in internal/write/author_record_test.go.

import (
	"testing"

	"datum/internal/model"
)

// authoredDispose builds one bundle the way admission does: the packet's
// events, then its review naming the packet author and each event's packet.
func authoredDispose(t *testing.T, l *ledgerBuilder, d model.RecordRef, eventPackets []model.ID) (model.Bundle, model.ID) {
	t.Helper()
	packet := newID("PKTD")
	review := &model.ReviewAdmit{Packets: []model.PacketRef{{CommandID: packet, Digest: newDigest("dispose")}}, Outcome: "accepted",
		Actor: model.Actor{ID: "coordinator"}, Reason: "reviewed", Authors: map[model.ID]model.Actor{packet: {ID: "lane-c2"}}, EventPackets: eventPackets}
	if eventPackets == nil {
		review.EventPackets = []model.ID{packet}
	}
	dispose := &model.DecisionDispose{Decision: d, Disposition: "approved", Quote: "ship it", Scope: testScope(), Authority: rulingAuthority("owner")}
	return l.add(t, dispose, review), packet
}

func TestDispositionCarriesItsRecordedPacketAuthor(t *testing.T) {
	l := proofLedger(t, true)
	d := ref(newID("DCSA"), 1)
	l.add(t, &model.DecisionOpen{ID: d.RecordID, Provenance: provenance("author"), Spec: decisionSpec()})
	b, packet := authoredDispose(t, l, d, nil)
	s := mustReplay(t, l.out)
	p, _ := s.DecisionAt(d)
	want := PacketAuthor{Packet: packet, Author: model.Actor{ID: "lane-c2"}}
	if len(p.Dispositions) != 1 || p.Dispositions[0].Author != want {
		t.Fatalf("disposition author = %+v, want %+v", p.Dispositions, want)
	}
	if got := s.EventAuthor(Origin{Sequence: b.Sequence, EventIndex: 1}); got.Packet != "" || got.Author.ID != "" || got.Author.UnknownReason == "" {
		t.Fatalf("the review event itself was attributed to a packet author: %+v", got)
	}
}

func TestLegacyReviewProjectsAuthorsAsUnknown(t *testing.T) {
	l := proofLedger(t, true)
	d := ref(newID("DCSA"), 1)
	open := l.add(t, &model.DecisionOpen{ID: d.RecordID, Provenance: provenance("author"), Spec: decisionSpec()})
	packet := newID("PKTG")
	l.add(t, &model.DecisionDispose{Decision: d, Disposition: "approved", Quote: "ship it", Scope: testScope(), Authority: rulingAuthority("owner")},
		&model.ReviewAdmit{Packets: []model.PacketRef{{CommandID: packet, Digest: newDigest("legacy")}}, Outcome: "accepted", Actor: model.Actor{ID: "coordinator"}, Reason: "legacy"})
	s := mustReplay(t, l.out)
	p, _ := s.DecisionAt(d)
	if got := p.Dispositions[0].Author; got.Packet != "" || got.Author.ID != "" || got.Author.UnknownReason != unattributedAuthor {
		t.Fatalf("legacy disposition author guessed: %+v", got)
	}
	r, _ := s.Review(ReviewKey{Project: testProject, CommandID: packet})
	if r.Author.ID != "" || r.Author.UnknownReason != unrecordedAuthor {
		t.Fatalf("legacy review author guessed: %+v", r.Author)
	}
	// Neither the admitter nor the provenance author stands in for a packet author.
	if got := s.EventAuthor(Origin{Sequence: open.Sequence, EventIndex: 0}); got.Author.ID != "" {
		t.Fatalf("an event without a recording review was attributed: %+v", got)
	}
}

func TestEventPacketsMustCoverExactlyThePrecedingEvents(t *testing.T) {
	l := proofLedger(t, true)
	d := ref(newID("DCSA"), 1)
	l.add(t, &model.DecisionOpen{ID: d.RecordID, Provenance: provenance("author"), Spec: decisionSpec()})
	before := mustReplay(t, l.out)
	// One dispose event, two attributions: the second would name a phantom event.
	b, _ := authoredDispose(t, l, d, []model.ID{newID("PKTD"), newID("PKTD")})
	_, err := Apply(before, b)
	if f := wantFault(t, err, CodeInvalidTransition); f.Path != "event_packets" {
		t.Fatalf("over-long attribution refused at %s, want event_packets", f.Path)
	}
}
