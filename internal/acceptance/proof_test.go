package acceptance_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
)

// The same admitted fixture exercises proof and the values escaping its snapshot.
func outsideProofFixture() ([]model.TypedEvent, model.InvocationEnvelope, *model.ProofAdmit) {
	c := evidenceCriterion()
	c.Claim, c.CriterionID = reduceRef(3, 1), reduceID(91)
	env := evidenceEnvelope(c, evidenceBody, 81)
	env.AttemptID = reduceID(70)
	// After the criterion's bundle is recorded (12:01), before the run's packet
	// is captured.
	env.StartedAt = time.Date(2026, 9, 22, 12, 1, 10, 0, time.UTC)
	env.ConfigRequested = map[string]model.Scalar{"sample_count": evidenceNumber("12")}
	env.ConditionsDeclared = map[string]model.Scalar{"seed": evidenceNumber("7")}
	env.ObservedAt = evidenceUnknown[time.Time]("not launched")
	env.Outcome = evidenceUnknown[model.ProcessOutcome]("not launched")
	env.Outputs = evidenceUnknown[[]model.RunOutput]("not launched")
	env.ConfigEffective = evidenceUnknown[map[string]model.Availability[model.Scalar]]("not launched")
	env.ConditionsObserved = evidenceUnknown[map[string]model.Availability[model.Scalar]]("not launched")
	instrument := model.InstrumentSpec{
		QuestionAnswered: "does every measured pose meet the frozen threshold", BlindTo: "unmeasured poses",
		NotAnswered: "production behavior", ConfigSurface: []string{"sample_count"}, DangerousDefaults: []string{},
		ValidRange: "the fixture population", ImplementationRef: reduceArtifact("instrument"),
		Validation: evidenceKnown(model.InstrumentValidation{Ref: reduceArtifact("validation"), Version: "1"}),
	}
	events := []model.TypedEvent{
		reduceCreate(1, reduceSpec(1)),
		&model.TaskStart{Task: reduceRef(1, 1), Actor: model.Actor{ID: "runner"}, AttemptID: env.AttemptID},
		&model.InstrumentDeclare{ID: reduceID(4), Provenance: reduceProvenance(), Spec: instrument},
		&model.ClaimAssert{ID: c.Claim.RecordID, Provenance: reduceProvenance(), Spec: model.ClaimSpec{
			Assertion: "every pose is below 0.05 mm", Falsifier: "a pose reaches 0.05 mm", Scope: reduceScope(), ExternalRefs: []model.ExternalReference{},
		}},
		&c,
	}
	proof := &model.ProofAdmit{Claim: c.Claim, CriterionRef: *env.CriterionRef.Value,
		Evidence: []model.ObservationDisposition{{InvocationRef: model.InvocationRef{Project: reduceProject, InvocationID: env.InvocationID}, Disposition: "supports", Reason: "both measured poses satisfy the threshold"}},
		Judgment: model.ResponsibleJudgment{Actor: model.Actor{ID: "reviewer"}, Reason: "the complete fixture family satisfies its frozen criterion"},
		Verdict:  model.VerdictSupports, // Every proof states its verdict.
	}
	return events, env, proof
}

func outsideProofSeal(env model.InvocationEnvelope) *model.InvocationSeal {
	env.ObservedAt = evidenceKnown(env.StartedAt.Add(time.Second))
	exit := 0
	env.Outcome = evidenceKnown(model.ProcessOutcome{Kind: "exit", ExitCode: &exit})
	env.Outputs = evidenceKnown([]model.RunOutput{evidenceRunOutput(evidenceBody)})
	env.ConfigEffective = evidenceConfig("sample_count", "12")
	env.ConditionsObserved = evidenceConfig("seed", "7")
	return &model.InvocationSeal{StartRef: model.InvocationRef{Project: reduceProject, InvocationID: env.InvocationID}, Envelope: env}
}

// outsideProofSetup admits the fixture's records in the shape write.Admit
// publishes, each in its author's packet. withCriterion false leaves the
// criterion out, for a caller that admits it late. Otherwise
// the criterion is admitted in a bundle before the run.
func outsideProofSetup(t *testing.T, events []model.TypedEvent, withCriterion bool) model.Bundle {
	t.Helper()
	var records, starts []model.TypedEvent
	for _, e := range events {
		switch e.(type) {
		case *model.TaskStart:
			starts = append(starts, e)
		case *model.CriterionFix:
			if withCriterion {
				records = append(records, e)
			}
		default:
			records = append(records, e)
		}
	}
	at := time.Date(2026, 9, 22, 12, 0, 20, 0, time.UTC)
	return reduceAdmitted(t, model.Bundle{},
		reducePacket(t, 2101, "agent-e", at, records...), reducePacket(t, 2102, "runner", at.Add(20*time.Second), starts...))
}

