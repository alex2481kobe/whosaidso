package reduce

// Replay-time rules for rejected criterion family members live here:
// a run recorded by a rejected or correction-requested review must be listed by
// the proof, only as inapplicable or inconclusive, and never as support. The
// write path's extraction and byte-identity checks are tested in internal/write.

import (
	"strings"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// rejectedLedger admits a passing sealed run and records, through a rejected
// review only, a second run carrying the same criterion.
func rejectedLedger(t *testing.T, outcome string, criterion model.CriterionRef) (*ledgerBuilder, model.RecordRef, model.InvocationRef) {
	t.Helper()
	l := goodLedger(t)
	claim := ref(newID("CMA1"), 1)
	l.add(t, &model.InstrumentDeclare{ID: newID("HNSS"), Provenance: provenance("agent-a"), Spec: proofInstrument()},
		&model.ClaimAssert{ID: claim.RecordID, Provenance: provenance("agent-a"), Spec: claimSpec()}, fixProofCriterion(claim))
	passed := proofEnvelope(claim, newID("RNA1"))
	l.add(t, &model.InvocationStart{Envelope: passed})
	l.add(t, sealProof(passed, 0))
	if criterion == (model.CriterionRef{}) {
		criterion = proofCriterion(claim)
	}
	packet := model.PacketRef{CommandID: newID("PKR0"), Digest: model.HashBytes([]byte("rejected packet"))}
	l.add(t, &model.ReviewAdmit{Packets: []model.PacketRef{packet}, Outcome: outcome, Actor: model.Actor{ID: "reviewer"}, Reason: "not canonical",
		Authors: map[model.ID]model.Actor{packet.CommandID: {ID: "agent-a"}}, CapturedAt: map[model.ID]model.Availability[time.Time]{packet.CommandID: knownAt(baseTime)}, EventPackets: []model.ID{},
		Invocations: []model.ReviewedInvocation{
			{Packet: packet.CommandID, Event: "invocation.start", InvocationID: newID("RNR0"), CriterionRef: proofKnown(criterion), EnvelopeDigest: model.HashBytes([]byte("start"))},
			{Packet: packet.CommandID, Event: "invocation.seal", InvocationID: newID("RNR0"), CriterionRef: proofKnown(criterion), EnvelopeDigest: model.HashBytes([]byte("seal"))},
		}})
	return l, claim, model.InvocationRef{Project: testProject, InvocationID: newID("RNR0")}
}

func withMember(p *model.ProofAdmit, member model.InvocationRef, disposition string) *model.ProofAdmit {
	p.Evidence = append(p.Evidence, model.ObservationDisposition{InvocationRef: member, Disposition: disposition, Reason: "rejected at admission: launched before its criterion froze"})
	return p
}

func TestRejectedRunMustBeDispositionedNeverSupport(t *testing.T) {
	for _, outcome := range []string{"rejected", "correction-requested"} {
		for _, disposition := range []string{"inapplicable", "inconclusive"} {
			t.Run(outcome+"/"+disposition, func(t *testing.T) {
				l, claim, rejected := rejectedLedger(t, outcome, model.CriterionRef{})
				l.add(t, withMember(admitProof(claim, newID("RNA1")), rejected, disposition))
				wantClaim(t, mustReplay(t, l.out), claim, StatusProven)
			})
		}
		for _, disposition := range []string{"supports", "contradicts", "omitted"} {
			t.Run(outcome+"/"+disposition, func(t *testing.T) {
				l, claim, rejected := rejectedLedger(t, outcome, model.CriterionRef{})
				proof := admitProof(claim, newID("RNA1"))
				if disposition != "omitted" {
					proof = withMember(proof, rejected, disposition)
				}
				l.add(t, proof)
				_, err := Replay(l.out)
				want := map[string]string{"omitted": "evidence"}[disposition]
				if want == "" {
					want = "evidence[1].disposition"
				}
				if f := wantFault(t, err, CodeRejectedFamilyMember); f.Path != want {
					t.Fatalf("refused at %s, want %s: %v", f.Path, want, f)
				}
			})
		}
	}
}

func TestRejectedRunBelongsOnlyToItsOwnCriterion(t *testing.T) {
	claim := ref(newID("CMA1"), 1)
	other := model.CriterionRef{Claim: claim, CriterionID: newID("CRTB"), Revision: 1}
	l, claim, rejected := rejectedLedger(t, "rejected", other)
	// Not a member of this family: it needs no disposition here...
	l.add(t, admitProof(claim, newID("RNA1")))
	wantClaim(t, mustReplay(t, l.out), claim, StatusProven)
	// ...and cannot be borrowed into it.
	l2, claim2, _ := rejectedLedger(t, "rejected", other)
	l2.add(t, withMember(admitProof(claim2, newID("RNA1")), rejected, "inapplicable"))
	_, err := Replay(l2.out)
	wantFault(t, err, CodeInvalidTransition)
}

func TestOnlyAProofMayNameARejectedRun(t *testing.T) {
	l, claim, rejected := rejectedLedger(t, "rejected", model.CriterionRef{})
	seal := sealProof(proofEnvelope(claim, rejected.InvocationID), 0)
	l.add(t, seal)
	_, err := Replay(l.out)
	wantFault(t, err, CodeUnknownReference)
}

func TestAcceptedReviewIsNeverARejectedRecord(t *testing.T) {
	claim := ref(newID("CMA1"), 1)
	// An accepted review carrying invocation facts does not even validate.
	packet := model.PacketRef{CommandID: newID("PKA0"), Digest: model.HashBytes([]byte("accepted packet"))}
	_, err := model.EncodeEvent(&model.ReviewAdmit{Packets: []model.PacketRef{packet}, Outcome: "accepted", Actor: model.Actor{ID: "reviewer"}, Reason: "canonical",
		Authors: map[model.ID]model.Actor{packet.CommandID: {ID: "agent-a"}}, CapturedAt: map[model.ID]model.Availability[time.Time]{packet.CommandID: knownAt(baseTime)}, EventPackets: []model.ID{},
		Invocations: []model.ReviewedInvocation{{Packet: packet.CommandID, Event: "invocation.start", InvocationID: newID("RNA9"), CriterionRef: proofKnown(proofCriterion(claim)), EnvelopeDigest: model.HashBytes([]byte("x"))}}})
	if f, ok := err.(*model.Fault); !ok || !strings.HasSuffix(f.Path, ".invocations") {
		t.Fatalf("an accepted review recorded rejected-run facts: %v", err)
	}
}
