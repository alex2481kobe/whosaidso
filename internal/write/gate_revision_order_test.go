package write

// Admission is optimistic concurrency: the gate orders a packet after the
// packets creating what it references and otherwise keeps capture (packet id)
// order. It never reorders packets to rescue a proposal naming a revision a
// packet in the same set supersedes; that proposal is refused as stale and its
// author re-captures it. Events inside one packet keep the author's order.
// Check admission must give admission's verdict in every case.

import (
	"context"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// admitBoth runs check admission and admission on the same packets and fails
// unless both give code ("" admits).
func (f *admissionFixture) admitBoth(code string, refs ...model.PacketRef) model.Bundle {
	f.t.Helper()
	request := f.request(refs...)
	check, err := CheckAdmission(context.Background(), f.project, request.PacketIDs, nil, request.Admitter)
	if err != nil {
		f.t.Fatal(err)
	}
	checked := ""
	if len(check.Refusals) > 0 {
		checked = admissionErrorCode(check.Refusals[0].Err)
	}
	if checked != code {
		f.t.Fatalf("check admission: %+v, want %q", check.Refusals, code)
	}
	if code != "" {
		f.refuse(request, code)
		return model.Bundle{}
	}
	return f.accept(refs...)
}

func eventTypes(b model.Bundle, n int) []string {
	var got []string
	for _, e := range b.Events[:n] {
		got = append(got, string(e.Type))
	}
	return got
}

func TestGateRefusesAProposalMadeStaleByAnEarlierCapture(t *testing.T) {
	body := []byte("owner read the delivery")
	t.Run("hold on r1 captured after the amendment is stale", func(t *testing.T) {
		f := newAdmissionFixture(t)
		task := f.goodControl()
		hold := &model.BlockerHold{Task: f.ref(task.ID, 1), BlockerID: f.id(), Reason: model.BlockerAwaitingAcceptance, Actor: f.author, Criterion: "owner reads the delivery"}
		amend := f.capture(nil, &model.TaskAmend{Target: f.ref(task.ID, 1), Replacement: task.Spec, Provenance: task.Provenance})
		clear := f.capture([][]byte{body}, holdIdentityClear(f.ref(task.ID, 2), hold.BlockerID, body))
		held := f.capture(nil, hold)
		f.admitBoth("revision-conflict", amend, clear, held)
	})
	t.Run("hold on r1 captured before the amendment admits in capture order", func(t *testing.T) {
		f := newAdmissionFixture(t)
		task := f.goodControl()
		hold := &model.BlockerHold{Task: f.ref(task.ID, 1), BlockerID: f.id(), Reason: model.BlockerAwaitingAcceptance, Actor: f.author, Criterion: "owner reads the delivery"}
		held := f.capture(nil, hold)
		amend := f.capture(nil, &model.TaskAmend{Target: f.ref(task.ID, 1), Replacement: task.Spec, Provenance: task.Provenance})
		clear := f.capture([][]byte{body}, holdIdentityClear(f.ref(task.ID, 2), hold.BlockerID, body))
		got := eventTypes(f.admitBoth("", clear, amend, held), 3)
		if got[0] != "blocker.hold" || got[1] != "task.amend" || got[2] != "blocker.clear" {
			t.Fatalf("admitted order %v", got)
		}
	})
	t.Run("start on r1 captured after the amendment is stale", func(t *testing.T) {
		f := newAdmissionFixture(t)
		task := f.goodControl()
		amend := f.capture(nil, &model.TaskAmend{Target: f.ref(task.ID, 1), Replacement: task.Spec, Provenance: task.Provenance})
		start := f.capture(nil, &model.TaskStart{Task: f.ref(task.ID, 1), Actor: f.author, AttemptID: f.id()})
		f.admitBoth("revision-conflict", amend, start)
	})
	t.Run("one packet keeps the author's order", func(t *testing.T) {
		f := newAdmissionFixture(t)
		task := f.goodControl()
		hold := &model.BlockerHold{Task: f.ref(task.ID, 1), BlockerID: f.id(), Reason: model.BlockerAwaitingAcceptance, Actor: f.author, Criterion: "owner reads the delivery"}
		f.admitBoth("revision-conflict", f.capture(nil, &model.TaskAmend{Target: f.ref(task.ID, 1), Replacement: task.Spec, Provenance: task.Provenance}, hold))
	})
}

// The gate keeps capture order between a claim revision and a packet naming
// the claim's earlier revision: a historical reference is not a dependency.
func TestGateKeepsCaptureOrderAroundAClaimRevision(t *testing.T) {
	f := newAdmissionFixture(t)
	claim := f.claim()
	f.accept(f.capture(nil, claim))
	snapshot := f.snapshot()
	revise := model.Packet{Project: f.project.ID, CommandID: f.id(), Author: f.author,
		Events: []model.Event{admissionTestEvent(t, &model.ClaimRevise{Provenance: claim.Provenance, Target: f.ref(claim.ID, 1), Replacement: claim.Spec})}}
	task := f.task()
	task.Spec.ContextRefs = []model.RecordRef{f.ref(claim.ID, 1)}
	referrer := model.Packet{Project: f.project.ID, CommandID: f.id(), Author: f.author,
		Events: []model.Event{admissionTestEvent(t, task)}}
	ordered, err := gatePackets(f.project.ID, snapshot, []model.Packet{referrer, revise})
	if err != nil {
		t.Fatal(err)
	}
	if ordered[0].CommandID != revise.CommandID || ordered[1].CommandID != referrer.CommandID {
		t.Fatalf("order %s, %s: the gate left capture order", ordered[0].CommandID, ordered[1].CommandID)
	}
}
