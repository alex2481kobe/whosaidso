package write

// Tests for the operations U12 opened outside proof: task.close (a cited
// authority's carrier, terminal attempts, success witnesses, containment) and
// decision.open/revise. Decision disposition is tested in gate_dispose_test.go;
// closing with no authority and the accepter (R15.1) in gate_accept_test.go.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
)

type closeWorld struct {
	f       *admissionFixture
	task    model.RecordRef
	spec    model.TaskSpec
	attempt model.ID
	ruling  model.ArtifactRef
}

func newCloseWorld(t *testing.T, terminal bool) *closeWorld {
	t.Helper()
	f := newAdmissionFixture(t)
	task := f.task()
	f.accept(f.capture(nil, task))
	w := &closeWorld{f: f, task: f.ref(task.ID, 1), spec: task.Spec, attempt: f.id()}
	f.accept(f.capture(nil, &model.TaskStart{Task: w.task, Actor: f.author, AttemptID: w.attempt}))
	proofPut(t, f.project.Root, "delivery/report.json", `{"delivered":true}`)
	proofPut(t, f.project.Root, "rulings/close.json", `{"ruling":"close it"}`)
	w.ruling = proofPin(`{"ruling":"close it"}`, "rulings/close.json")
	if terminal {
		f.accept(f.capture(nil, &model.AttemptTerminal{Task: w.task, AttemptID: w.attempt, Outcome: model.AttemptSuccess, Reason: "done",
			NextAction: "accept the delivery", DeliveryRefs: []model.ArtifactRef{proofPin(`{"delivered":true}`, "delivery/report.json")}}))
	}
	return w
}

func (w *closeWorld) carrier(speaker string) *model.SourceIntake {
	return &model.SourceIntake{SourceID: w.f.id(), SourceRef: w.ruling, OriginalDigest: w.ruling.Content.SHA256, Length: w.ruling.Content.Length,
		Speaker: model.Actor{ID: speaker}, Referents: []model.RecordRef{w.task}}
}

func (w *closeWorld) closure(outcome model.ClosureOutcome) *model.TaskClose {
	scope := w.spec.Scope
	scope.ContextRefs = []model.RecordRef{w.task}
	delivery := proofPin(`{"delivered":true}`, "delivery/report.json")
	return &model.TaskClose{Task: w.task, Outcome: outcome,
		Authority:             &model.Authority{Actor: model.Actor{ID: "owner"}, SourceRef: w.ruling, Selector: model.Selector{Kind: "json-pointer", Pointer: "/ruling"}, Scope: scope},
		AcceptanceWitnessRefs: []model.AcceptanceWitness{{CriterionID: w.spec.AcceptanceCriteria[0].ID, CriterionRevision: 1, WitnessRef: delivery}},
		DeliveryWitnessRefs:   []model.ArtifactRef{delivery}}
}

func (w *closeWorld) status(t *testing.T) reduce.TaskProjection {
	t.Helper()
	p, _ := w.f.snapshot().Task(reduce.Ident{Project: w.task.Project, ID: w.task.RecordID})
	return p
}

