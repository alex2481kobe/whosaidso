package write

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

func handbackControl(t *testing.T) (*admissionFixture, *model.TaskCreate, HandbackRequest) {
	t.Helper()
	f := newAdmissionFixture(t)
	f.author = model.Actor{ID: "holder-é-持有者-🦊-�"}
	task := f.goodControl()
	attempt := f.id()
	f.accept(f.capture(nil, &model.TaskStart{Task: f.ref(task.ID, 1), Actor: f.author, AttemptID: attempt}))
	p, _ := f.snapshot().Task(reduce.Ident{Project: f.project.ID, ID: task.ID})
	if p.Status != reduce.StatusInFlight || len(p.LiveAttempts) != 1 {
		t.Fatal("control attempt did not start")
	}
	return f, task, HandbackRequest{CommandID: f.id(), Author: f.author, AttemptID: attempt, Outcome: model.AttemptStopped, Reason: "  writer's exact reason é / e\u0301 / 理由 / 🦊 / �\r\n", NextAction: "  owner's exact next action é / 次 / 🦊\n"}
}

func captureHandback(t *testing.T, f *admissionFixture, r HandbackRequest) model.PacketRef {
	t.Helper()
	ref, err := Handback(context.Background(), f.project, r)
	if err != nil {
		t.Fatal(err)
	}
	packets, err := store.ReadIntake(f.project, []model.ID{ref.CommandID})
	if err != nil || len(packets) != 1 || packets[0].CapturedAt.IsZero() || packets[0].Author != r.Author {
		t.Fatalf("receipt was not durable: %v", err)
	}
	return ref
}

func TestHandbackNineHonestOutcomes(t *testing.T) {
	for _, outcome := range []model.AttemptOutcome{model.AttemptSuccess, model.AttemptStopped, model.AttemptRefused, model.AttemptNoReading, model.AttemptMeasurementImpossible, model.AttemptRunnerDied, model.AttemptHarnessBroken, model.AttemptOutOfScope, model.AttemptBlockedMidTask} {
		t.Run(string(outcome), func(t *testing.T) {
			f, task, r := handbackControl(t)
			r.Outcome, r.CommitsDenied = outcome, true
			if outcome == model.AttemptBlockedMidTask || outcome == model.AttemptOutOfScope {
				r.Holds = []HandbackHold{{BlockerID: f.id(), Reason: model.BlockerResume, Actor: f.author, Criterion: "owner assigns work to the appropriate lane"}}
			}
			packet := captureHandback(t, f, r)
			before, _ := f.snapshot().Task(reduce.Ident{Project: f.project.ID, ID: task.ID})
			if len(before.LiveAttempts) != 1 {
				t.Fatal("capture mutated canonical state")
			}
			b := f.accept(packet)
			p, _ := f.snapshot().Task(reduce.Ident{Project: f.project.ID, ID: task.ID})
			terminal := p.Attempts[0].Terminal
			if len(p.LiveAttempts) != 0 || p.Closure != nil || terminal == nil || terminal.Outcome != outcome || terminal.Reason != r.Reason || terminal.NextAction != r.NextAction || !p.CommitsDenied || len(terminal.DeliveryRefs) != 0 {
				t.Fatalf("dishonest receipt: %+v", p)
			}
			if !reflect.DeepEqual(p.Spec.Scope, task.Spec.Scope) || len(b.Events) != 2+len(r.Holds) {
				t.Fatal("scope or transaction changed")
			}
			if outcome == model.AttemptSuccess && (p.Status != reduce.StatusBlocked || p.Reasons[0].Kind != reduce.ReasonAwaitingAcceptance) {
				t.Fatal("success prose closed the obligation")
			}
			if len(r.Holds) > 0 && (p.Status != reduce.StatusBlocked || len(p.Blockers) != 1 || !p.Blockers[0].Open() || p.Blockers[0].Held.Sequence != terminal.Origin.Sequence) {
				t.Fatal("receipt escaped its hold")
			}
			if outcome != model.AttemptSuccess && len(r.Holds) == 0 && p.Status != reduce.StatusReady {
				t.Fatal("honest non-success stranded the task")
			}
			consumer := f.task()
			consumer.Spec.Prerequisites = []model.Prerequisite{{Kind: "task-success", Target: f.ref(task.ID, 1), WaiverPolicy: "forbid"}}
			f.accept(f.capture(nil, consumer))
			dependent, _ := f.snapshot().Task(reduce.Ident{Project: f.project.ID, ID: consumer.ID})
			if dependent.Status != reduce.StatusBlocked {
				t.Fatal("terminal receipt satisfied an ordinary success dependency")
			}
			if again := captureHandback(t, f, r); again != packet {
				t.Fatal("capture retry changed receipt")
			}
			f.refuse(f.request(packet), "conflict")
		})
	}
}

