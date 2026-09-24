package write

// Tests that admission records each reviewed packet's author in the ledger
// (R10.1 revised: accountability is visibility), and that every admitted event
// is attributed to the packet that carried it even when the gate reorders
// packets by dependency. Decision disposition rules live in gate_dispose_test.go.

import (
	"context"
	"reflect"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
)

func decodeReview(t *testing.T, b model.Bundle) *model.ReviewAdmit {
	t.Helper()
	e, err := model.DecodeEvent(b.Events[len(b.Events)-1])
	if err != nil {
		t.Fatal(err)
	}
	return e.(*model.ReviewAdmit)
}

func TestDisposedDecisionNamesItsPacketAuthorBesideTheAuthority(t *testing.T) {
	w := newDisposeWorld(t)
	packet := w.f.capture(nil, w.dispose(1, "approved"))
	bundle := w.f.accept(packet)
	review := decodeReview(t, bundle)
	if got := review.Authors; !reflect.DeepEqual(got, map[model.ID]model.Actor{packet.CommandID: {ID: "lane-c2"}}) {
		t.Fatalf("review.admit authors = %+v, want the packet's captured author lane-c2", got)
	}
	if got := review.EventPackets; !reflect.DeepEqual(got, []model.ID{packet.CommandID}) {
		t.Fatalf("event_packets = %v, want the one dispose event attributed to %s", got, packet.CommandID)
	}
	d := w.decision(t, 1).Dispositions[0]
	if d.Author != (reduce.PacketAuthor{Packet: packet.CommandID, Author: model.Actor{ID: "lane-c2"}}) {
		t.Fatalf("disposition author = %+v, want lane-c2 from packet %s", d.Author, packet.CommandID)
	}
	if d.Disposition.Authority.Actor.ID != "owner" {
		t.Fatalf("authority replaced by the author: %+v", d.Disposition.Authority.Actor)
	}
}

func TestReorderedPacketsKeepTheirOwnAuthors(t *testing.T) {
	f := newAdmissionFixture(t)
	proofPut(t, f.project.Root, "rulings/decision.json", disposeRuling)
	w := &disposeWorld{f: f, id: f.id(), scope: f.task().Spec.Scope}
	// The dispose packet gets the LOWER command id, so sorted order and the
	// gate's dependency order disagree: the open must be admitted first.
	f.author = model.Actor{ID: "lane-dispose"}
	dispose := f.capture(nil, w.dispose(1, "rejected"))
	f.author = model.Actor{ID: "lane-open"}
	open := f.capture(nil, &model.DecisionOpen{ID: w.id, Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}},
		Spec: model.DecisionSpec{Question: "ship", Options: []string{"yes", "no"}, WaitingActor: model.Actor{ID: "owner"}, Scope: w.scope}})
	bundle := f.accept(dispose, open)
	if len(bundle.Events) != 3 || bundle.Events[0].Type != "decision.open" || bundle.Events[1].Type != "decision.dispose" {
		t.Fatalf("control: expected the gate to reorder open before dispose, got %+v", bundle.Events)
	}
	s := f.snapshot()
	want := []reduce.PacketAuthor{
		{Packet: open.CommandID, Author: model.Actor{ID: "lane-open"}},
		{Packet: dispose.CommandID, Author: model.Actor{ID: "lane-dispose"}},
	}
	for i, w := range want {
		if got := s.EventAuthor(reduce.Origin{Sequence: bundle.Sequence, EventIndex: i}); got != w {
			t.Fatalf("event %d author = %+v, want %+v", i, got, w)
		}
	}
	if got := w.decision(t, 1).Dispositions[0].Author; got != want[1] {
		t.Fatalf("disposition attributed to %+v, want %+v", got, want[1])
	}
}

func TestUnknownPacketAuthorIsRecordedAsUnknown(t *testing.T) {
	for _, outcome := range []string{"accepted", "rejected"} {
		t.Run(outcome, func(t *testing.T) {
			f := newAdmissionFixture(t)
			f.author = model.Actor{UnknownReason: "captured by a script with no identity"}
			packet := f.capture(nil, f.task())
			request := f.request(packet)
			request.Outcome = outcome
			review := decodeReview(t, admitOrFail(t, f, request))
			if got := review.Authors[packet.CommandID]; got != f.author {
				t.Fatalf("recorded author = %+v, want the captured unknown actor %+v", got, f.author)
			}
			if outcome != "accepted" && (review.EventPackets == nil || len(review.EventPackets) != 0) {
				t.Fatalf("a %s review attributed events it did not admit: %v", outcome, review.EventPackets)
			}
			r, _ := f.snapshot().Review(reduce.ReviewKey{Project: f.project.ID, CommandID: packet.CommandID})
			if r.Author != f.author {
				t.Fatalf("review projection author = %+v, want %+v", r.Author, f.author)
			}
		})
	}
}

func admitOrFail(t *testing.T, f *admissionFixture, request AdmitRequest) model.Bundle {
	t.Helper()
	b, err := Admit(context.Background(), f.project, request)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
