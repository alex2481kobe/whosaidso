package reduce

// Tests that an instrument declared or revised with UNKNOWN validation stays
// UNKNOWN through replay, stored bytes, proof admission and revision.
// Refusing authored KNOWN validation is not tested here: the acceptance suite
// currently requires that form to decode and to support proof.

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
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

// Two instruments declared with UNKNOWN validation in one reviewed packet
// survive the bytes a ledger stores: each bundle is encoded and decoded back,
// incremental Apply and full replay agree, each declaration and its unknown
// reason are kept exactly, each names its packet author, and editing an
// exported copy establishes nothing.
func TestInstrumentValidationUnknownSurvivesStoredBytes(t *testing.T) {
	second := unknownInstrument()
	second.QuestionAnswered = "how long each fixture run takes"
	second.Validation.Reason = "no known-answer case exists yet"
	declared := []*model.InstrumentDeclare{
		{ID: newID("HNSA"), Provenance: provenance("author"), Spec: unknownInstrument()},
		{ID: newID("HNSB"), Provenance: provenance("author"), Spec: second},
	}
	review := &model.ReviewAdmit{Outcome: "accepted", Actor: model.Actor{ID: "reviewer"}, Reason: "declare two instruments",
		Authors: map[model.ID]model.Actor{}, CapturedAt: map[model.ID]model.Availability[time.Time]{}}
	for i := range declared {
		packet := newID(fmt.Sprintf("PKTN%d", i))
		review.Packets = append(review.Packets, model.PacketRef{CommandID: packet, Digest: newDigest(string(packet))})
		review.EventPackets = append(review.EventPackets, packet)
		review.Authors[packet] = model.Actor{ID: "author"}
		review.CapturedAt[packet] = knownAt(baseTime)
	}
	l := goodLedger(t)
	l.add(t, declared[0], declared[1], review)
	var stored []model.Bundle
	var incremental Snapshot
	for _, b := range l.bundles() {
		data, err := model.Encode(b)
		if err != nil {
			t.Fatal(err)
		}
		bundle, err := model.DecodeBundle(data)
		if err != nil || bundle.Sequence != b.Sequence {
			t.Fatalf("bundle %d does not decode at its sequence: %v", b.Sequence, err)
		}
		stored = append(stored, bundle)
		if incremental, err = Apply(incremental, bundle); err != nil {
			t.Fatalf("Apply at %d: %v", bundle.Sequence, err)
		}
	}
	replayed := mustReplay(t, stored)
	if !reflect.DeepEqual(incremental, replayed) {
		t.Fatal("incremental and full replay of the stored bytes disagree")
	}
	for _, declaration := range declared {
		target := model.RecordRef{Project: testProject, RecordID: declaration.ID, Revision: 1}
		requireUnknownInstrument(t, replayed, target)
		p, _ := replayed.InstrumentAt(target)
		if !reflect.DeepEqual(p.Spec, &declaration.Spec) {
			t.Fatal("the instrument or its unknown reason changed")
		}
		record, ok := replayed.Record(target)
		if author := replayed.EventAuthor(record.Origin).Author; !ok || author.ID != "author" {
			t.Fatalf("the instrument lost its packet author: %+v", author)
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
