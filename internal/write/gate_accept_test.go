package write

// R15.1 through admission: task.close needs no authority carrier, only its
// witnesses; a task that names an accepter is closed only by a packet that
// actor wrote; the closer is recorded, and self_accepted compares it with the
// author of the attempt receipt. The replay-side rules are in internal/reduce.

import (
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
)

// newAcceptWorld is newCloseWorld with an optional accepter on the task. The
// lane (f.author) starts the attempt and writes its success receipt.
func newAcceptWorld(t *testing.T, accepter *model.Actor) *closeWorld {
	t.Helper()
	f := newAdmissionFixture(t)
	task := f.task()
	task.Spec.Accepter = accepter
	f.accept(f.capture(nil, task))
	w := &closeWorld{f: f, task: f.ref(task.ID, 1), spec: task.Spec, attempt: f.id()}
	f.accept(f.capture(nil, &model.TaskStart{Task: w.task, Actor: f.author, AttemptID: w.attempt}))
	proofPut(t, f.project.Root, "delivery/report.json", `{"delivered":true}`)
	f.accept(f.capture(nil, &model.AttemptTerminal{Task: w.task, AttemptID: w.attempt, Outcome: model.AttemptSuccess, Reason: "done",
		NextAction: "accept the delivery", DeliveryRefs: []model.ArtifactRef{proofPin(`{"delivered":true}`, "delivery/report.json")}}))
	return w
}

// closeAs captures a closure with no authority, written by author.
func (w *closeWorld) closeAs(author string, closure *model.TaskClose) model.PacketRef {
	lane := w.f.author
	w.f.author = model.Actor{ID: author}
	defer func() { w.f.author = lane }()
	closure.Authority = nil
	return w.f.capture(nil, closure)
}

func TestTaskCloseNeedsNoCarrierAndRecordsASelfAcceptance(t *testing.T) {
	w := newAcceptWorld(t, nil)
	if p := w.status(t); p.Status != reduce.StatusBlocked || p.Reasons[0].Actor != w.f.author {
		t.Fatalf("control: a succeeded attempt awaits acceptance from the next actor: %+v", p)
	}
	// Witnesses are still required without an authority.
	bare := w.closure(model.ClosureSuccess)
	bare.AcceptanceWitnessRefs = []model.AcceptanceWitness{}
	w.f.refuse(w.f.request(w.closeAs(w.f.author.ID, bare)), "closure-ineffective")
	w.f.accept(w.closeAs(w.f.author.ID, w.closure(model.ClosureSuccess)))
	p := w.status(t)
	if p.Status != reduce.StatusClosed || p.Closure.Authority != nil || p.Closure.Closer.Author != w.f.author || p.Closure.SelfAccepted != reduce.TruthTrue {
		t.Fatalf("the lane closing its own work must read CLOSED, closer lane, self-accepted TRUE: %+v", p.Closure)
	}
}

func TestTaskAccepterAloneMayClose(t *testing.T) {
	owner := model.Actor{ID: "owner"}
	w := newAcceptWorld(t, &owner)
	if p := w.status(t); p.Status != reduce.StatusBlocked || p.Reasons[0].Actor != owner {
		t.Fatalf("an awaiting-acceptance task waits on its named accepter: %+v", p.Reasons)
	}
	w.f.refuse(w.f.request(w.closeAs(w.f.author.ID, w.closure(model.ClosureSuccess))), "accepter-mismatch")
	w.f.refuse(w.f.request(w.closeAs("someone-else", w.closure(model.ClosureSuccess))), "accepter-mismatch")
	if w.status(t).Status == reduce.StatusClosed {
		t.Fatal("a refused closure closed the task")
	}
	w.f.accept(w.closeAs("owner", w.closure(model.ClosureSuccess)))
	p := w.status(t)
	if p.Status != reduce.StatusClosed || p.Closure.Closer.Author != owner || p.Closure.SelfAccepted != reduce.TruthFalse {
		t.Fatalf("the accepter closing another's work must read CLOSED, closer owner, self-accepted FALSE: %+v", p.Closure)
	}
}