func TestHandbackProseOnlyAndMissingSemanticsFail(t *testing.T) {
	f, _, control := handbackControl(t)
	captureHandback(t, f, control)
	for _, tc := range []struct{ field, value string }{
		{"outcome", ""}, {"reason", "\u200b"}, {"next_action", ""},
		{"reason", "reason-\xff"}, {"next_action", "action-\xff"}, {"reason", "truncated-\xe2\x82"},
		{"author.id", "holder-\xff"}, {"author.unknown_reason", "unknown-\xff"},
	} {
		r := control
		r.CommandID = f.id()
		*map[string]*string{"outcome": (*string)(&r.Outcome), "reason": &r.Reason, "next_action": &r.NextAction, "author.id": &r.Author.ID, "author.unknown_reason": &r.Author.UnknownReason}[tc.field] = tc.value
		_, err := Handback(context.Background(), f.project, r)
		if err == nil {
			t.Fatalf("broken %s acknowledged", tc.field)
		}
		if strings.Contains(tc.value, "-") && !strings.Contains(err.Error(), tc.field+": input contains invalid UTF-8") {
			t.Fatalf("expected field-specific UTF-8 refusal for %s: %v", tc.field, err)
		}
	}
}

func TestHandbackDurableDespiteCancellationAndAdmissionFailure(t *testing.T) {
	f, task, r := handbackControl(t)
	captureHandback(t, f, r)
	r.CommandID, r.Outcome = f.id(), model.AttemptBlockedMidTask
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	packet, err := Handback(ctx, f.project, r)
	if err != nil {
		t.Fatal(err)
	}
	f.refuse(f.request(packet), "missing-hold")
	packets, err := store.ReadIntake(f.project, []model.ID{packet.CommandID})
	if err != nil || len(packets) != 1 {
		t.Fatal("failed admission lost the receipt")
	}
	hold := &model.BlockerHold{Task: f.ref(task.ID, 1), BlockerID: f.id(), Reason: model.BlockerResume, Actor: f.author, Criterion: "resume after owner resolves the boundary"}
	b := f.accept(packet, f.capture(nil, hold))
	if len(b.Events) != 3 {
		t.Fatal("receipt, hold and review must be one bundle")
	}
}

func TestHandbackStartedAndCurrentRevisions(t *testing.T) {
	f, task, r := handbackControl(t)
	r.Outcome = model.AttemptBlockedMidTask
	r.Holds = []HandbackHold{{BlockerID: f.id(), Reason: model.BlockerResume, Actor: f.author, Criterion: "owner reviews the changed contract"}}
	packet := captureHandback(t, f, r)
	amend := &model.TaskAmend{Target: f.ref(task.ID, 1), ExpectedRevision: 1, Replacement: task.Spec, Provenance: task.Provenance}
	amend.Replacement.Intent = "contract changed while work was in flight"
	f.accept(f.capture(nil, amend))
	if again := captureHandback(t, f, r); again != packet {
		t.Fatal("amendment changed receipt identity")
	}
	b := f.accept(packet)
	e, _ := model.DecodeEvent(b.Events[0])
	packets, err := store.ReadIntake(f.project, []model.ID{packet.CommandID})
	if err != nil {
		t.Fatal(err)
	}
	original, _ := model.DecodeEvent(packets[0].Events[0])
	p, _ := f.snapshot().Task(reduce.Ident{Project: f.project.ID, ID: task.ID})
	if e.(*model.AttemptTerminal).Task.Revision != 2 || original.(*model.AttemptTerminal).Task.Revision != 1 || p.Attempts[0].TaskRevision != 1 || p.Blockers[0].TaskRevision != 2 {
		t.Fatal("starting/admission revisions were collapsed")
	}
	if e.(*model.AttemptTerminal).Reason != r.Reason || e.(*model.AttemptTerminal).NextAction != r.NextAction {
		t.Fatal("revision translation rewrote meaning")
	}
}

