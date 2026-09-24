package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

const schemaProject ProjectID = "example/schema"

func schemaID(n int) ID { return ID(fmt.Sprintf("%026d", n)) }

func schemaRef(n int) RecordRef {
	return RecordRef{Project: schemaProject, RecordID: schemaID(n), Revision: 1}
}
func schemaKnown[T any](v T) Availability[T] { return Availability[T]{State: Known, Value: &v} }
func schemaUnknown[T any]() Availability[T] {
	return Availability[T]{State: Unknown, Reason: "not observed by this producer"}
}
func schemaNumber(s string) Scalar { n := json.Number(s); return Scalar{Type: "number", Number: &n} }
func schemaArtifact() ArtifactRef {
	return ArtifactRef{Kind: "content", Content: &ContentPin{SHA256: HashBytes([]byte("source")), Length: 6, MediaType: "application/json", Locators: []Locator{{Path: ".whosaidso/artifacts/source.json"}}}, Selector: Selector{Kind: "whole"}}
}
func schemaScope() Scope {
	return Scope{SourcePaths: []string{"internal/model"}, ContextRefs: []RecordRef{schemaRef(2)}, AppliesWhen: "the pinned source and configuration match", Limitations: "does not establish runtime behavior"}
}
func schemaAuthority() Authority {
	return Authority{Actor: Actor{ID: "owner"}, SourceRef: schemaArtifact(), Selector: Selector{Kind: "json-pointer", Pointer: "/ruling"}, Scope: schemaScope()}
}
func schemaProvenance() Provenance {
	return Provenance{SourceRefs: []ArtifactRef{schemaArtifact()}}
}
func schemaTask() TaskSpec {
	return TaskSpec{Intent: "validate authored payloads", Subject: "WhoSaidSo model", Scope: schemaScope(), NonGoals: []string{"no admission logic"}, AcceptanceCriteria: []AcceptanceCriterion{{ID: schemaID(3), Revision: 1, Criterion: "malformed payloads are refused"}}, ContextRefs: []RecordRef{schemaRef(4)}, ConstraintRefs: []RecordRef{schemaRef(5)}, Prerequisites: []Prerequisite{{Kind: "task-success", Target: schemaRef(6), WaiverPolicy: "forbid"}}, NextActor: Actor{ID: "coordinator"}, Progress: &TaskProgress{Summary: "fixtures authored", NextAction: "run them", WitnessRefs: []ArtifactRef{schemaArtifact()}}}
}
func schemaClaim() ClaimSpec {
	return ClaimSpec{Assertion: "blank blind spots are refused", Falsifier: "a whitespace-only blind_to decodes", Scope: schemaScope(), ExternalRefs: []ExternalReference{{Tag: "REPORTED MEASUREMENT", Citation: "prior audit", SourceRef: ptr(schemaArtifact()), RecordRef: ptr(schemaRef(7))}}}
}
func schemaDecision() DecisionSpec {
	return DecisionSpec{Question: "approve the bounded vocabulary?", Options: []string{"approve", "reject"}, WaitingActor: Actor{ID: "owner"}, Scope: schemaScope()}
}
func schemaInstrument() InstrumentSpec {
	return InstrumentSpec{QuestionAnswered: "are payload shapes strict?", BlindTo: "stateful admission", NotAnswered: "whether an event is applicable now", ConfigSurface: []string{"sample_count"}, DangerousDefaults: []string{"none; all observation state is explicit"}, ValidRange: "wire version 1", ImplementationRef: schemaArtifact(), Validation: schemaUnknown[InstrumentValidation]()}
}
func schemaCriterionRef() CriterionRef {
	return CriterionRef{Claim: schemaRef(1), CriterionID: schemaID(8), Revision: 2}
}
func schemaExpression() CriterionExpression {
	return CriterionExpression{ResultSelector: schemaArtifact(), Unit: "cases", Population: Population{Identity: "all declared schema cases", Selector: schemaArtifact(), Denominator: "the complete fixture set"}, Operator: Equal, Target: schemaNumber("0"), Reducer: All}
}
func schemaEnvelope() InvocationEnvelope {
	return InvocationEnvelope{InvocationID: schemaID(9), AttemptID: schemaID(10), InstrumentRef: schemaRef(11), CriterionRef: schemaKnown(schemaCriterionRef()), ExecutionSourceIdentity: ExecutionIdentity{Project: schemaProject, MachineID: schemaUnknown[ID](), SourceRefs: []ArtifactRef{schemaArtifact()}, Head: schemaUnknown[GitHead](), Dirty: schemaUnknown[bool]()}, Argv: []string{"go", "test", "./internal/model/"}, InputRefs: []ArtifactRef{schemaArtifact()}, ConfigRequested: map[string]Scalar{"sample_count": schemaNumber("0")}, ConfigEffective: schemaUnknown[map[string]Availability[Scalar]](), ConditionsDeclared: map[string]Scalar{}, ConditionsObserved: schemaUnknown[map[string]Availability[Scalar]](), Isolation: schemaUnknown[Isolation](), StartedAt: time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC), ObservedAt: schemaUnknown[time.Time](), Outcome: schemaUnknown[ProcessOutcome](), Outputs: schemaUnknown[[]RunOutput](), Visual: schemaUnknown[VisualObservation]()}
}
func ptr[T any](v T) *T { return &v }
func schemaEvents() []TypedEvent {
	seal := schemaEnvelope()
	seal.ObservedAt = schemaKnown(seal.StartedAt.Add(time.Second))
	seal.Outcome = schemaKnown(ProcessOutcome{Kind: "exit", ExitCode: ptr(0)})
	seal.Outputs = schemaKnown([]RunOutput{{Name: "out/result.json", SHA256: HashBytes([]byte("result")), Length: 6, MediaType: "application/json"}})
	seal.ConfigEffective = schemaKnown(map[string]Availability[Scalar]{"sample_count": schemaKnown(schemaNumber("0"))})
	seal.ConditionsObserved = schemaKnown(map[string]Availability[Scalar]{})
	return []TypedEvent{
		&TaskCreate{ID: schemaID(1), Spec: schemaTask(), Provenance: schemaProvenance()},
		&TaskAmend{Target: schemaRef(1), Replacement: schemaTask(), Provenance: schemaProvenance()},
		&TaskStart{Task: schemaRef(1), Actor: Actor{ID: "agent-a"}, AttemptID: schemaID(10)},
		&TaskTakeover{Task: schemaRef(1), Actor: Actor{ID: "agent-b"}, AttemptID: schemaID(10), PriorAttemptID: schemaID(12), StoppedConfirmationRef: schemaArtifact()},
		&AttemptTerminal{Task: schemaRef(1), AttemptID: schemaID(10), Outcome: AttemptRunnerDied, Reason: "observer died", NextAction: "reconcile missing terminal observation", DeliveryRefs: []ArtifactRef{}, CommitsDenied: true, ReconciliationOwed: true},
		&TaskClose{Task: schemaRef(1), Outcome: ClosureSuccess, Authority: ptr(schemaAuthority()), AcceptanceWitnessRefs: []AcceptanceWitness{{CriterionID: schemaID(3), CriterionRevision: 1, WitnessRef: schemaArtifact()}}, DeliveryWitnessRefs: []ArtifactRef{schemaArtifact()}},
		&BlockerHold{Task: schemaRef(1), BlockerID: schemaID(13), Reason: BlockerAwaitingAcceptance, Actor: Actor{ID: "owner"}, Criterion: "owner has accepted the witnessed result"},
		&BlockerClear{Task: schemaRef(1), BlockerID: schemaID(13), HoldRef: BlockerRef{Task: schemaRef(1), BlockerID: schemaID(13)}, ResolvingWitness: schemaArtifact()},
		&InvocationStart{Envelope: schemaEnvelope()},
		&InvocationSeal{StartRef: InvocationRef{Project: schemaProject, InvocationID: schemaID(9)}, Envelope: seal},
		&SourceIntake{SourceID: schemaID(14), OriginalDigest: schemaArtifact().Content.SHA256, Length: 6, SourceRef: schemaArtifact(), Speaker: Actor{UnknownReason: "original speaker was not retained"}, Order: 0, Referents: []RecordRef{schemaRef(1)}},
		&ClaimAssert{ID: schemaID(1), Spec: schemaClaim(), Provenance: schemaProvenance()},
		&ClaimRevise{Target: schemaRef(1), Replacement: schemaClaim(), Provenance: schemaProvenance()},
		&CriterionFix{Claim: schemaRef(1), CriterionID: schemaID(8), Revision: 2, Expression: schemaExpression(), Policy: EvaluationPolicy{Inclusion: "entire-criterion-family", Retry: "retain-all"}, Author: Actor{ID: "predicate-author"}, SourceRefs: []ArtifactRef{schemaArtifact()}},
		&ProofAdmit{Claim: schemaRef(1), CriterionRef: schemaCriterionRef(), Evidence: []ObservationDisposition{{InvocationRef: InvocationRef{Project: schemaProject, InvocationID: schemaID(9)}, Disposition: "supports", Reason: "every case matched the predicate"}}, Judgment: ResponsibleJudgment{Actor: Actor{ID: "coordinator"}, Reason: "the complete family supports the criterion"}, Verdict: VerdictSupports},
		&DecisionOpen{ID: schemaID(1), Spec: schemaDecision(), Provenance: schemaProvenance()},
		&DecisionRevise{Target: schemaRef(1), Replacement: schemaDecision(), Provenance: schemaProvenance()},
		&DecisionDispose{Decision: schemaRef(1), Disposition: "approved", Quote: "  Approve this scope.\n", Scope: schemaScope(), Authority: schemaAuthority()},
		&Supersede{Prior: schemaRef(1), Replacement: schemaRef(15), Reason: "new scope replaces the old ruling", Authority: ptr(schemaAuthority())},
		&Correction{Target: CorrectionTarget{Kind: "support", Support: &SupportLink{Dependent: schemaRef(1), Evidence: schemaArtifact()}}, AffectedRevisions: []RecordRef{schemaRef(1)}, Reason: "support did not test the asserted scope", CorrectiveRef: schemaArtifact()},
		&InstrumentDeclare{ID: schemaID(1), Spec: schemaInstrument(), Provenance: schemaProvenance()},
		&InstrumentRevise{Target: schemaRef(1), Replacement: schemaInstrument(), Provenance: schemaProvenance()},
		&TrustWithdraw{Instrument: schemaRef(1), Scope: schemaScope(), RevalidationCondition: "bind validation to the repaired implementation"},
		&ReviewAdmit{Packets: []PacketRef{{CommandID: schemaID(16), Digest: HashBytes([]byte("packet"))}}, Outcome: "accepted", Actor: Actor{ID: "coordinator"}, Reason: "reviewed against the current record",
			Authors: map[ID]Actor{schemaID(16): {ID: "agent-a"}}, CapturedAt: map[ID]Availability[time.Time]{schemaID(16): {State: Unknown, Reason: "not recorded"}}, EventPackets: []ID{}},
		&ArtifactDispose{Artifact: schemaArtifact(), Digest: schemaArtifact().Content.SHA256, PreviousLocation: ".whosaidso/artifacts/source.json", SupportLoss: []SupportLoss{{Target: schemaRef(1), Reason: "the observation is no longer verifiable"}}, Authority: schemaAuthority()},
	}
}