// outsideProofRun admits the run and the proof after previous: the start is
// captured after it began, the seal after it observed, and the proof in its
// judge's own packet. late events (a criterion fixed after launch) ride in
// their author's packet between start and seal.
func outsideProofRun(t *testing.T, previous model.Bundle, env model.InvocationEnvelope, proof model.Event, late ...model.TypedEvent) model.Bundle {
	t.Helper()
	if !env.StartedAt.After(previous.RecordedAt) {
		t.Fatalf("fixture run starting at %s must start after the criterion bundle recorded at %s", env.StartedAt, previous.RecordedAt)
	}
	packets := []model.Packet{reducePacket(t, 2103, "runner", env.StartedAt.Add(5*time.Second), &model.InvocationStart{Envelope: env})}
	if len(late) != 0 {
		packets = append(packets, reducePacket(t, 2104, "agent-e", env.StartedAt.Add(7*time.Second), late...))
	}
	judged := reducePacket(t, 2106, "reviewer", env.StartedAt.Add(30*time.Second))
	judged.Events = append(judged.Events, proof)
	packets = append(packets, reducePacket(t, 2105, "runner", env.StartedAt.Add(10*time.Second), outsideProofSeal(env)), judged)
	return reduceAdmitted(t, previous, packets...)
}

func outsideProofEncode(t *testing.T, e model.TypedEvent) model.Event {
	t.Helper()
	raw, err := model.EncodeEvent(e)
	if err != nil {
		t.Fatalf("fixture %s must pass the strict event codec: %v", e.EventType(), err)
	}
	return raw
}

// outsideProofControl returns the PROVEN snapshot and the admitted prefix
// that produced it: setup with the criterion, then the run and its proof.
func outsideProofControl(t *testing.T) (reduce.Snapshot, []model.Bundle) {
	t.Helper()
	events, env, proof := outsideProofFixture()
	setup := outsideProofSetup(t, events, true)
	prefix := []model.Bundle{setup, outsideProofRun(t, setup, env, outsideProofEncode(t, proof))}
	s := reduceReplay(t, prefix...)
	p, ok := s.ClaimAt(proof.Claim)
	if !ok || p.Status != reduce.StatusProven || len(p.Observations) != 1 || len(p.Proofs) != 1 {
		t.Fatalf("control frozen criterion, completed observation and responsible judgment must establish one PROVEN claim; got %+v", p)
	}
	return s, prefix
}

func TestProofEmptyContradictingAndUnfrozenFamiliesCannotEstablishProof(t *testing.T) {
	outsideProofControl(t)
	for _, name := range []string{"empty", "contradicting", "criterion_after_start"} {
		t.Run(name, func(t *testing.T) {
			outsideProofControl(t)
			events, env, proof := outsideProofFixture()
			var late []model.TypedEvent
			if name == "criterion_after_start" {
				late = []model.TypedEvent{events[len(events)-1]}
			}
			if name == "empty" {
				proof.Evidence = []model.ObservationDisposition{}
			} else if name == "contradicting" {
				proof.Evidence[0].Disposition = "contradicts"
			}
			setup := outsideProofSetup(t, events, len(late) == 0)
			// Bypass only the encoder's semantic validation to exercise Replay's boundary.
			data, err := json.Marshal(proof)
			if err != nil {
				t.Fatal(err)
			}
			run := outsideProofRun(t, setup, env, model.Event{Type: proof.EventType(), Data: data}, late...)
			got, err := reduce.Replay([]model.Bundle{setup, run})
			if err == nil || !reflect.DeepEqual(got, reduce.Snapshot{}) {
				t.Fatalf("%s family must be refused without publishing a snapshot; got watermark %+v, error %v. Proof requires nonempty, uncontradicted evidence of a criterion fixed before launch", name, got.Watermark(), err)
			}
		})
	}
}

