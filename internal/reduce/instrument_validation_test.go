package reduce

// Tests that an instrument declared or revised with UNKNOWN validation stays
// UNKNOWN through replay, the committed ledger, proof admission and revision.
// Refusing authored KNOWN validation is not tested here: the acceptance suite
// currently requires that form to decode and to support proof.

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"whosaidso/internal/model"
)

func unknownInstrument() model.InstrumentSpec {
	spec := proofInstrument()
	spec.Validation = model.Availability[model.InstrumentValidation]{
		State: model.Unknown, Reason: "nobody checked this against a known-answer case",
	}
	return spec
}

func requireUnknownInstrument(t *testing.T, s Snapshot, target model.RecordRef) {
	t.Helper()
	check := func(p InstrumentProjection, ok bool) {
		t.Helper()
		if !ok || p.Spec.Validation.State != model.Unknown || model.Blank(p.Spec.Validation.Reason) {
			t.Fatalf("honest validation missing: %+v", p)
		}
		if p.Support.ActiveTrust != TruthUnknown || p.Support.Current() == TruthTrue {
			t.Fatalf("unknown instrument acquired trust: %+v", p.Support)
		}
	}
	check(s.InstrumentAt(target))
	check(s.Instrument(ident(target)))
	found := false
	for _, p := range s.Instruments() {
		if asRef(p.Instrument) == target {
			check(p, true)
			found = true
		}
	}
	if !found {
		t.Fatal("instrument missing from inventory")
	}
	for _, context := range []SupportContext{{}, {EvidenceAvailable: TruthTrue, ScopeApplicable: TruthTrue}} {
		facts, ok := s.Support(target, context)
		if !ok || facts.ActiveTrust != TruthUnknown || facts.Current() == TruthTrue {
			t.Fatalf("read-time context manufactured validation: %+v", facts)
		}
	}
}

// The committed ledger's sequence 3 declares two real instruments whose
// validation is UNKNOWN. They must decode, replay, and stay untrusted.
func TestInstrumentValidationRealSequenceThreeReplay(t *testing.T) {
	paths, err := filepath.Glob("../../.whosaidso/events/*.json")
	if err != nil || len(paths) < 3 {
		t.Fatalf("need the committed bundles through sequence 3: %v, %v", paths, err)
	}
	// The subject is sequence 3's declarations, so read the prefix through 3.
	// Later bundles (WhoSaidSo recording its own work) may revise these instruments.
	paths = paths[:3]
	var bundles []model.Bundle
	var incremental Snapshot
	var declared []*model.InstrumentDeclare
	var project model.ProjectID
	for i, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		bundle, err := model.DecodeBundle(data)
		if err != nil || bundle.Sequence != uint64(i+1) {
			t.Fatalf("%s does not decode at its sequence: %v", path, err)
		}
		bundles = append(bundles, bundle)
		if incremental, err = Apply(incremental, bundle); err != nil {
			t.Fatalf("real ledger Apply at %d: %v", bundle.Sequence, err)
		}
		if bundle.Sequence != 3 {
			continue
		}
		project = bundle.Project
		for _, raw := range bundle.Events {
			if raw.Type == "instrument.declare" {
				event, err := model.DecodeEvent(raw)
				if err != nil {
					t.Fatal(err)
				}
				declared = append(declared, event.(*model.InstrumentDeclare))
			}
		}
	}
	replayed := mustReplay(t, bundles)
	if len(declared) != 2 {
		t.Fatalf("sequence three must hold two instrument declarations, found %d", len(declared))
	}
	if !reflect.DeepEqual(incremental, replayed) {
		t.Fatal("real ledger incremental and full replay disagree")
	}
	for _, declaration := range declared {
		target := model.RecordRef{Project: project, RecordID: declaration.ID, Revision: 1}
		requireUnknownInstrument(t, replayed, target)
		p, _ := replayed.InstrumentAt(target)
		if !reflect.DeepEqual(p.Spec, &declaration.Spec) {
			t.Fatal("real instrument or its unknown reason changed")
		}
		record, ok := replayed.Record(target)
		if author := replayed.EventAuthor(record.Origin).Author; !ok || author.ID == "" {
			t.Fatalf("real instrument lost its packet author: %+v", author)
		}
		// Editing an exported copy is not an admitted validation fact.
		p.Spec.Validation = proofKnown(model.InstrumentValidation{Ref: blobRef("forged"), Version: "self-certified"})
		p.Support.ActiveTrust = TruthTrue
		requireUnknownInstrument(t, replayed, target)
	}
}