func requireSchemaGood(t *testing.T, e TypedEvent) Event {
	t.Helper()
	raw, err := EncodeEvent(e)
	if err != nil {
		t.Fatalf("good control failed: %v", err)
	}
	got, err := DecodeEvent(raw)
	if err != nil {
		t.Fatalf("good control decode failed: %v", err)
	}
	if !reflect.DeepEqual(e, got) {
		t.Fatalf("round-trip changed payload\nwant %#v\ngot %#v", e, got)
	}
	return raw
}
func requireSchemaRefusal(t *testing.T, raw Event, code string) {
	t.Helper()
	_, err := DecodeEvent(raw)
	if err == nil {
		t.Fatal("accepted invalid payload")
	}
	var f *Fault
	if !errors.As(err, &f) || f.Code != code || f.Path == "" {
		t.Fatalf("expected located %s Fault, got %v", code, err)
	}
}
func mutateSchema(t *testing.T, raw Event, at string, value any, remove bool) Event {
	t.Helper()
	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw.Data))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil {
		t.Fatal(err)
	}
	keys := strings.Split(at, ".")
	m := obj
	for _, k := range keys[:len(keys)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			t.Fatalf("fixture path %s is not an object at %s", at, k)
		}
		m = next
	}
	if remove {
		delete(m, keys[len(keys)-1])
	} else {
		m[keys[len(keys)-1]] = value
	}
	b, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	raw.Data = b
	return raw
}

