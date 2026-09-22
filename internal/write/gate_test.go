package write

import (
	"context"
	"encoding/json"
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
	for _, operation := range []string{"decision", "review"} {
		t.Run(operation, func(t *testing.T) {
			f := newAdmissionFixture(t)
			control := f.goodControl()
			var event model.TypedEvent
			switch operation {
			case "decision":
				event = &model.DecisionOpen{ID: f.id(), Provenance: control.Provenance, Spec: model.DecisionSpec{Question: "may this be enabled", Options: []string{"yes", "no"}, WaitingActor: model.Actor{ID: "owner"}, Scope: control.Spec.Scope}}
			case "review":
				event = &model.ReviewAdmit{Packets: []model.PacketRef{{CommandID: f.id(), Digest: model.Digest(strings.Repeat("a", 64))}}, Outcome: "accepted", Actor: model.Actor{ID: "owner"}, Reason: "a packet cannot mint another admission"}
			}
			f.refuse(f.request(f.capture(nil, f.task(), event)), "unavailable-until-integrated")
		})
	}
}

func (f *admissionFixture) claim() *model.ClaimAssert {
	return &model.ClaimAssert{ID: f.id(), Provenance: model.Provenance{Author: f.author, SourceRefs: []model.ArtifactRef{}},
		Spec: model.ClaimSpec{Assertion: "a finding awaiting measurement", Falsifier: "a counterexample", Scope: f.task().Spec.Scope, ExternalRefs: []model.ExternalReference{}}}
}

func TestAdmissionClaimRemainsUnmeasured(t *testing.T) {
	for _, tag := range []string{"", "VERIFIED", "VENDOR CLAIM", "REPORTED MEASUREMENT"} {
		t.Run("external-tag="+tag, func(t *testing.T) {
			f := newAdmissionFixture(t)
			claim := f.claim()
			if tag != "" {
				claim.Spec.ExternalRefs = []model.ExternalReference{{Tag: tag, Citation: "an external report is context, not local proof"}}
			}
			packet := f.capture(nil, claim)
			request := f.request(packet)
			request.Admitter = f.author
			if _, err := Admit(context.Background(), f.project, request); err != nil {
				t.Fatal(err)
			}
			snapshot := f.snapshot()
			got, ok := snapshot.ClaimAt(f.ref(claim.ID, 1))
			if !ok || got.Status != reduce.StatusUnmeasured || len(got.Observations) != 0 || len(got.Proofs) != 0 || got.Support.EvidenceAvailable != reduce.TruthUnknown || got.Support.ApplicableScope != reduce.TruthFalse {
				t.Fatalf("assertion acquired measurement or support: %+v", got)
			}
			review, ok := snapshot.Review(reduce.ReviewKey{Project: f.project.ID, CommandID: packet.CommandID})
			if !ok || !strings.Contains(review.Reason, "Self-admitted: true") {
				t.Fatalf("self-admission must be admitted and queryable: %+v", review)
			}
		})
	}
}

func TestGateClaimCannotInjectProofStatusOrOmitAuthor(t *testing.T) {
	for _, mutation := range []string{"status", "spec.status", "missing-author"} {
		t.Run(mutation, func(t *testing.T) {
			f := newAdmissionFixture(t)
			claim := f.claim()
			raw := admissionTestEvent(t, claim)
			var data map[string]any
			if err := json.Unmarshal(raw.Data, &data); err != nil {
				t.Fatal(err)
			}
			wantPath := "event.data." + mutation
			switch mutation {
			case "status":
				data["status"] = "PROVEN"
			case "spec.status":
				data["spec"].(map[string]any)["status"] = "PROVEN"
			case "missing-author":
				data["provenance"].(map[string]any)["author"] = map[string]any{}
				wantPath = "event.data.provenance.author"
			}
			var err error
			raw.Data, err = json.Marshal(data)
			if err != nil {
				t.Fatal(err)
			}
			// Exercise the gate's untrusted-event decode directly: the normal
			// capture encoder already refuses these malformed payloads.
			packets, err := gatePackets(f.project.ID, reduce.Snapshot{}, []model.Packet{{Project: f.project.ID, CommandID: f.id(), Author: f.author, Events: []model.Event{raw}}})
			if admissionErrorCode(err) != "invalid-field" || !strings.Contains(err.Error(), wantPath) || len(packets) != 0 {
				t.Fatalf("malformed claim must be refused at %s: packets %v, error %v", wantPath, packets, err)
			}
		})
	}
}

