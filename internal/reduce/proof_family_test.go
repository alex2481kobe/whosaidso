package reduce

// Replay-time closure of a proof's evaluation family lives here: omitted and
// unsealed members of the criterion family. Artifact evaluation, pending intake
// and judgment attribution are gate work and are tested in internal/write.

import (
	"testing"

	"datum/internal/model"
)

func familyLedger(t *testing.T, sealSecond bool) (*ledgerBuilder, model.RecordRef, model.InvocationEnvelope, model.InvocationEnvelope) {
	t.Helper()
	l := goodLedger(t)
	claim := ref(newID("CMA1"), 1)
	l.add(t, &model.InstrumentDeclare{ID: newID("HNSS"), Provenance: provenance("lane-a"), Spec: proofInstrument()},
		&model.ClaimAssert{ID: claim.RecordID, Provenance: provenance("lane-a"), Spec: claimSpec()}, fixProofCriterion(claim))
	failed, passed := proofEnvelope(claim, newID("RNA0")), proofEnvelope(claim, newID("RNA1"))
	l.add(t, &model.InvocationStart{Envelope: failed}, &model.InvocationStart{Envelope: passed})
	l.add(t, sealProof(passed, 0))
	if sealSecond {
		l.add(t, sealProof(failed, 1))
	}
	return l, claim, failed, passed
}

func TestU12ProofMustListTheWholeAdmittedFamily(t *testing.T) {
	l, claim, failed, _ := familyLedger(t, true)
	// Control: listing the failed run too, dispositioned, admits.
	control := admitProof(claim, newID("RNA1"))
	control.Evidence = append([]model.ObservationDisposition{{InvocationRef: model.InvocationRef{Project: testProject, InvocationID: failed.InvocationID}, Disposition: "inconclusive", Reason: "the judgment reviewed it"}}, control.Evidence...)
	l.add(t, control)
	wantClaim(t, mustReplay(t, l.out), claim, StatusProven)
	// Cherry-picking the passing run alone must be refused on replay.
	l2, claim2, _, _ := familyLedger(t, true)
	l2.add(t, admitProof(claim2, newID("RNA1")))
	_, err := Replay(l2.out)
	if f := wantFault(t, err, CodeInvalidTransition); f.Path != "evidence" {
		t.Fatalf("cherry-picked proof refused at %s, want evidence", f.Path)
	}
}

func TestU12ProofWaitsForAnUnsealedFamilyMember(t *testing.T) {
	l, claim, failed, _ := familyLedger(t, false)
	proof := admitProof(claim, newID("RNA1"))
	proof.Evidence = append(proof.Evidence, model.ObservationDisposition{InvocationRef: model.InvocationRef{Project: testProject, InvocationID: failed.InvocationID}, Disposition: "inconclusive", Reason: "still running"})
	l.add(t, proof)
	if _, err := Replay(l.out); err == nil {
		t.Fatal("a proof was admitted while a family member had no seal")
	}
	// Omitting the unsealed member is refused too: absence of a seal is not absence of a member.
	l2, claim2, _, _ := familyLedger(t, false)
	l2.add(t, admitProof(claim2, newID("RNA1")))
	if _, err := Replay(l2.out); err == nil {
		t.Fatal("a proof omitting an unsealed family member was admitted")
	}
}
