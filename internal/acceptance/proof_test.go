package acceptance_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/reduce"
)

// The same admitted fixture exercises proof and the values escaping its snapshot.
func outsideProofFixture() ([]model.TypedEvent, model.InvocationEnvelope, *model.ProofAdmit) {
	c := laneEEvidenceCriterion()
	c.Claim, c.CriterionID = laneEReduceRef(3, 1), laneEReduceID(91)
	env := laneEEvidenceEnvelope(c, laneEEvidenceBody, 81)
	env.AttemptID = laneEReduceID(70)
	env.ConfigRequested = map[string]model.Scalar{"sample_count": laneEEvidenceNumber("12")}
	env.ConditionsDeclared = map[string]model.Scalar{"seed": laneEEvidenceNumber("7")}
	env.ObservedAt = laneEEvidenceUnknown[time.Time]("not launched")
	env.Outcome = laneEEvidenceUnknown[model.ProcessOutcome]("not launched")
	env.OutputRefs = laneEEvidenceUnknown[[]model.ArtifactRef]("not launched")
	env.ConfigEffective = laneEEvidenceUnknown[map[string]model.Availability[model.Scalar]]("not launched")
	env.ConditionsObserved = laneEEvidenceUnknown[map[string]model.Availability[model.Scalar]]("not launched")
	instrument := model.InstrumentSpec{
		QuestionAnswered: "does every measured pose meet the frozen threshold", BlindTo: "unmeasured poses",
		NotAnswered: "production behavior", ConfigSurface: []string{"sample_count"}, DangerousDefaults: []string{},
		ValidRange: "the fixture population", ImplementationRef: laneEReduceArtifact("instrument"),
		Validation: laneEEvidenceKnown(model.InstrumentValidation{Ref: laneEReduceArtifact("validation"), Version: "1"}),
	}
	events := []model.TypedEvent{
		laneEReduceCreate(1, laneEReduceSpec(1)),
		&model.TaskStart{Task: laneEReduceRef(1, 1), Actor: model.Actor{ID: "runner"}, AttemptID: env.AttemptID},
		&model.InstrumentDeclare{ID: laneEReduceID(4), Provenance: laneEReduceProvenance(), Spec: instrument},
		&model.ClaimAssert{ID: c.Claim.RecordID, Provenance: laneEReduceProvenance(), Spec: model.ClaimSpec{
			Assertion: "every pose is below 0.05 mm", Falsifier: "a pose reaches 0.05 mm", Scope: laneEReduceScope(), ExternalRefs: []model.ExternalReference{},
		}},
		&c,
	}
	proof := &model.ProofAdmit{Claim: c.Claim, CriterionRef: *env.CriterionRef.Value,
		Evidence: []model.ObservationDisposition{{InvocationRef: model.InvocationRef{Project: laneEReduceProject, InvocationID: env.InvocationID}, Disposition: "supports", Reason: "both measured poses satisfy the threshold"}},
		Judgment: model.ResponsibleJudgment{Actor: model.Actor{ID: "reviewer"}, Reason: "the complete fixture family satisfies its frozen criterion"},
	}
	return events, env, proof
}

func outsideProofSeal(env model.InvocationEnvelope) *model.InvocationSeal {
	env.ObservedAt = laneEEvidenceKnown(env.StartedAt.Add(time.Second))
	exit := 0
	env.Outcome = laneEEvidenceKnown(model.ProcessOutcome{Kind: "exit", ExitCode: &exit})
	env.OutputRefs = laneEEvidenceKnown([]model.ArtifactRef{laneEEvidenceContent(laneEEvidenceBody)})
	env.ConfigEffective = laneEEvidenceConfig("sample_count", "12")
	env.ConditionsObserved = laneEEvidenceConfig("seed", "7")
	return &model.InvocationSeal{StartRef: model.InvocationRef{Project: laneEReduceProject, InvocationID: env.InvocationID}, Envelope: env}
}

func outsideProofControl(t *testing.T) (reduce.Snapshot, model.Bundle) {
	t.Helper()
	events, env, proof := outsideProofFixture()
	b := laneEReduceBundle(t, model.Bundle{}, append(events, &model.InvocationStart{Envelope: env}, outsideProofSeal(env), proof)...)
	s := laneEReduceReplay(t, b)
	p, ok := s.ClaimAt(proof.Claim)
	if !ok || p.Status != reduce.StatusProven || len(p.Observations) != 1 || len(p.Proofs) != 1 {
		t.Fatalf("control frozen criterion, completed observation and responsible judgment must establish one PROVEN claim; got %+v", p)
	}
	return s, b
}

