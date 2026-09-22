package reduce

// Replay-time rule that a new criterion revision cannot erase known
// counterevidence: runs under earlier revisions of the same criterion stay in
// the proof family, dispositioned inapplicable or inconclusive, never support.
// Rejected-run membership lives in proof_rejected_test.go.

import (
	"testing"

	"datum/internal/model"
)

// revisedLedger admits a failing run under revision 1, then revision 2 of the
// same criterion and a passing run under it.
func revisedLedger(t *testing.T) (*ledgerBuilder, model.RecordRef, model.InvocationRef, model.CriterionRef) {
	t.Helper()
	l := goodLedger(t)
	claim := ref(newID("CMA1"), 1)
	l.add(t, &model.InstrumentDeclare{ID: newID("HNSS"), Provenance: provenance("lane-a"), Spec: proofInstrument()},
		&model.ClaimAssert{ID: claim.RecordID, Provenance: provenance("lane-a"), Spec: claimSpec()}, fixProofCriterion(claim))
	failed := proofEnvelope(claim, newID("RNA0"))
	l.add(t, &model.InvocationStart{Envelope: failed})
	l.add(t, sealProof(failed, 1))
	second := fixProofCriterion(claim)
	second.Revision = 2
	l.add(t, second)
	rev2 := model.CriterionRef{Claim: claim, CriterionID: second.CriterionID, Revision: 2}
	passed := proofEnvelope(claim, newID("RNA1"))
	passed.CriterionRef = proofKnown(rev2)
	l.add(t, &model.InvocationStart{Envelope: passed})
	l.add(t, sealProof(passed, 0))
	return l, claim, model.InvocationRef{Project: testProject, InvocationID: failed.InvocationID}, rev2
}

func revisedProof(claim model.RecordRef, rev2 model.CriterionRef) *model.ProofAdmit {
	p := admitProof(claim, newID("RNA1"))
	p.CriterionRef = rev2
	return p
}

func TestR10EarlierRevisionRunsStayInTheFamily(t *testing.T) {
	for _, disposition := range []string{"inapplicable", "inconclusive"} {
		t.Run(disposition, func(t *testing.T) {
			l, claim, failed, rev2 := revisedLedger(t)
			l.add(t, withMember(revisedProof(claim, rev2), failed, disposition))
			wantClaim(t, mustReplay(t, l.out), claim, StatusProven)
		})
	}
	for _, disposition := range []string{"omitted", "supports", "contradicts"} {
		t.Run(disposition, func(t *testing.T) {
			l, claim, failed, rev2 := revisedLedger(t)
			proof := revisedProof(claim, rev2)
			if disposition != "omitted" {
				proof = withMember(proof, failed, disposition)
			}
			l.add(t, proof)
			_, err := Replay(l.out)
			if f := wantFault(t, err, CodeInvalidTransition); f.Path != "evidence" {
				t.Fatalf("refused at %s, want evidence", f.Path)
			}
		})
	}
}

func TestR10LaterRevisionRunsAreNotEarlierFamily(t *testing.T) {
	// Revision 1's proof never needs revision 2's run, and cannot borrow it.
	l, claim, failed, _ := revisedLedger(t)
	l.add(t, withMember(admitProof(claim, newID("RNA1")), failed, "inconclusive"))
	_, err := Replay(l.out)
	wantFault(t, err, CodeInvalidTransition)
	if member, earlier := CriterionFamily(proofKnown(proofCriterion(claim)), model.CriterionRef{Claim: claim, CriterionID: newID("CRTB"), Revision: 2}); member || earlier {
		t.Fatal("a different criterion id joined the family")
	}
	other := ref(newID("CMA1"), 2)
	if member, _ := CriterionFamily(proofKnown(proofCriterion(claim)), model.CriterionRef{Claim: other, CriterionID: newID("CRTA"), Revision: 2}); member {
		t.Fatal("another claim revision's criterion joined the family")
	}
}

func TestR10RejectedRunUnderAnEarlierRevisionStaysInTheFamily(t *testing.T) {
	build := func(omit bool) (*ledgerBuilder, model.RecordRef) {
		l, claim, rejected := rejectedLedger(t, "rejected", model.CriterionRef{})
		second := fixProofCriterion(claim)
		second.Revision = 2
		l.add(t, second)
		rev2 := model.CriterionRef{Claim: claim, CriterionID: second.CriterionID, Revision: 2}
		passed := proofEnvelope(claim, newID("RNA2"))
		passed.CriterionRef = proofKnown(rev2)
		l.add(t, &model.InvocationStart{Envelope: passed})
		l.add(t, sealProof(passed, 0))
		proof := withMember(admitProof(claim, newID("RNA2")), model.InvocationRef{Project: testProject, InvocationID: newID("RNA1")}, "inapplicable")
		if !omit {
			proof = withMember(proof, rejected, "inapplicable")
		}
		proof.CriterionRef = rev2
		l.add(t, proof)
		return l, claim
	}
	l, _ := build(true)
	_, err := Replay(l.out)
	wantFault(t, err, CodeInvalidTransition)
	l, claim := build(false)
	wantClaim(t, mustReplay(t, l.out), claim, StatusProven)
}
