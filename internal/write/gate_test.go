package write

import (
	"context"
	"strings"
	"testing"

	"datum/internal/model"
	"datum/internal/reduce"
)

func TestAdmissionReferenceAndAttributionRefusals(t *testing.T) {
	for _, mutation := range []string{"missing-reference", "cycle", "in-packet-forward", "foreign-subject", "forged-author", "duplicate-provider"} {
		t.Run(mutation, func(t *testing.T) {
			f := newAdmissionFixture(t)
			control := f.goodControl()
			first, second := f.task(), f.task()
			var packets []model.PacketRef
			code := "unknown-reference"
			switch mutation {
			case "missing-reference":
				first.Spec.ContextRefs = []model.RecordRef{f.ref(second.ID, 1)}
				packets = []model.PacketRef{f.capture(nil, first)}
			case "cycle":
				first.Spec.ContextRefs = []model.RecordRef{f.ref(second.ID, 1)}
				second.Spec.ContextRefs = []model.RecordRef{f.ref(first.ID, 1)}
				packets = []model.PacketRef{f.capture(nil, first), f.capture(nil, second)}
				code = "dependency-cycle"
			case "in-packet-forward":
				first.Spec.ContextRefs = []model.RecordRef{f.ref(second.ID, 1)}
				packets = []model.PacketRef{f.capture(nil, first, second)}
			case "foreign-subject":
				target := f.ref(control.ID, 1)
				target.Project = "another/project"
				packets = []model.PacketRef{f.capture(nil, &model.TaskStart{Task: target, Actor: f.author, AttemptID: f.id()})}
				code = "invalid-field"
			case "forged-author":
				first.Provenance.Author.ID = "a different author"
				packets = []model.PacketRef{f.capture(nil, first)}
				code = "attribution-mismatch"
			case "duplicate-provider":
				second.ID = first.ID
				packets = []model.PacketRef{f.capture(nil, first), f.capture(nil, second)}
				code = "conflict"
			}
			f.refuse(f.request(packets...), code)
		})
	}
}

func TestAdmissionStableTieBreakAndExternalReferences(t *testing.T) {
	f := newAdmissionFixture(t)
	first, second, third := f.task(), f.task(), f.task()
	first.Spec.ContextRefs = []model.RecordRef{{Project: "elsewhere", RecordID: f.id(), Revision: 3}}
	a, b, c := f.capture(nil, first), f.capture(nil, second), f.capture(nil, third)
	bundle := f.accept(c, a, b)
	for i, want := range []model.ID{first.ID, second.ID, third.ID} {
		event, err := model.DecodeEvent(bundle.Events[i])
		if err != nil || event.(*model.TaskCreate).ID != want {
			t.Fatalf("unstable command-id tie-break at %d: %v, %v", i, event, err)
		}
	}
	if got := f.snapshot().Records()[0].Task.ContextRefs; len(got) != 1 || got[0].Project != "elsewhere" {
		t.Fatal("external unresolved reference was lost")
	}
}