func TestEveryTypedEventRoundTripAndRefusal(t *testing.T) {
	// Each event's refusal starts with its own typed positive fixture.
	bad := map[EventType]struct {
		path  string
		value any
	}{
		"task.create": {"spec.intent", " \t"}, "task.amend": {"target.revision", 0},
		"task.start": {"task.revision", 0}, "task.takeover": {"attempt_id", schemaID(12)},
		"attempt.terminal": {"outcome", "done"}, "task.close": {"outcome", "done"},
		"blocker.hold": {"reason", "whatever"}, "blocker.clear": {"hold_ref.blocker_id", schemaID(20)},
		"invocation.start": {"envelope.outcome", schemaKnown(ProcessOutcome{Kind: "exit", ExitCode: ptr(0)})},
		"invocation.seal":  {"start_ref.invocation_id", schemaID(20)},
		"source.intake":    {"length", 7}, "claim.assert": {"spec.falsifier", " "},
		"claim.revise": {"target.revision", 0}, "criterion.fix": {"expression.operator", "approximately"},
		"proof.admit": {"criterion_ref.revision", 0}, "decision.open": {"spec.question", " "},
		"decision.revise": {"target.revision", 0}, "decision.dispose": {"quote", " \n"},
		"supersede": {"prior.revision", 0}, "correction": {"target.kind", "patch"},
		"instrument.declare": {"spec.blind_to", " "}, "instrument.revise": {"replacement.blind_to", " "},
		"trust.withdraw": {"revalidation_condition", " "}, "review.admit": {"outcome", "superseded"},
		"artifact.dispose": {"digest", HashBytes([]byte("other"))},
	}
	for _, e := range schemaEvents() {
		t.Run(string(e.EventType()), func(t *testing.T) {
			raw := requireSchemaGood(t, e)
			pretty, err := Encode(raw)
			if err != nil {
				t.Fatal(err)
			}
			var wire Event
			if err := json.Unmarshal(pretty, &wire); err != nil {
				t.Fatal(err)
			}
			got, err := DecodeEvent(wire)
			if err != nil || !reflect.DeepEqual(e, got) {
				t.Fatalf("pretty round-trip: %v", err)
			}
			c, ok := bad[e.EventType()]
			if !ok {
				t.Fatal("missing event-specific negative fixture")
			}
			requireSchemaRefusal(t, mutateSchema(t, raw, c.path, c.value, false), "invalid-field")
			requireSchemaRefusal(t, mutateSchema(t, raw, "status", "PROVEN", false), "invalid-field")
			requireSchemaRefusal(t, mutateSchema(t, raw, "event_id", schemaID(20), false), "invalid-field")
			requireSchemaRefusal(t, mutateSchema(t, raw, "unexpected", true, false), "invalid-field")
		})
	}
}

