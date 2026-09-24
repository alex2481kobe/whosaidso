package reduce

// R14.2's ledger half through Replay and Apply alike: a failing run of the
// proof's own criterion revision is set aside as inapplicable only beside a
// recorded code change whose From is the run's known clean head, whose paths
// lie under the claim's scope, and whose To is the clean head every supporting
// run ran at. Whether git agrees is the gate's, tested in internal/write.

import (
	"strings"
	"testing"

	"whosaidso/internal/model"
)

var (
	oldHead   = model.GitHead{ObjectFormat: "sha1", Commit: strings.Repeat("a", 40)}
	newHead   = model.GitHead{ObjectFormat: "sha1", Commit: strings.Repeat("b", 40)}
	otherHead = model.GitHead{ObjectFormat: "sha1", Commit: strings.Repeat("c", 40)}
)

func atHead(env model.InvocationEnvelope, head model.GitHead) model.InvocationEnvelope {
	env.ExecutionSourceIdentity.Head = proofKnown(head)
	env.ExecutionSourceIdentity.Dirty = proofKnown(false)
	return env
}

// codeChangeLedger admits a failing run at oldHead and a passing run at
// newHead under one criterion revision, and returns the proof that sets the
// failing run aside, for a case to spoil before it is added.
func codeChangeLedger(t *testing.T, failedEnv, passedEnv func(model.InvocationEnvelope) model.InvocationEnvelope) (*ledgerBuilder, model.RecordRef, *model.ProofAdmit) {
	t.Helper()
	l := goodLedger(t)
	claim := ref(newID("CMA1"), 1)
	l.add(t, &model.InstrumentDeclare{ID: newID("HNSS"), Provenance: provenance("lane-a"), Spec: proofInstrument()},
		&model.ClaimAssert{ID: claim.RecordID, Provenance: provenance("lane-a"), Spec: claimSpec()}, fixProofCriterion(claim))
	failed := failedEnv(atHead(proofEnvelope(claim, newID("RNA0")), oldHead))
	passed := passedEnv(atHead(proofEnvelope(claim, newID("RNA1")), newHead))
	l.add(t, &model.InvocationStart{Envelope: failed}, &model.InvocationStart{Envelope: passed})
	l.add(t, sealProof(failed, 1), sealProof(passed, 0))
	proof := admitProof(claim, passed.InvocationID)
	proof.Verdict = model.VerdictSupports
	aside := member(failed.InvocationID, "inapplicable")
	aside.CodeChange = &model.CodeChange{From: oldHead, To: newHead, ChangedPaths: []string{"internal/reduce/reduce.go"}}
	proof.Evidence = append(proof.Evidence, aside)
	return l, claim, proof
}

func same(env model.InvocationEnvelope) model.InvocationEnvelope { return env }

func TestCodeChangeSetsAFailingRunAside(t *testing.T) {
	l, claim, proof := codeChangeLedger(t, same, same)
	l.add(t, proof)
	wantClaim(t, wantBoth(t, l, ""), claim, StatusProven)
}

func TestCodeChangeRefusals(t *testing.T) {
	unknownHead := func(env model.InvocationEnvelope) model.InvocationEnvelope {
		env.ExecutionSourceIdentity.Head = proofUnknown[model.GitHead]()
		return env
	}
	dirty := func(env model.InvocationEnvelope) model.InvocationEnvelope {
		env.ExecutionSourceIdentity.Dirty = proofKnown(true)
		return env
	}
	dirtyUnknown := func(env model.InvocationEnvelope) model.InvocationEnvelope {
		env.ExecutionSourceIdentity.Dirty = proofUnknown[bool]()
		return env
	}
	elsewhere := func(env model.InvocationEnvelope) model.InvocationEnvelope { return atHead(env, otherHead) }
	for _, tc := range []struct {
		name           string
		failed, passed func(model.InvocationEnvelope) model.InvocationEnvelope
		spoil          func(p *model.ProofAdmit)
	}{
		{"the failing run's head is unknown", unknownHead, same, nil},
		{"the failing run's checkout was dirty", dirty, same, nil},
		{"the failing run's checkout state is unknown", dirtyUnknown, same, nil},
		{"from is not the run's commit", same, same, func(p *model.ProofAdmit) { p.Evidence[1].CodeChange.From = otherHead }},
		{"a changed path outside the scope", same, same, func(p *model.ProofAdmit) {
			p.Evidence[1].CodeChange.ChangedPaths = []string{"internal/write/gate.go"}
		}},
		{"a sibling that only shares the scope path's prefix", same, same, func(p *model.ProofAdmit) {
			p.Evidence[1].CodeChange.ChangedPaths = []string{"internal/reduce/reduce.go.orig"}
		}},
		{"one in-scope path beside one outside", same, same, func(p *model.ProofAdmit) {
			p.Evidence[1].CodeChange.ChangedPaths = []string{"internal/reduce/reduce.go", "internal/write/gate.go"}
		}},
		{"the supporting run ran at another commit", same, elsewhere, nil},
		{"the supporting run's head is unknown", same, unknownHead, nil},
		{"a refuting proof sets a run aside", same, same, func(p *model.ProofAdmit) {
			p.Verdict = model.VerdictRefutes
			p.Evidence[0].Disposition = "contradicts"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, claim, proof := codeChangeLedger(t, tc.failed, tc.passed)
			if tc.spoil != nil {
				tc.spoil(proof)
			}
			l.add(t, proof)
			wantBoth(t, l, CodeCodeChange)
			if s, err := Replay(l.out[:len(l.out)-1]); err != nil {
				t.Fatal(err)
			} else {
				wantClaim(t, s, claim, StatusMeasured)
			}
		})
	}
}

// Without a code change, a failing run of the same revision stays
// counterevidence: cited as "contradicts" it leaves the claim unproven. (Set
// aside without one, only the gate can see it fails; internal/write tests it.)
func TestWithoutACodeChangeTheRunStaysCounterevidence(t *testing.T) {
	l, _, proof := codeChangeLedger(t, same, same)
	proof.Evidence[1].Disposition = "contradicts"
	proof.Evidence[1].CodeChange = nil
	l.add(t, proof)
	wantBoth(t, l, CodeInvalidTransition)
}

func TestCodeChangeOnlyForAnExactRevisionRun(t *testing.T) {
	l, claim, proof := codeChangeLedger(t, same, same)
	l.add(t, criterionRevision(claim, 2))
	next := atHead(proofEnvelope(claim, newID("RNA3")), newHead)
	two := proofCriterion(claim)
	two.Revision = 2
	next.CriterionRef = proofKnown(two)
	l.add(t, &model.InvocationStart{Envelope: next})
	l.add(t, sealProof(next, 0))
	proof.CriterionRef = two
	proof.Evidence = append(proof.Evidence, member(next.InvocationID, "supports"))
	proof.Evidence[0].Disposition = "inapplicable"
	l.add(t, proof)
	wantBoth(t, l, CodeCodeChange)
}
