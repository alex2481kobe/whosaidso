package reduce

// Seal declaration preservation and permitted observation changes live here.

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"whosaidso/internal/model"
)

func sealNumber(s string) model.Scalar {
	n := json.Number(s)
	return model.Scalar{Type: "number", Number: &n}
}

func sealStart(t *testing.T) (*ledgerBuilder, model.InvocationEnvelope, Snapshot) {
	t.Helper()
	l := configLedger(t) // declares the "samples" knob the run requests
	env := proofEnvelope(ref(newID("CMA1"), 1), newID("RNA"))
	env.Argv = []string{"fixture-runner", "--seed", "7"}
	env.InputRefs = []model.ArtifactRef{blobRef("input")}
	env.ConfigRequested = map[string]model.Scalar{"samples": sealNumber("12")}
	env.ConditionsDeclared = map[string]model.Scalar{"seed": sealNumber("7")}
	l.add(t, &model.InvocationStart{Envelope: env})
	return l, env, mustReplay(t, l.out)
}

func TestSealPreservesDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		change     func(*model.InvocationEnvelope)
	}{
		{"attempt", "attempt_id", func(e *model.InvocationEnvelope) { e.AttemptID = newID("ATTB") }},
		{"instrument", "instrument_ref", func(e *model.InvocationEnvelope) { e.InstrumentRef.Project = "foreign/project" }},
		{"criterion", "criterion_ref", func(e *model.InvocationEnvelope) { e.CriterionRef.Value.Claim.Project = "foreign/project" }},
		{"erase criterion", "criterion_ref", func(e *model.InvocationEnvelope) { e.CriterionRef = proofUnknown[model.CriterionRef]() }},
		{"machine", "execution_source_identity", func(e *model.InvocationEnvelope) { e.ExecutionSourceIdentity.MachineID = proofKnown(newID("MACH")) }},
		{"source", "execution_source_identity", func(e *model.InvocationEnvelope) { e.ExecutionSourceIdentity.SourceRefs[0] = blobRef("other-source") }},
		{"head", "execution_source_identity", func(e *model.InvocationEnvelope) {
			e.ExecutionSourceIdentity.Head = proofKnown(model.GitHead{ObjectFormat: "sha1", Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
		}},
		{"dirty", "execution_source_identity", func(e *model.InvocationEnvelope) { e.ExecutionSourceIdentity.Dirty = proofKnown(true) }},
		{"argv value", "argv", func(e *model.InvocationEnvelope) { e.Argv[2] = "999" }},
		{"argv order", "argv", func(e *model.InvocationEnvelope) { e.Argv[1], e.Argv[2] = e.Argv[2], e.Argv[1] }},
		{"input", "input_refs", func(e *model.InvocationEnvelope) { e.InputRefs[0] = blobRef("other-input") }},
		{"requested value", "config_requested", func(e *model.InvocationEnvelope) { e.ConfigRequested["samples"] = sealNumber("99") }},
		{"requested removal", "config_requested", func(e *model.InvocationEnvelope) { delete(e.ConfigRequested, "samples") }},
		{"requested addition", "config_requested", func(e *model.InvocationEnvelope) { e.ConfigRequested["extra"] = sealNumber("1") }},
		{"declared value", "conditions_declared", func(e *model.InvocationEnvelope) { e.ConditionsDeclared["seed"] = sealNumber("999") }},
		{"declared removal", "conditions_declared", func(e *model.InvocationEnvelope) { delete(e.ConditionsDeclared, "seed") }},
		{"declared addition", "conditions_declared", func(e *model.InvocationEnvelope) { e.ConditionsDeclared["extra"] = sealNumber("1") }},
		{"start time", "started_at", func(e *model.InvocationEnvelope) { e.StartedAt = e.StartedAt.Add(-time.Second) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, env, before := sealStart(t)
			want := before.Invocations()
			seal := sealProof(deepCopy(env), 0)
			tc.change(&seal.Envelope)
			b := l.add(t, seal)
			after, err := Apply(before, b)
			f := wantFault(t, err, CodeInvalidField)
			if f.Path != "envelope."+tc.path || f.Sequence != b.Sequence || f.EventIndex != 0 {
				t.Fatalf("wrong refusal location: %+v", f)
			}
			if !reflect.DeepEqual(after, Snapshot{}) || !reflect.DeepEqual(before.Invocations(), want) {
				t.Fatal("refusal published partial state or changed the original snapshot")
			}
		})
	}
}

func TestSealCannotSupplyPreviouslyUnknownCriterion(t *testing.T) {
	l, env, _ := sealStart(t)
	l.out, l.seq = l.out[:3], 3
	l.prev = l.out[2].CommandID
	env.CriterionRef = proofUnknown[model.CriterionRef]()
	l.add(t, &model.InvocationStart{Envelope: env})
	before := mustReplay(t, l.out)
	seal := sealProof(env, 0)
	seal.Envelope.CriterionRef = proofKnown(proofCriterion(ref(newID("CMA1"), 1)))
	_, err := Apply(before, l.add(t, seal))
	if f := wantFault(t, err, CodeInvalidField); f.Path != "envelope.criterion_ref" {
		t.Fatalf("wrong field: %+v", f)
	}
}

func TestSealMayAddObservationsThatDifferFromDeclarations(t *testing.T) {
	l, env, before := sealStart(t)
	seal := sealProof(deepCopy(env), 7)
	seal.Envelope.ConfigEffective = proofKnown(map[string]model.Availability[model.Scalar]{"samples": proofKnown(sealNumber("99"))})
	seal.Envelope.ConditionsObserved = proofKnown(map[string]model.Availability[model.Scalar]{"seed": proofKnown(sealNumber("999"))})
	seal.Envelope.Isolation = proofKnown(model.IsolationClean)
	seal.Envelope.Visual = proofKnown(model.VisualObservation{
		Framing: proofUnknown[model.VisualFraming](), Transforms: proofUnknown[model.ArtifactRef](),
		Clip: proofKnown("observed-clip"), Time: proofUnknown[json.Number](), Seed: proofKnown("999"),
		Backend: proofKnown("cpu"), Appearance: proofUnknown[model.VisualAppearance](), Limits: proofUnknown[model.VisualLimits](),
	})
	// A different zone spelling of the same instant is no changed launch time.
	seal.Envelope.StartedAt = env.StartedAt.In(time.FixedZone("same-instant", 37*60))
	after, err := Apply(before, l.add(t, seal))
	if err != nil {
		t.Fatal(err)
	}
	got := after.Invocations()[0]
	if got.Seal == nil || !reflect.DeepEqual(got.Start, before.Invocations()[0].Start) {
		t.Fatal("accepted observations changed the declaration or lost the seal")
	}
	if *(*got.Seal.ConditionsObserved.Value)["seed"].Value.Number != "999" ||
		*(*got.Seal.ConfigEffective.Value)["samples"].Value.Number != "99" ||
		*got.Seal.Isolation.Value != model.IsolationClean || *got.Seal.Visual.Value.Seed.Value != "999" ||
		*got.Seal.Outcome.Value.ExitCode != 7 || got.Seal.ObservedAt.State != model.Known || got.Seal.Outputs.State != model.Known {
		t.Fatal("seal did not retain the run's observations")
	}
}

func TestSealMayRetainUnknownObservations(t *testing.T) {
	l, env, before := sealStart(t)
	env.Outcome.Reason = "observer died before collecting a receipt"
	after, err := Apply(before, l.add(t, &model.InvocationSeal{
		StartRef: model.InvocationRef{Project: testProject, InvocationID: env.InvocationID}, Envelope: env,
	}))
	if err != nil || after.Invocations()[0].Seal.Outcome.State != model.Unknown {
		t.Fatalf("unknown observation must remain representable: %v", err)
	}
}

func TestSealIdentityMustMatchItsStartLink(t *testing.T) {
	_, env, _ := sealStart(t)
	seal := sealProof(env, 0)
	seal.Envelope.InvocationID = newID("RNB")
	_, err := model.EncodeEvent(seal)
	if err == nil {
		t.Fatal("model admitted an invocation identity contradicting its start link")
	}
}