func TestClosedEventSetAndReplacementOnly(t *testing.T) {
	raw := requireSchemaGood(t, schemaEvents()[0])
	for _, typ := range []EventType{"record.update", "task.patch", "reconciliation", "preservation-ready", "task.accept", "TASK.CREATE", "task.create ", ""} {
		bad := raw
		bad.Type = typ
		requireSchemaRefusal(t, bad, "unknown-event")
	}
	for _, e := range schemaEvents() {
		switch e.(type) {
		case *TaskAmend, *ClaimRevise, *DecisionRevise, *InstrumentRevise:
			t.Run(string(e.EventType()), func(t *testing.T) {
				good := requireSchemaGood(t, e)
				requireSchemaRefusal(t, mutateSchema(t, good, "replacement", map[string]any{}, false), "invalid-field")
				requireSchemaRefusal(t, mutateSchema(t, good, "patch", map[string]any{"intent": "forged"}, false), "invalid-field")
				requireSchemaRefusal(t, mutateSchema(t, good, "expected_revision", 1, false), "invalid-field")
				requireSchemaRefusal(t, mutateSchema(t, good, "target.revision", json.Number("18446744073709551615"), false), "invalid-field")
				requireSchemaRefusal(t, mutateSchema(t, good, "replacement", nil, true), "invalid-field")
			})
		}
	}
}

func TestStrictEventJSON(t *testing.T) {
	raw := requireSchemaGood(t, schemaEvents()[0])
	for name, data := range map[string][]byte{
		"duplicate": bytes.Replace(raw.Data, []byte(`"intent":`), []byte(`"intent":"forged","intent":`), 1),
		"trailing":  append(append([]byte{}, raw.Data...), []byte(` {}`)...),
	} {
		t.Run(name, func(t *testing.T) { bad := raw; bad.Data = data; requireSchemaRefusal(t, bad, "invalid-json") })
	}
	for _, data := range []string{"null", "[]", "true", "42", "\"body\""} {
		bad := raw
		bad.Data = []byte(data)
		requireSchemaRefusal(t, bad, "invalid-field")
	}
	for name, at := range map[string]string{"null spec": "spec", "missing source refs": "provenance.source_refs", "missing expected semantic": "spec.intent", "missing empty-capable array": "spec.context_refs"} {
		t.Run(name, func(t *testing.T) {
			requireSchemaRefusal(t, mutateSchema(t, raw, at, nil, name != "null spec"), "invalid-field")
		})
	}
	requireSchemaRefusal(t, mutateSchema(t, raw, "Spec", schemaTask(), false), "invalid-field")
}

