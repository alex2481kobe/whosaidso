package reduce

// Config names on invocation start and seal, checked against the exact
// instrument revision, live here. Seal-versus-start declaration preservation
// lives in seal_test.go; instrument validation state in
// instrument_validation_test.go.

import (
	"testing"

	"datum/internal/model"
)

// configLedger declares instrument HNSS revision 1 with one knob, "samples",
// beside claim CMA1 and its criterion, on top of goodLedger's live ATTA.
func configLedger(t *testing.T) *ledgerBuilder {
	t.Helper()
	l := goodLedger(t)
	instrument, claim := proofInstrument(), ref(newID("CMA1"), 1)
	instrument.ConfigSurface = []string{"samples"}
	l.add(t, &model.InstrumentDeclare{ID: newID("HNSS"), Provenance: provenance("lane-a"), Spec: instrument},
		&model.ClaimAssert{ID: claim.RecordID, Provenance: provenance("lane-a"), Spec: claimSpec()}, fixProofCriterion(claim))
	return l
}

func configEnvelope(id string, requested ...string) model.InvocationEnvelope {
	env := proofEnvelope(ref(newID("CMA1"), 1), newID(id))
	for _, name := range requested {
		env.ConfigRequested[name] = sealNumber("1")
	}
	return env
}

func configEffective(names ...string) model.Availability[map[string]model.Availability[model.Scalar]] {
	out := map[string]model.Availability[model.Scalar]{}
	for _, name := range names {
		out[name] = proofKnown(sealNumber("1"))
	}
	return proofKnown(out)
}

// configRefused requires the same refusal through Apply and through Replay of
// the same ledger, so neither admission nor history can hold the relationship.
func configRefused(t *testing.T, l *ledgerBuilder, event model.TypedEvent, code, path string) {
	t.Helper()
	before := mustReplay(t, l.out)
	b := l.add(t, event)
	_, err := Apply(before, b)
	if f := wantFault(t, err, code); path != "" && f.Path != path {
		t.Fatalf("Apply refused at %q, want %q: %v", f.Path, path, err)
	}
	_, err = Replay(l.out)
	if f := wantFault(t, err, code); path != "" && f.Path != path {
		t.Fatalf("Replay refused at %q, want %q: %v", f.Path, path, err)
	}
}

func TestInvocationConfigNamesTheExactInstrumentRevision(t *testing.T) {
	t.Run("control: declared names start and seal", func(t *testing.T) {
		l := configLedger(t)
		env := configEnvelope("RNC", "samples")
		l.add(t, &model.InvocationStart{Envelope: env})
		seal := sealProof(env, 0)
		seal.Envelope.ConfigEffective = configEffective("samples")
		l.add(t, seal)
		mustReplay(t, l.out)
	})
	t.Run("control: unknown effective config names nothing", func(t *testing.T) {
		l := configLedger(t)
		env := configEnvelope("RNC")
		l.add(t, &model.InvocationStart{Envelope: env}, sealProof(env, 0))
		mustReplay(t, l.out)
	})
	t.Run("start requests an undeclared knob", func(t *testing.T) {
		configRefused(t, configLedger(t), &model.InvocationStart{Envelope: configEnvelope("RNC", "mode")},
			CodeInvalidField, "envelope.config_requested.mode")
	})
	t.Run("seal observes an undeclared knob", func(t *testing.T) {
		l := configLedger(t)
		env := configEnvelope("RNC")
		l.add(t, &model.InvocationStart{Envelope: env})
		seal := sealProof(env, 0)
		seal.Envelope.ConfigEffective = configEffective("samples", "mode")
		configRefused(t, l, seal, CodeInvalidField, "envelope.config_effective.mode")
	})
	t.Run("seal omits a declared knob", func(t *testing.T) {
		l := configLedger(t)
		env := configEnvelope("RNC")
		l.add(t, &model.InvocationStart{Envelope: env})
		seal := sealProof(env, 0)
		seal.Envelope.ConfigEffective = configEffective()
		configRefused(t, l, seal, CodeInvalidField, "envelope.config_effective.samples")
	})
	t.Run("a later revision's knob does not serve an earlier revision", func(t *testing.T) {
		l := configLedger(t)
		revised := proofInstrument()
		revised.ConfigSurface = []string{"samples", "mode"}
		l.add(t, &model.InstrumentRevise{Target: ref(newID("HNSS"), 1), ExpectedRevision: 1, Provenance: provenance("lane-a"), Replacement: revised})
		atTwo := configEnvelope("RNC", "mode")
		atTwo.InstrumentRef.Revision = 2
		control := *l
		control.out = append([]model.Bundle{}, l.out...)
		control.add(t, &model.InvocationStart{Envelope: atTwo})
		mustReplay(t, control.out)
		configRefused(t, l, &model.InvocationStart{Envelope: configEnvelope("RND", "mode")},
			CodeInvalidField, "envelope.config_requested.mode")
	})
	// Moved from write's gateInvocationConfig: an unresolvable revision is the
	// reference check's answer, never a config verdict.
	t.Run("unresolvable same-project revision", func(t *testing.T) {
		env := configEnvelope("RNC", "mode")
		env.InstrumentRef.Revision = 9
		configRefused(t, configLedger(t), &model.InvocationStart{Envelope: env}, CodeUnknownReference, "")
	})
	t.Run("cross-project instrument resolves on read", func(t *testing.T) {
		l := configLedger(t)
		env := configEnvelope("RNC", "mode")
		env.InstrumentRef.Project = "foreign/project"
		l.add(t, &model.InvocationStart{Envelope: env})
		mustReplay(t, l.out)
	})
}
