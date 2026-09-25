package write

// Admission's side of a hold's identity: a hold is found by (task id, blocker
// id) through the reducer's one lookup, so a clear against the amended task's
// current revision admits, and check admission gives admission's verdict. The
// reducer's side is tested in internal/reduce/hold_identity_test.go.

import (
	"context"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
)

func holdIdentityClear(task model.RecordRef, blocker model.ID, body []byte) *model.BlockerClear {
	return &model.BlockerClear{Task: task, BlockerID: blocker, HoldRef: model.BlockerRef{Task: task, BlockerID: blocker}, ResolvingWitness: admissionContent(body)}
}

func TestAdmissionClearsAHoldAfterAmendment(t *testing.T) {
	body := []byte("owner read the delivery")
	for _, c := range []struct {
		name string
		rev  model.Revision
		// held names the held blocker id; otherwise one never held.
		held bool
		code string
	}{
		{"current-revision", 2, true, ""},
		{"stale-revision", 1, true, "revision-conflict"},
		{"never-held", 2, false, "unknown-reference"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newAdmissionFixture(t)
			task := f.goodControl()
			hold := &model.BlockerHold{Task: f.ref(task.ID, 1), BlockerID: f.id(), Reason: model.BlockerAwaitingAcceptance, Actor: f.author, Criterion: "owner reads the delivery"}
			f.accept(f.capture(nil, hold))
			f.accept(f.capture(nil, &model.TaskAmend{Target: f.ref(task.ID, 1), Replacement: task.Spec, Provenance: task.Provenance}))
			blocker := hold.BlockerID
			if !c.held {
				blocker = f.id()
			}
			packet := f.capture([][]byte{body}, holdIdentityClear(f.ref(task.ID, c.rev), blocker, body))
			request := f.request(packet)
			check, err := CheckAdmission(context.Background(), f.project, request.PacketIDs, nil, request.Admitter)
			if err != nil {
				t.Fatal(err)
			}
			checked := ""
			if len(check.Refusals) > 0 {
				checked = admissionErrorCode(check.Refusals[0].Err)
			}
			if checked != c.code {
				t.Fatalf("check admission: %+v, want %q", check.Refusals, c.code)
			}
			if c.code != "" {
				f.refuse(request, c.code)
				return
			}
			f.accept(packet)
			got, ok := f.snapshot().Hold(model.BlockerRef{Task: f.ref(task.ID, 2), BlockerID: hold.BlockerID})
			if !ok || got.Open() {
				t.Fatalf("hold after clear: %+v %v", got, ok)
			}
		})
	}
}

// A hold, the amendment and the clear proposed together order by dependency:
// the clear names revision 2 and the hold revision 1, and the gate matches them
// by task and blocker id as the reducer does.
func TestAdmissionOrdersHoldAmendAndClearTogether(t *testing.T) {
	f := newAdmissionFixture(t)
	task := f.goodControl()
	body := []byte("owner read the delivery")
	hold := &model.BlockerHold{Task: f.ref(task.ID, 1), BlockerID: f.id(), Reason: model.BlockerAwaitingAcceptance, Actor: f.author, Criterion: "owner reads the delivery"}
	// The clear's packet has the lowest ULID and must still land last.
	clear := f.capture([][]byte{body}, holdIdentityClear(f.ref(task.ID, 2), hold.BlockerID, body))
	held := f.capture(nil, hold)
	amend := f.capture(nil, &model.TaskAmend{Target: f.ref(task.ID, 1), Replacement: task.Spec, Provenance: task.Provenance})
	bundle := f.accept(clear, held, amend)
	var got []string
	for _, e := range bundle.Events[:3] {
		got = append(got, string(e.Type))
	}
	if got[0] != "blocker.hold" || got[1] != "task.amend" || got[2] != "blocker.clear" {
		t.Fatalf("admitted order %v", got)
	}
	if blockers := f.snapshot().Blockers(reduce.Ident{Project: f.project.ID, ID: task.ID}); len(blockers) != 1 || blockers[0].Open() {
		t.Fatalf("blockers %+v", blockers)
	}
}