func TestWritableStatusRejectedAtEveryObjectDepth(t *testing.T) {
	for _, e := range schemaEvents() {
		t.Run(string(e.EventType()), func(t *testing.T) {
			raw := requireSchemaGood(t, e)
			var tree any
			d := json.NewDecoder(bytes.NewReader(raw.Data))
			d.UseNumber()
			if err := d.Decode(&tree); err != nil {
				t.Fatal(err)
			}
			var walk func(any)
			walk = func(v any) {
				switch x := v.(type) {
				case map[string]any:
					x["status"] = "PROVEN"
					b, err := json.Marshal(tree)
					if err != nil {
						t.Fatal(err)
					}
					requireSchemaRefusal(t, Event{Type: raw.Type, Data: b}, "invalid-field")
					delete(x, "status")
					for _, child := range x {
						walk(child)
					}
				case []any:
					for _, child := range x {
						walk(child)
					}
				}
			}
			walk(tree)
		})
	}
}

func TestReferenceWalkerCoversNestedLinksAndKeepsExternal(t *testing.T) {
	e := schemaEvents()[0].(*TaskCreate)
	e.Spec.Prerequisites[0].WaiverPolicy = "allow-with-authority"
	e.Spec.Prerequisites[0].Authority = ptr(schemaAuthority())
	external := schemaRef(20)
	external.Project = "another/project"
	e.Spec.ContextRefs = append(e.Spec.ContextRefs, external)
	requireSchemaGood(t, e)
	refs, err := EventReferences(e)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"spec.scope.context_refs[0]", "spec.context_refs[0]", "spec.context_refs[1]", "spec.constraint_refs[0]", "spec.prerequisites[0].target", "spec.prerequisites[0].authority.scope.context_refs[0]"}
	var got []string
	for _, r := range refs {
		got = append(got, r.Path)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("reference paths: got %v want %v", got, want)
	}
	local, err := SameProjectReferences(e, schemaProject)
	if err != nil {
		t.Fatal(err)
	}
	if len(local) != len(refs)-1 {
		t.Fatalf("external link was lost or treated as local: %v", local)
	}
	// Config named like a reference remains an observed value, not a dependency.
	invocation := &InvocationStart{Envelope: schemaEnvelope()}
	invocation.Envelope.ConfigRequested["target"] = schemaNumber("1")
	requireSchemaGood(t, invocation)
	refs, err = EventReferences(invocation)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0].Record == nil || refs[1].Criterion == nil {
		t.Fatalf("wrong envelope links: %+v", refs)
	}
	for _, e := range schemaEvents() {
		requireSchemaGood(t, e)
		if _, err := EventReferences(e); err != nil {
			t.Fatalf("walker missing %s: %v", e.EventType(), err)
		}
	}
	requireSchemaRefusal(t, mutateSchema(t, requireSchemaGood(t, e), "spec.prerequisites", []Prerequisite{{Kind: "task-success", Target: RecordRef{Project: schemaProject, RecordID: schemaID(2)}, WaiverPolicy: "forbid"}}, false), "invalid-field")
}

func TestEveryTerminalAndDispositionRemainsRepresentable(t *testing.T) {
	e := schemaEvents()[4].(*AttemptTerminal)
	requireSchemaGood(t, e)
	for _, o := range []AttemptOutcome{AttemptSuccess, AttemptStopped, AttemptRefused, AttemptNoReading, AttemptMeasurementImpossible, AttemptRunnerDied, AttemptHarnessBroken, AttemptOutOfScope, AttemptBlockedMidTask} {
		e.Outcome = o
		requireSchemaGood(t, e)
	}
	closure := schemaEvents()[5].(*TaskClose)
	requireSchemaGood(t, closure)
	for _, o := range []ClosureOutcome{ClosureSuccess, ClosureCancelled, ClosureWithdrawn, ClosureWaived} {
		closure.Outcome = o
		requireSchemaGood(t, closure)
	}
	proof := schemaEvents()[14].(*ProofAdmit)
	requireSchemaGood(t, proof)
	for _, d := range []string{"supports", "contradicts", "inapplicable", "inconclusive"} {
		proof.Evidence[0].Disposition = d
		requireSchemaGood(t, proof)
	}
	// Schema capture preserves contradictions; U12 is responsible for refusing proof admission.
}
