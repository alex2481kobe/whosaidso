package write

// The gate's revision ordering: a proposed event naming a record at revision r
// is ordered before the proposed event that moves that record to r+1, for
// every record kind; constraints that cannot all hold are a dependency cycle;
// events inside one packet keep the author's order. Check admission must give
// admission's verdict in every case.

import (
	"context"
	"testing"

	"whosaidso/internal/model"
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

func TestGateOrdersReferrerBeforeTheAmendmentThatMovesItsRevision(t *testing.T) {
	body := []byte("owner read the delivery")
	t.Run("hold-amend-clear captured amend, clear, hold", func(t *testing.T) {
		f := newAdmissionFixture(t)
		task := f.goodControl()
		hold := &model.BlockerHold{Task: f.ref(task.ID, 1), BlockerID: f.id(), Reason: model.BlockerAwaitingAcceptance, Actor: f.author, Criterion: "owner reads the delivery"}
		amend := f.capture(nil, &model.TaskAmend{Target: f.ref(task.ID, 1), Replacement: task.Spec, Provenance: task.Provenance})
		clear := f.capture([][]byte{body}, holdIdentityClear(f.ref(task.ID, 2), hold.BlockerID, body))
		held := f.capture(nil, hold)
		got := eventTypes(f.admitBoth("", amend, clear, held), 3)
		if got[0] != "blocker.hold" || got[1] != "task.amend" || got[2] != "blocker.clear" {
			t.Fatalf("admitted order %v", got)
		}
	})
	t.Run("start on r1 captured after the amendment", func(t *testing.T) {
		f := newAdmissionFixture(t)
		task := f.goodControl()
		amend := f.capture(nil, &model.TaskAmend{Target: f.ref(task.ID, 1), Replacement: task.Spec, Provenance: task.Provenance})
		start := f.capture(nil, &model.TaskStart{Task: f.ref(task.ID, 1), Actor: f.author, AttemptID: f.id()})
		got := eventTypes(f.admitBoth("", amend, start), 2)
		if got[0] != "task.start" || got[1] != "task.amend" {
			t.Fatalf("admitted order %v", got)
		}
	})
	t.Run("constraints that cannot all hold are a cycle", func(t *testing.T) {
		f := newAdmissionFixture(t)
		task := f.goodControl()
		hold := &model.BlockerHold{Task: f.ref(task.ID, 1), BlockerID: f.id(), Reason: model.BlockerAwaitingAcceptance, Actor: f.author, Criterion: "owner reads the delivery"}
		amend := f.capture(nil, &model.TaskAmend{Target: f.ref(task.ID, 1), Replacement: task.Spec, Provenance: task.Provenance})
		// The hold names r1 (before the amendment), the clear names r2 (after it).
		both := f.capture([][]byte{body}, hold, holdIdentityClear(f.ref(task.ID, 2), hold.BlockerID, body))
		f.admitBoth("dependency-cycle", amend, both)
	})
	t.Run("one packet keeps the author's order", func(t *testing.T) {
		f := newAdmissionFixture(t)
		task := f.goodControl()
		hold := &model.BlockerHold{Task: f.ref(task.ID, 1), BlockerID: f.id(), Reason: model.BlockerAwaitingAcceptance, Actor: f.author, Criterion: "owner reads the delivery"}
		f.admitBoth("revision-conflict", f.capture(nil, &model.TaskAmend{Target: f.ref(task.ID, 1), Replacement: task.Spec, Provenance: task.Provenance}, hold))
	})
}

// The rule is not task-specific: a packet naming claim revision 1 goes before
// the packet revising the claim to revision 2, whatever their capture order.
func TestGateOrdersClaimReferrerBeforeClaimRevision(t *testing.T) {
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
	ordered, err := gatePackets(f.project.ID, snapshot, []model.Packet{revise, referrer})
	if err != nil {
		t.Fatal(err)
	}
	if ordered[0].CommandID != referrer.CommandID || ordered[1].CommandID != revise.CommandID {
		t.Fatalf("order %s, %s: the claim revision went before the packet naming revision 1", ordered[0].CommandID, ordered[1].CommandID)
	}
}
