package reduce

// R15.1 through Replay and Apply alike: a task naming an accepter is closed
// only by a packet that actor wrote; no authority is needed; the closer is
// projected, and closer_authored_receipt compares it with the receipt's author: TRUE
// for the same known actor, FALSE for distinct known actors, UNKNOWN when
// either is unknown, two unknowns included.

import (
	"fmt"
	"testing"
	"time"

	"whosaidso/internal/model"
)

// authored appends an accepted review attributing each event to the actor
// named beside it; an empty name records that author as unknown.
func authored(seq uint64, events []model.TypedEvent, authors ...string) []model.TypedEvent {
	review := &model.ReviewAdmit{Outcome: "accepted", Actor: model.Actor{ID: "coordinator"}, Reason: "fixture admission"}
	for i := range events {
		packet := newID(fmt.Sprintf("PKT%dA%d", seq, i))
		review.Packets = append(review.Packets, model.PacketRef{CommandID: packet, Digest: newDigest(string(packet))})
		review.EventPackets = append(review.EventPackets, packet)
		if review.Authors == nil {
			review.Authors, review.CapturedAt = map[model.ID]model.Actor{}, map[model.ID]model.Availability[time.Time]{}
		}
		review.Authors[packet] = model.Actor{ID: authors[i]}
		if authors[i] == "" {
			review.Authors[packet] = model.Actor{UnknownReason: "the fixture recorded no author"}
		}
		review.CapturedAt[packet] = knownAt(baseTime)
	}
	return append(append([]model.TypedEvent{}, events...), review)
}

// acceptLedger is a task (with the given accepter) whose attempt's success
// receipt was written by doer ("" leaves it unattributed), ready to close.
func acceptLedger(t *testing.T, accepter *model.Actor, doer string) *ledgerBuilder {
	t.Helper()
	spec := taskSpec()
	spec.Accepter = accepter
	l := newLedger()
	l.add(t, &model.TaskCreate{Provenance: provenance("agent-a"), ID: newID("TSKA"), Spec: spec})
	l.add(t, &model.TaskStart{Task: ref(newID("TSKA"), 1), Actor: model.Actor{ID: "agent-a"}, AttemptID: newID("ATTA")})
	receipt := &model.AttemptTerminal{Task: ref(newID("TSKA"), 1), AttemptID: newID("ATTA"), Outcome: model.AttemptSuccess,
		Reason: "done", NextAction: "accept it", DeliveryRefs: []model.ArtifactRef{blobRef("delivery")}}
	l.add(t, authored(l.seq+1, []model.TypedEvent{receipt}, doer)...)
	return l
}

func closeBy(l *ledgerBuilder, t *testing.T, closer string) {
	t.Helper()
	closure := closeSuccess(newID("TSKA"), 1)
	closure.Authority = nil
	l.add(t, authored(l.seq+1, []model.TypedEvent{closure}, closer)...)
}

func TestAccepterAloneMayClose(t *testing.T) {
	owner := model.Actor{ID: "owner"}
	for _, tc := range []struct {
		name, closer, code string
	}{
		{"the accepter closes", "owner", ""},
		{"the doer closes", "agent-a", CodeAccepterMismatch},
		{"an unattributed packet closes", "", CodeAccepterMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := acceptLedger(t, &owner, "agent-a")
			if p := projectTask(t, l, newID("TSKA")); p.Reasons[0].Actor != owner {
				t.Fatalf("an awaiting-acceptance task waits on its accepter: %+v", p.Reasons)
			}
			closeBy(l, t, tc.closer)
			s := wantBoth(t, l, tc.code)
			if tc.code == "" {
				if p, _ := s.Task(Ident{Project: testProject, ID: newID("TSKA")}); p.Status != StatusClosed || p.Closure.Closer.Author != owner {
					t.Fatalf("the accepter's closure must close the task and record the closer: %+v", p.Closure)
				}
			}
		})
	}
}

func TestSelfAccepted(t *testing.T) {
	for _, tc := range []struct {
		name, doer, closer string
		want               Truth
	}{
		{"the doer closes", "agent-a", "agent-a", TruthTrue},
		{"another actor closes", "agent-a", "owner", TruthFalse},
		{"the closer is unknown", "agent-a", "", TruthUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := acceptLedger(t, nil, tc.doer)
			closeBy(l, t, tc.closer)
			p, _ := wantBoth(t, l, "").Task(Ident{Project: testProject, ID: newID("TSKA")})
			if p.Status != StatusClosed || p.Closure.CloserAuthoredReceipt != tc.want {
				t.Fatalf("closer_authored_receipt = %s, want %s (a visible fact, never a block): %+v", p.Closure.CloserAuthoredReceipt, tc.want, p.Closure)
			}
		})
	}
	// A receipt whose author is unknown is not the holder's: the ledger
	// refuses it (handback.go), so no closure is compared with an unknown doer.
	wantBoth(t, acceptLedger(t, nil, ""), CodeAttributionMismatch)
	// No receipt at all: there is no known doer to compare with.
	l := newLedger()
	l.add(t, &model.TaskCreate{Provenance: provenance("agent-a"), ID: newID("TSKA"), Spec: taskSpec()})
	closeBy(l, t, "owner")
	if p := projectTask(t, l, newID("TSKA")); p.Closure.CloserAuthoredReceipt != TruthUnknown {
		t.Fatalf("a closure with no receipt compared against nobody: want UNKNOWN, got %s", p.Closure.CloserAuthoredReceipt)
	}
}