func TestTaskCloseSuccessRequiresWitnessesAndTerminalAttempts(t *testing.T) {
	for _, route := range []string{"control", "no-acceptance", "no-delivery", "stale-witness", "live-attempt", "cancelled-live", "cancelled-control"} {
		t.Run(route, func(t *testing.T) {
			w := newCloseWorld(t, !strings.HasSuffix(route, "live") && route != "live-attempt")
			closure, code := w.closure(model.ClosureSuccess), "closure-ineffective"
			switch route {
			case "control":
				code = ""
			case "no-acceptance":
				closure.AcceptanceWitnessRefs = []model.AcceptanceWitness{}
			case "no-delivery":
				closure.DeliveryWitnessRefs = []model.ArtifactRef{}
			case "stale-witness":
				closure.AcceptanceWitnessRefs[0].CriterionRevision = 2
			case "cancelled-live":
				closure.Outcome = model.ClosureCancelled
			case "cancelled-control":
				closure.Outcome, closure.AcceptanceWitnessRefs, closure.DeliveryWitnessRefs, code = model.ClosureCancelled, []model.AcceptanceWitness{}, []model.ArtifactRef{}, ""
			}
			request := w.f.request(w.f.capture([][]byte{[]byte(`{"ruling":"close it"}`)}, w.carrier("owner"), closure))
			if code != "" {
				w.f.refuse(request, code)
				if w.status(t).Status == reduce.StatusClosed {
					t.Fatal("refused closure closed the task")
				}
				return
			}
			if _, err := Admit(context.Background(), w.f.project, request); err != nil {
				t.Fatalf("authorised closure must admit: %v", err)
			}
			if p := w.status(t); p.Status != reduce.StatusClosed || p.Outcome != closure.Outcome {
				t.Fatalf("closure did not take effect: %+v", p)
			}
		})
	}
}

// R15.1 made the authority optional; one a closure does cite is still checked.
func TestTaskCloseAuthorityNeedsItsCarrier(t *testing.T) {
	for _, route := range []string{"no-carrier", "other-speaker", "unknown-actor", "scope-omits-task", "ruling-selector-absent", "witness-escape"} {
		t.Run(route, func(t *testing.T) {
			w := newCloseWorld(t, true)
			closure, events, code := w.closure(model.ClosureSuccess), []model.TypedEvent{w.carrier("owner")}, "authority-unavailable"
			switch route {
			case "no-carrier":
				events = nil
			case "other-speaker":
				events = []model.TypedEvent{w.carrier("someone-else")}
			case "unknown-actor":
				closure.Authority.Actor = model.Actor{UnknownReason: "not recorded"}
			case "scope-omits-task":
				closure.Authority.Scope.ContextRefs = []model.RecordRef{}
				code = "authority-scope"
			case "ruling-selector-absent":
				closure.Authority.Selector = model.Selector{Kind: "json-pointer", Pointer: "/not-said"}
				code = "unavailable"
			case "witness-escape":
				outside := t.TempDir()
				proofPut(t, outside, "secret.json", `{"delivered":"elsewhere"}`)
				if err := os.Symlink(filepath.Join(outside, "secret.json"), filepath.Join(w.f.project.Root, "escape.json")); err != nil {
					t.Fatal(err)
				}
				closure.DeliveryWitnessRefs = []model.ArtifactRef{proofPin(`{"delivered":"elsewhere"}`, "escape.json")}
				code = "unavailable"
			}
			w.f.refuse(w.f.request(w.f.capture([][]byte{[]byte(`{"ruling":"close it"}`)}, append(events, closure)...)), code)
		})
	}
}

func TestDecisionOpenAndReviseAdmitWithoutDisposition(t *testing.T) {
	f := newAdmissionFixture(t)
	scope := f.task().Spec.Scope
	open := &model.DecisionOpen{ID: f.id(), Provenance: model.Provenance{Author: f.author, SourceRefs: []model.ArtifactRef{}},
		Spec: model.DecisionSpec{Question: "ship this", Options: []string{"yes", "no"}, WaitingActor: model.Actor{ID: "owner"}, Scope: scope}}
	f.accept(f.capture(nil, open))
	ref := f.ref(open.ID, 1)
	revise := &model.DecisionRevise{Provenance: open.Provenance, Target: ref, Replacement: open.Spec}
	forged := *revise
	forged.Provenance.Author = model.Actor{ID: "someone-else"}
	f.refuse(f.request(f.capture(nil, &forged)), "attribution-mismatch")
	f.accept(f.capture(nil, revise))
	d, ok := f.snapshot().Decision(reduce.Ident{Project: f.project.ID, ID: open.ID})
	if !ok || d.Status != reduce.StatusOpen || d.Decision.Revision != 2 {
		t.Fatalf("decision did not stay OPEN at revision 2: %+v", d)
	}
}