func TestAdmissionClaimCannotAssertProofWithoutEvidence(t *testing.T) {
	f := newAdmissionFixture(t)
	claim := f.claim()
	ref := f.ref(claim.ID, 1)
	// Syntactically valid names do not establish a frozen criterion or an
	// observation. A claim packet must not open the proof admission operation.
	proof := &model.ProofAdmit{Claim: ref, CriterionRef: model.CriterionRef{Claim: ref, CriterionID: f.id(), Revision: 1},
		Evidence: []model.ObservationDisposition{{InvocationRef: model.InvocationRef{Project: f.project.ID, InvocationID: f.id()}, Disposition: "supports", Reason: "asserted without an observation"}},
		Judgment: model.ResponsibleJudgment{Actor: f.author, Reason: "asserted proof without evidence"}}
	f.refuse(f.request(f.capture(nil, claim, proof)), "unavailable-until-integrated")
	if _, exists := f.snapshot().ClaimAt(ref); exists {
		t.Fatal("refused proof packet partially admitted its claim")
	}
	// The finding itself remains admissible after the proof attempt is refused.
	f.accept(f.capture(nil, claim))
}

func TestGateOtherOperationsRemainDisabled(t *testing.T) {
	for _, event := range []model.TypedEvent{
		&model.TaskTakeover{}, &model.AttemptTerminal{}, &model.TaskClose{},
		&model.InvocationStart{}, &model.InvocationSeal{}, &model.ClaimRevise{},
		&model.CriterionFix{}, &model.ProofAdmit{}, &model.DecisionOpen{},
		&model.DecisionRevise{}, &model.DecisionDispose{}, &model.Supersede{},
		&model.Correction{}, &model.InstrumentDeclare{}, &model.InstrumentRevise{},
		&model.TrustWithdraw{}, &model.ReviewAdmit{}, &model.ArtifactDispose{},
	} {
		t.Run(string(event.EventType()), func(t *testing.T) {
			// Isolate operation authority from schema and downstream checks;
			// a later refusal must not hide an accidentally widened allowlist.
			if err := gateOperation(event, model.Actor{ID: "owner"}); admissionErrorCode(err) != "unavailable-until-integrated" {
				t.Fatalf("operation became authorized: %v", err)
			}
		})
	}
}

func TestAdmissionClaimReferencesAndAttribution(t *testing.T) {
	for _, mutation := range []string{"scope-reference", "external-reference", "forged-author", "duplicate-claim", "duplicate-task", "cycle", "in-packet-forward"} {
		t.Run(mutation, func(t *testing.T) {
			f := newAdmissionFixture(t)
			claim, other := f.claim(), f.claim()
			var packets []model.PacketRef
			code := "unknown-reference"
			switch mutation {
			case "scope-reference":
				claim.Spec.Scope.ContextRefs = []model.RecordRef{f.ref(other.ID, 1)}
			case "external-reference":
				ref := f.ref(other.ID, 1)
				claim.Spec.ExternalRefs = []model.ExternalReference{{Tag: "VERIFIED", Citation: "missing local record", RecordRef: &ref}}
			case "forged-author":
				claim.Provenance.Author.ID = "someone else"
				code = "attribution-mismatch"
			case "duplicate-claim":
				other.ID = claim.ID
				packets = append(packets, f.capture(nil, other))
				code = "conflict"
			case "duplicate-task":
				task := f.task()
				task.ID = claim.ID
				packets = append(packets, f.capture(nil, task))
				code = "conflict"
			case "cycle":
				claim.Spec.Scope.ContextRefs = []model.RecordRef{f.ref(other.ID, 1)}
				other.Spec.Scope.ContextRefs = []model.RecordRef{f.ref(claim.ID, 1)}
				packets = append(packets, f.capture(nil, other))
				code = "dependency-cycle"
			case "in-packet-forward":
				claim.Spec.Scope.ContextRefs = []model.RecordRef{f.ref(other.ID, 1)}
				f.refuse(f.request(f.capture(nil, claim, other)), code)
				return
			}
			packets = append(packets, f.capture(nil, claim))
			f.refuse(f.request(packets...), code)
		})
	}
}

func TestAdmissionClaimForwardProviderAndUnknownAuthor(t *testing.T) {
	f := newAdmissionFixture(t)
	f.author = model.Actor{UnknownReason: "original reviewer was not recorded"}
	claim, consumer := f.claim(), f.task()
	consumer.Spec.ContextRefs = []model.RecordRef{f.ref(claim.ID, 1)}
	first, second := f.capture(nil, consumer), f.capture(nil, claim)
	bundle := f.accept(first, second)
	if bundle.Events[0].Type != "claim.assert" || bundle.Events[1].Type != "task.create" {
		t.Fatal("claim revision did not provide the forward reference")
	}
	record, ok := f.snapshot().Record(f.ref(claim.ID, 1))
	if !ok || record.Provenance.Author != f.author {
		t.Fatalf("explicit unknown provenance was lost: %+v", record)
	}
	review, ok := f.snapshot().Review(reduce.ReviewKey{Project: f.project.ID, CommandID: second.CommandID})
	if !ok || !strings.Contains(review.Reason, "Self-admitted: unknown") {
		t.Fatalf("unknown attribution invented self-admission: %+v", review)
	}
	// The same reference also resolves from an admitted snapshot.
	another := f.claim()
	ref := f.ref(claim.ID, 1)
	another.Spec.ExternalRefs = []model.ExternalReference{{Tag: "VERIFIED", Citation: "prior finding", RecordRef: &ref}}
	f.accept(f.capture(nil, another))
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