func TestInstrumentValidationUnknownDeclarationAndRevisionAdmit(t *testing.T) {
	l := newLedger()
	target := ref(newID("HNSS"), 1)
	l.add(t, &model.InstrumentDeclare{ID: target.RecordID, Provenance: provenance("author"), Spec: unknownInstrument()})
	declared := mustReplay(t, l.out)
	requireUnknownInstrument(t, declared, target)
	next, err := Apply(declared, l.add(t, &model.InstrumentRevise{Target: target, Provenance: provenance("author"), Replacement: unknownInstrument()}))
	if err != nil {
		t.Fatal(err)
	}
	requireUnknownInstrument(t, next, ref(target.RecordID, 2))
	if !reflect.DeepEqual(next, mustReplay(t, l.out)) {
		t.Fatal("incremental and full replay disagree")
	}
	requireUnknownInstrument(t, declared, target)
}

func TestInstrumentValidationUnknownObservationCannotProve(t *testing.T) {
	l := goodLedger(t)
	claim := ref(newID("CMA1"), 1)
	instrument := ref(newID("HNSS"), 1)
	l.add(t,
		&model.InstrumentDeclare{ID: instrument.RecordID, Provenance: provenance("author"), Spec: unknownInstrument()},
		&model.ClaimAssert{ID: claim.RecordID, Provenance: provenance("author"), Spec: claimSpec()},
		fixProofCriterion(claim),
	)
	env := proofEnvelope(claim, newID("RNA"))
	l.add(t, &model.InvocationStart{Envelope: env})
	l.add(t, sealProof(env, 0))
	before := mustReplay(t, l.out)
	requireUnknownInstrument(t, before, instrument)
	p := wantClaim(t, before, claim, StatusMeasured)
	if len(p.Observations) != 1 || len(p.Proofs) != 0 || p.Support.Current() == TruthTrue {
		t.Fatalf("measurement was promoted to proof: %+v", p)
	}
	after, err := Apply(before, l.add(t, admitProof(claim, env.InvocationID)))
	wantFault(t, err, CodeInvalidTransition)
	if !reflect.DeepEqual(after, Snapshot{}) {
		t.Fatal("unknown instrument established proof")
	}
	wantClaim(t, before, claim, StatusMeasured)
	requireUnknownInstrument(t, before, instrument)
}

func TestInstrumentValidationRevisionDoesNotRestoreWithdrawnTrust(t *testing.T) {
	l := newLedger()
	target := ref(newID("HNSS"), 1)
	l.add(t, &model.InstrumentDeclare{ID: target.RecordID, Provenance: provenance("author"), Spec: unknownInstrument()})
	initial := mustReplay(t, l.out)
	requireUnknownInstrument(t, initial, target)
	l.add(t, withdrawal())
	withdrawn := mustReplay(t, l.out)
	p, _ := withdrawn.InstrumentAt(target)
	if p.Support.ActiveTrust != TruthFalse || len(p.Withdrawals) != 1 {
		t.Fatalf("withdrawal lost: %+v", p)
	}
	l.add(t, &model.InstrumentRevise{Target: target, Provenance: provenance("author"), Replacement: unknownInstrument()})
	revised := mustReplay(t, l.out)
	requireUnknownInstrument(t, revised, ref(target.RecordID, 2))
	p, _ = revised.InstrumentAt(target)
	if p.Support.ActiveTrust != TruthFalse || len(p.Withdrawals) != 1 {
		t.Fatal("authored revision restored withdrawn trust")
	}
	requireUnknownInstrument(t, initial, target)
}