func TestHandbackAtomicHoldRefusals(t *testing.T) {
	for _, variant := range []string{"old", "wrong-task", "cleared", "reassignment", "reassignment-to-nobody", "scope-amendment", "wrong-author", "wrong-revision"} {
		t.Run(variant, func(t *testing.T) {
			f, task, r := handbackControl(t)
			r.Outcome = model.AttemptBlockedMidTask
			hold := &model.BlockerHold{Task: f.ref(task.ID, 1), BlockerID: f.id(), Reason: model.BlockerResume, Actor: f.author, Criterion: "owner reassigns work"}
			var extra []model.PacketRef
			code := "missing-hold"
			switch variant {
			case "old":
				f.accept(f.capture(nil, hold))
			case "wrong-task":
				other := f.goodControl()
				hold.Task = f.ref(other.ID, 1)
				extra = append(extra, f.capture(nil, hold))
			case "cleared":
				body := []byte("cleared immediately")
				extra = append(extra, f.capture([][]byte{body}, hold, &model.BlockerClear{Task: hold.Task, BlockerID: hold.BlockerID, HoldRef: model.BlockerRef{Task: hold.Task, BlockerID: hold.BlockerID}, ResolvingWitness: admissionContent(body)}))
			case "reassignment":
				r.Outcome = model.AttemptOutOfScope
				hold.Reason = model.BlockerPrerequisite
				extra = append(extra, f.capture(nil, hold))
			case "reassignment-to-nobody":
				r.Outcome = model.AttemptOutOfScope
				hold.Actor = model.Actor{UnknownReason: "no actor supplied"}
				extra = append(extra, f.capture(nil, hold))
			case "scope-amendment":
				r.Outcome = model.AttemptOutOfScope
				extra = append(extra, f.capture(nil, hold, &model.TaskAmend{Target: hold.Task, ExpectedRevision: 1, Replacement: task.Spec, Provenance: task.Provenance}))
				code = "invalid-transition"
			case "wrong-author":
				r.Author.ID = "another lane"
				code = "attribution-mismatch"
			case "wrong-revision":
				f.accept(f.capture(nil, &model.TaskAmend{Target: hold.Task, ExpectedRevision: 1, Replacement: task.Spec, Provenance: task.Provenance}))
				bad := &model.AttemptTerminal{Task: f.ref(task.ID, 2), AttemptID: r.AttemptID, Outcome: model.AttemptStopped, Reason: r.Reason, NextAction: r.NextAction, DeliveryRefs: []model.ArtifactRef{}}
				f.refuse(f.request(f.capture(nil, bad)), "revision-conflict")
				return
			}
			packet := captureHandback(t, f, r)
			f.refuse(f.request(append(extra, packet)...), code)
			p, _ := f.snapshot().Task(reduce.Ident{Project: f.project.ID, ID: task.ID})
			if len(p.LiveAttempts) != 1 {
				t.Fatal("failed bundle partially terminated attempt")
			}
		})
	}
}

func TestHandbackEnvelopeAndReconciliation(t *testing.T) {
	f, task, r := handbackControl(t)
	r.Envelope = &model.InvocationEnvelope{AttemptID: r.AttemptID, ExecutionSourceIdentity: model.ExecutionIdentity{Project: f.project.ID}}
	r.AttemptID, r.ReconciliationOwed = "", true
	packet := captureHandback(t, f, r)
	f.accept(packet)
	p, _ := f.snapshot().Task(reduce.Ident{Project: f.project.ID, ID: task.ID})
	if p.Status != reduce.StatusBlocked || p.Reasons[0].Kind != reduce.ReasonReconciliation || !p.Attempts[0].Terminal.ReconciliationOwed {
		t.Fatal("reconciliation debt disappeared")
	}
	r.AttemptID = f.id()
	if _, err := Handback(context.Background(), f.project, r); err == nil {
		t.Fatal("contradictory envelope identity accepted")
	}
}

