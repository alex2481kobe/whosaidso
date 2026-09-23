package reduce

// Replay of review.admit packet authors and event attribution (R10.1 revised).
// Admission writing them is tested in internal/write/author_record_test.go.

import (
	"encoding/json"
	"testing"
	"time"

	"datum/internal/model"
)

// authoredDispose builds one bundle the way admission does: the packet's
// events, then its review naming the packet author and each event's packet.
func authoredDispose(t *testing.T, l *ledgerBuilder, d model.RecordRef, eventPackets []model.ID) (model.Bundle, model.ID) {
	t.Helper()
	packet := newID("PKTD")
	review := &model.ReviewAdmit{Packets: []model.PacketRef{{CommandID: packet, Digest: newDigest("dispose")}}, Outcome: "accepted",
		Actor: model.Actor{ID: "coordinator"}, Reason: "reviewed", Authors: map[model.ID]model.Actor{packet: {ID: "lane-c2"}},
		CapturedAt: map[model.ID]model.Availability[time.Time]{packet: knownAt(baseTime)}, EventPackets: eventPackets}
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

// R18.2: authors, captured_at and event_packets are required on every review;
// an omitted one is refused, never read as unknown. An author recorded as
// unknown stays unknown and is never replaced by the admitter or provenance.
func TestReviewWithoutRequiredFieldsIsRefused(t *testing.T) {
	d := ref(newID("DCSA"), 1)
	packet := newID("PKTG")
	build := func(omit string) (*ledgerBuilder, model.Bundle) {
		l := proofLedger(t, true)
		l.add(t, &model.DecisionOpen{ID: d.RecordID, Provenance: provenance("author"), Spec: decisionSpec()})
		before := len(l.out)
		review := &model.ReviewAdmit{Packets: []model.PacketRef{{CommandID: packet, Digest: newDigest("unknown")}}, Outcome: "accepted",
			Actor: model.Actor{ID: "coordinator"}, Reason: "reviewed",
			Authors:      map[model.ID]model.Actor{packet: {UnknownReason: "not recorded at admission (before R10.1)"}},
			CapturedAt:   map[model.ID]model.Availability[time.Time]{packet: {State: model.Unknown, Reason: "not recorded at admission (before R10.1)"}},
			EventPackets: []model.ID{packet}}
		dispose := &model.DecisionDispose{Decision: d, Disposition: "approved", Quote: "ship it", Scope: testScope(), Authority: rulingAuthority("owner")}
		b := l.add(t, dispose, review)
		if omit != "" {
			raw := &l.out[before].Events[1]
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw.Data, &fields); err != nil {
				t.Fatal(err)
			}
			delete(fields, omit)
			data, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			raw.Data = data
		}
		return l, b
	}
	l, b := build("")
	s := mustReplay(t, l.out)
	p, _ := s.DecisionAt(d)
	if got := p.Dispositions[0].Author; got.Packet != packet || got.Author.ID != "" || got.Author.UnknownReason != "not recorded at admission (before R10.1)" {
		t.Fatalf("control: an explicit unknown author must project unknown with its reason: %+v", got)
	}
	r, _ := s.Review(ReviewKey{Project: testProject, CommandID: packet})
	if r.Author.ID != "" || r.SelfAdmission != model.SelfAdmissionUnknown {
		t.Fatalf("control: the admitter stood in for an unknown author: %+v", r)
	}
	if got := s.EventAuthor(Origin{Sequence: b.Sequence, EventIndex: 1}); got.Author.ID != "" {
		t.Fatalf("the review event itself was attributed: %+v", got)
	}
	for _, omit := range []string{"authors", "captured_at", "event_packets"} {
		l, _ := build(omit)
		if _, err := Replay(l.out); err == nil {
			t.Fatalf("a review without %s replayed", omit)
		}
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