func TestAdmissionOnlyFirstGateOperations(t *testing.T) {
	for _, operation := range []string{"claim", "decision", "review"} {
		t.Run(operation, func(t *testing.T) {
			f := newAdmissionFixture(t)
			control := f.goodControl()
			var event model.TypedEvent
			switch operation {
			case "claim":
				event = &model.ClaimAssert{ID: f.id(), Provenance: control.Provenance, Spec: model.ClaimSpec{Assertion: "unchecked claim", Falsifier: "a counterexample", Scope: control.Spec.Scope, ExternalRefs: []model.ExternalReference{}}}
			case "decision":
				event = &model.DecisionOpen{ID: f.id(), Provenance: control.Provenance, Spec: model.DecisionSpec{Question: "may this be enabled", Options: []string{"yes", "no"}, WaitingActor: model.Actor{ID: "owner"}, Scope: control.Spec.Scope}}
			case "review":
				event = &model.ReviewAdmit{Packets: []model.PacketRef{{CommandID: f.id(), Digest: model.Digest(strings.Repeat("a", 64))}}, Outcome: "accepted", Actor: model.Actor{ID: "owner"}, Reason: "a packet cannot mint another admission"}
			}
			f.refuse(f.request(f.capture(nil, f.task(), event)), "unavailable-until-integrated")
		})
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

func TestAdmissionExactAuthorityCarrier(t *testing.T) {
	for _, mutation := range []string{"actor-only", "wrong-speaker", "wrong-target", "wrong-scope", "wrong-pin", "missing-selector"} {
		t.Run(mutation, func(t *testing.T) {
			f := newAdmissionFixture(t)
			target := f.goodControl()
			targetRef := f.ref(target.ID, 1)
			body := []byte(`{"ruling":"waiver is permitted for this exact prerequisite"}`)
			pin := admissionContent(body)
			pin.Content.MediaType = "application/json"
			source := &model.SourceIntake{SourceID: f.id(), OriginalDigest: model.HashBytes(body), Length: uint64(len(body)), SourceRef: pin, Speaker: model.Actor{ID: "owner"}, Referents: []model.RecordRef{targetRef}}
			authority := model.Authority{Actor: source.Speaker, SourceRef: pin, Selector: model.Selector{Kind: "json-pointer", Pointer: "/ruling"}, Scope: target.Spec.Scope}
			authority.Scope.ContextRefs = []model.RecordRef{targetRef}
			consumer := f.task()
			consumer.Spec.Prerequisites = []model.Prerequisite{{Kind: "task-success", Target: targetRef, WaiverPolicy: "allow-with-authority", Authority: &authority}}
			// The first good control uses a carrier in the same bundle, even
			// though the consumer packet was authored before the source packet.
			consumerPacket := f.capture(nil, consumer)
			sourcePacket := f.capture([][]byte{body}, source)
			f.accept(consumerPacket, sourcePacket)
			admittedCarrierConsumer := f.task()
			admittedCarrierConsumer.Spec.Prerequisites = consumer.Spec.Prerequisites
			f.accept(f.capture(nil, admittedCarrierConsumer))
			bad := f.task()
			changed := authority
			bad.Spec.Prerequisites = []model.Prerequisite{{Kind: "task-success", Target: targetRef, WaiverPolicy: "allow-with-authority", Authority: &changed}}
			code := "authority-unavailable"
			switch mutation {
			case "actor-only":
				changed.SourceRef = admissionContent([]byte("an actor string does not grant authority"))
			case "wrong-speaker":
				changed.Actor.ID = "someone else"
			case "wrong-target":
				other := f.goodControl()
				bad.Spec.Prerequisites[0].Target = f.ref(other.ID, 1)
				changed.Scope.ContextRefs = []model.RecordRef{f.ref(other.ID, 1)}
			case "wrong-scope":
				changed.Scope.ContextRefs = []model.RecordRef{}
				code = "authority-scope"
			case "wrong-pin":
				changed.SourceRef = admissionContent([]byte("different words"))
			case "missing-selector":
				changed.Selector.Pointer = "/absent"
				code = "unavailable"
			}
			f.refuse(f.request(f.capture(nil, bad)), code)
		})
	}
}

func TestAdmissionUnknownIdentityIsNotSelfAdmission(t *testing.T) {
	f := newAdmissionFixture(t)
	f.author = model.Actor{UnknownReason: "author was not recorded"}
	packet := f.capture(nil, f.task())
	request := f.request(packet)
	request.Admitter = f.author
	bundle, err := Admit(context.Background(), f.project, request)
	if err != nil {
		t.Fatal(err)
	}
	event, err := model.DecodeEvent(bundle.Events[len(bundle.Events)-1])
	if err != nil || !strings.Contains(event.(*model.ReviewAdmit).Reason, "Self-admitted: unknown") {
		t.Fatalf("matching unknown reasons were mistaken for an identity: %v, %v", event, err)
	}
}