func TestProofEmptyContradictingAndUnfrozenFamiliesCannotEstablishProof(t *testing.T) {
	outsideProofControl(t)
	for _, name := range []string{"empty", "contradicting", "criterion_after_start"} {
		t.Run(name, func(t *testing.T) {
			outsideProofControl(t)
			events, env, proof := outsideProofFixture()
			if name == "criterion_after_start" {
				fix := events[len(events)-1]
				events = append(events[:len(events)-1], &model.InvocationStart{Envelope: env}, fix, outsideProofSeal(env))
			} else {
				events = append(events, &model.InvocationStart{Envelope: env}, outsideProofSeal(env))
			}
			if name == "empty" {
				proof.Evidence = []model.ObservationDisposition{}
			} else if name == "contradicting" {
				proof.Evidence[0].Disposition = "contradicts"
			}
			b := laneEReduceBundle(t, model.Bundle{}, events...)
			// Bypass only the encoder's semantic validation to exercise Replay's boundary.
			data, err := json.Marshal(proof)
			if err != nil {
				t.Fatal(err)
			}
			b.Events = append(b.Events, model.Event{Type: proof.EventType(), Data: data})
			got, err := reduce.Replay([]model.Bundle{b})
			if err == nil || !reflect.DeepEqual(got, reduce.Snapshot{}) {
				t.Fatalf("%s family must be refused without publishing a snapshot; got watermark %+v, error %v. Proof requires nonempty, uncontradicted evidence of a criterion fixed before launch", name, got.Watermark(), err)
			}
		})
	}
}

func TestProofInvocationSealMustPreserveEveryDeclaredCondition(t *testing.T) {
	outsideProofControl(t)
	events, env, _ := outsideProofFixture()
	first := laneEReduceBundle(t, model.Bundle{}, append(events, &model.InvocationStart{Envelope: env})...)
	before := laneEReduceReplay(t, first)
	control, err := reduce.Apply(before, laneEReduceBundle(t, first, outsideProofSeal(env)))
	if err != nil || control.Invocations()[0].Seal == nil {
		t.Fatalf("control seal repeating its prelaunch declaration must be accepted: %v", err)
	}
	seal := outsideProofSeal(env)
	seal.Envelope.ConditionsDeclared = map[string]model.Scalar{"seed": laneEEvidenceNumber("999")}
	after, err := reduce.Apply(before, laneEReduceBundle(t, first, seal))
	if err == nil {
		inv := after.Invocations()[0]
		t.Fatalf("seal changed declared seed from %s to %s and was admitted. It must be refused: one invocation identity cannot carry two prelaunch declarations, even if Claim later declines to count the observation", *inv.Start.ConditionsDeclared["seed"].Number, *inv.Seal.ConditionsDeclared["seed"].Number)
	}
	if !reflect.DeepEqual(after, reduce.Snapshot{}) {
		t.Fatal("refused seal published partial state")
	}
}

func TestProofRevisionAndTrustLossStayAttachedToTheirExactSubjects(t *testing.T) {
	s, first := outsideProofControl(t)
	support, ok := s.Support(laneEReduceRef(3, 1), reduce.SupportContext{EvidenceAvailable: reduce.TruthTrue, ScopeApplicable: reduce.TruthTrue})
	if !ok || support.Current() != reduce.TruthTrue {
		t.Fatalf("control verified fixture support must be current: %+v", support)
	}
	spec := model.DecisionSpec{Question: "ship this revision", Options: []string{"approve", "reject"}, WaitingActor: model.Actor{ID: "owner"}, Scope: laneEReduceScope()}
	authority := laneEReduceClose(1, 1, 1, model.ClosureSuccess).Authority
	ruling := &model.DecisionDispose{Decision: laneEReduceRef(5, 1), Disposition: "approved", Quote: "ship revision one", Scope: spec.Scope, Authority: authority}
	second := laneEReduceBundle(t, first, &model.DecisionOpen{ID: laneEReduceID(5), Provenance: laneEReduceProvenance(), Spec: spec}, ruling)
	decided := laneEReduceReplay(t, first, second)
	d, ok := decided.Decision(laneEReduceIdent(5))
	if !ok || d.Status != reduce.StatusDecided {
		t.Fatalf("control scoped ruling must settle revision 1: %+v", d)
	}
	third := laneEReduceBundle(t, second,
		&model.DecisionRevise{Target: laneEReduceRef(5, 1), ExpectedRevision: 1, Provenance: laneEReduceProvenance(), Replacement: spec},
		&model.TrustWithdraw{Instrument: laneEReduceRef(4, 1), Scope: laneEReduceScope(), RevalidationCondition: "repeat independent validation"})
	after := laneEReduceReplay(t, first, second, third)
	d, _ = after.Decision(laneEReduceIdent(5))
	if d.Status != reduce.StatusOpen || len(d.Dispositions) != 0 {
		t.Fatalf("revision 2 borrowed revision 1's disposition: %+v; a changed question needs its own ruling", d)
	}
	p, _ := after.ClaimAt(laneEReduceRef(3, 1))
	if p.Status != reduce.StatusProven || p.Support.ActiveTrust != reduce.TruthFalse {
		t.Fatalf("withdrawal must remove current trust while retaining historical PROVEN achievement: %+v", p)
	}
	// A second seal cannot overwrite the first receipt.
	_, env, _ := outsideProofFixture()
	if _, err := reduce.Apply(s, laneEReduceBundle(t, first, outsideProofSeal(env))); err == nil {
		t.Fatal("a second seal was accepted under an already sealed invocation identity; the first receipt must remain immutable")
	}
}