func TestProofInvocationSealMustPreserveEveryDeclaredCondition(t *testing.T) {
	outsideProofControl(t)
	events, env, _ := outsideProofFixture()
	setup := outsideProofSetup(t, events, true)
	first := reduceAdmitted(t, setup, reducePacket(t, 2103, "runner", env.StartedAt.Add(5*time.Second), &model.InvocationStart{Envelope: env}))
	before := reduceReplay(t, setup, first)
	sealed := func(seal *model.InvocationSeal) model.Bundle {
		return reduceAdmitted(t, first, reducePacket(t, 2105, "runner", env.StartedAt.Add(10*time.Second), seal))
	}
	control, err := reduce.Apply(before, sealed(outsideProofSeal(env)))
	if err != nil || control.Invocations()[0].Seal == nil {
		t.Fatalf("control seal repeating its prelaunch declaration must be accepted: %v", err)
	}
	seal := outsideProofSeal(env)
	seal.Envelope.ConditionsDeclared = map[string]model.Scalar{"seed": evidenceNumber("999")}
	after, err := reduce.Apply(before, sealed(seal))
	if err == nil {
		inv := after.Invocations()[0]
		t.Fatalf("seal changed declared seed from %s to %s and was admitted. It must be refused: one invocation identity cannot carry two prelaunch declarations, even if Claim later declines to count the observation", *inv.Start.ConditionsDeclared["seed"].Number, *inv.Seal.ConditionsDeclared["seed"].Number)
	}
	if !reflect.DeepEqual(after, reduce.Snapshot{}) {
		t.Fatal("refused seal published partial state")
	}
}

func TestProofRevisionAndTrustLossStayAttachedToTheirExactSubjects(t *testing.T) {
	s, prefix := outsideProofControl(t)
	first := prefix[len(prefix)-1]
	support, ok := s.Support(reduceRef(3, 1), reduce.SupportContext{EvidenceAvailable: reduce.TruthTrue, ScopeApplicable: reduce.TruthTrue})
	if !ok || support.Current() != reduce.TruthTrue {
		t.Fatalf("control verified fixture support must be current: %+v", support)
	}
	spec := model.DecisionSpec{Question: "ship this revision", Options: []string{"approve", "reject"}, WaitingActor: model.Actor{ID: "owner"}, Scope: reduceScope()}
	authority := *reduceClose(1, 1, 1, model.ClosureSuccess).Authority // A closure's authority is optional, so a pointer.
	ruling := &model.DecisionDispose{Decision: reduceRef(5, 1), Disposition: "approved", Quote: "ship revision one", Scope: spec.Scope, Authority: authority}
	second := reduceBundle(t, first, &model.DecisionOpen{ID: reduceID(5), Provenance: reduceProvenance(), Spec: spec}, ruling)
	decided := reduceReplay(t, append(prefix[:len(prefix):len(prefix)], second)...)
	d, ok := decided.Decision(reduceIdent(5))
	if !ok || d.Status != reduce.StatusDecided {
		t.Fatalf("control scoped ruling must settle revision 1: %+v", d)
	}
	third := reduceBundle(t, second,
		&model.DecisionRevise{Target: reduceRef(5, 1), Provenance: reduceProvenance(), Replacement: spec},
		&model.TrustWithdraw{Instrument: reduceRef(4, 1), Scope: reduceScope(), RevalidationCondition: "repeat independent validation"})
	after := reduceReplay(t, append(prefix[:len(prefix):len(prefix)], second, third)...)
	d, _ = after.Decision(reduceIdent(5))
	if d.Status != reduce.StatusOpen || len(d.Dispositions) != 0 {
		t.Fatalf("revision 2 borrowed revision 1's disposition: %+v; a changed question needs its own ruling", d)
	}
	p, _ := after.ClaimAt(reduceRef(3, 1))
	if p.Status != reduce.StatusProven || p.Support.ActiveTrust != reduce.TruthFalse {
		t.Fatalf("withdrawal must remove current trust while retaining historical PROVEN achievement: %+v", p)
	}
	// A second seal cannot overwrite the first receipt.
	_, env, _ := outsideProofFixture()
	if _, err := reduce.Apply(s, reduceAdmitted(t, first, reducePacket(t, 2107, "runner", env.StartedAt.Add(20*time.Second), outsideProofSeal(env)))); err == nil {
		t.Fatal("a second seal was accepted under an already sealed invocation identity; the first receipt must remain immutable")
	}
}