func TestHandbackTakeoverRequiresAvailableStoppedReference(t *testing.T) {
	f, task, r := handbackControl(t)
	body := []byte("prior writer explicitly confirms it stopped")
	takeover := &model.TaskTakeover{Task: f.ref(task.ID, 1), Actor: f.author, AttemptID: f.id(), PriorAttemptID: r.AttemptID, StoppedConfirmationRef: admissionContent(body)}
	f.accept(f.capture([][]byte{body}, takeover))
	p, _ := f.snapshot().Task(reduce.Ident{Project: f.project.ID, ID: task.ID})
	if len(p.Attempts) != 2 || len(p.LiveAttempts) != 2 {
		t.Fatal("takeover fabricated the prior holder's receipt")
	}
	takeover.AttemptID, takeover.PriorAttemptID = f.id(), takeover.AttemptID
	takeover.StoppedConfirmationRef = admissionContent([]byte("unavailable confirmation"))
	before := f.snapshot().Watermark()
	if _, err := Admit(context.Background(), f.project, f.request(f.capture(nil, takeover))); err == nil {
		t.Fatal("takeover without available confirmation passed")
	}
	if f.snapshot().Watermark() != before {
		t.Fatal("failed takeover published")
	}
	takeover.StoppedConfirmationRef = model.ArtifactRef{}
	if _, err := model.EncodeEvent(takeover); err == nil {
		t.Fatal("absent explicit confirmation passed")
	}
}

func TestAdmissionStartAndBlockerTransitions(t *testing.T) {
	f := newAdmissionFixture(t)
	task := f.goodControl()
	ref := f.ref(task.ID, 1)
	hold := &model.BlockerHold{Task: ref, BlockerID: f.id(), Reason: model.BlockerPrerequisite, Actor: f.author, Criterion: "wait for a recorded witness"}
	f.accept(f.capture(nil, hold))
	if projection, _ := f.snapshot().Task(reduce.Ident{Project: f.project.ID, ID: task.ID}); projection.Status != reduce.StatusBlocked {
		t.Fatal("good hold did not block the task")
	}
	f.refuse(f.request(f.capture(nil, &model.TaskStart{Task: ref, Actor: f.author, AttemptID: f.id()})), "invalid-transition")
	witness := []byte("the prerequisite was resolved")
	clear := &model.BlockerClear{Task: ref, BlockerID: hold.BlockerID, HoldRef: model.BlockerRef{Task: ref, BlockerID: hold.BlockerID}, ResolvingWitness: admissionContent(witness)}
	clearPacket := f.capture([][]byte{witness}, clear)
	attempt := f.id()
	startPacket := f.capture(nil, &model.TaskStart{Task: ref, Actor: f.author, AttemptID: attempt})
	f.accept(startPacket, clearPacket)
	if projection, _ := f.snapshot().Task(reduce.Ident{Project: f.project.ID, ID: task.ID}); projection.Status != reduce.StatusInFlight {
		t.Fatalf("cleared task did not start: %+v", projection)
	}
	f.refuse(f.request(f.capture(nil, &model.TaskStart{Task: ref, Actor: f.author, AttemptID: f.id()})), "invalid-transition")
	other := f.task()
	f.accept(f.capture(nil, other))
	f.refuse(f.request(f.capture(nil, &model.TaskStart{Task: f.ref(other.ID, 1), Actor: f.author, AttemptID: attempt})), "conflict")
	f.refuse(f.request(f.capture([][]byte{witness}, clear)), "invalid-transition")
}

func TestAdmissionForwardBlockerReference(t *testing.T) {
	f := newAdmissionFixture(t)
	task := f.goodControl()
	ref := f.ref(task.ID, 1)
	hold := &model.BlockerHold{Task: ref, BlockerID: f.id(), Reason: model.BlockerPrerequisite, Actor: f.author, Criterion: "wait for witness"}
	witness := []byte("resolved")
	clear := &model.BlockerClear{Task: ref, BlockerID: hold.BlockerID, HoldRef: model.BlockerRef{Task: ref, BlockerID: hold.BlockerID}, ResolvingWitness: admissionContent(witness)}
	first := f.capture([][]byte{witness}, clear)
	second := f.capture(nil, hold)
	bundle := f.accept(first, second)
	if bundle.Events[0].Type != "blocker.hold" || bundle.Events[1].Type != "blocker.clear" {
		t.Fatal("forward hold reference did not sort")
	}
}

// The disabled-operation list moved to TestGateOperationsStillUnavailable (gate_family_test.go).
