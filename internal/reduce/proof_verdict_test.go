package reduce

// R14.1 proof verdicts through Replay and Apply alike: a refutation lists the
// contradicting runs of the current criterion revision, counts nothing as
// support, projects REFUTED, and a later supports proof under a new revision
// proves the claim again. A proof with no verdict is refused.
// Whether a member really fails its criterion is artifact evaluation, tested
// in internal/write.

import (
	"errors"
	"strings"
	"testing"

	"datum/internal/model"
)

func refutation(claim model.RecordRef, members ...model.ObservationDisposition) *model.ProofAdmit {
	p := admitProof(claim, newID("RNA1"))
	p.Verdict, p.Evidence = model.VerdictRefutes, members
	return p
}

func criterionRevision(claim model.RecordRef, revision model.Revision) *model.CriterionFix {
	fix := fixProofCriterion(claim)
	fix.Revision = revision
	return fix
}

func TestR141RefutationProjectsRefutedAndEstablishesNothing(t *testing.T) {
	l, claim, failed, _ := familyLedger(t, true)
	l.add(t, refutation(claim, member(failed.InvocationID, "contradicts"), member(newID("RNA1"), "inconclusive")))
	s := wantBoth(t, l, "")
	p := wantClaim(t, s, claim, StatusRefuted)
	if len(p.Proofs) != 1 || !p.Proofs[0].Admission.Refutes() {
		t.Fatalf("the refutation is not recorded as the proof in force: %+v", p.Proofs)
	}
	if got := supportNow(t, s, claim); got.ApplicableScope != TruthFalse || got.Current() != TruthFalse {
		t.Fatalf("a refuted claim reads as supported: %+v", got)
	}
	if truth, _ := s.inner().claimProof(claim); truth != TruthFalse {
		t.Fatalf("a claim-proof prerequisite on a refuted claim is %s, want FALSE", truth)
	}
}

// R18.2: a proof without a verdict is refused, never read as supports: the
// event cannot be encoded, and bytes that omit it do not replay.
func TestR141ProofWithoutVerdictIsRefused(t *testing.T) {
	l, claim, failed, _ := familyLedger(t, true)
	proof := admitProof(claim, newID("RNA1"))
	proof.Evidence = append(proof.Evidence, member(failed.InvocationID, "inconclusive"))
	l.add(t, proof)
	wantClaim(t, wantBoth(t, l, ""), claim, StatusProven) // control: the same proof with its verdict proves
	proof.Verdict = ""
	if _, err := model.EncodeEvent(proof); err == nil {
		t.Fatal("a proof without a verdict encoded")
	}
	last := &l.out[len(l.out)-1]
	for i := range last.Events {
		if last.Events[i].Type != "proof.admit" {
			continue
		}
		data := strings.Replace(string(last.Events[i].Data), `,"verdict":"supports"`, "", 1)
		if data == string(last.Events[i].Data) {
			t.Fatalf("control: the encoded proof carries no verdict to strip: %s", data)
		}
		last.Events[i].Data = []byte(data)
	}
	if _, err := Replay(l.out); err == nil {
		t.Fatal("a proof without a verdict replayed")
	}
}

func TestR141RefutationRefusals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		members func(failed model.ID) []model.ObservationDisposition
		prefix  func(l *ledgerBuilder, claim model.RecordRef)
	}{
		{"a member counted as support", func(failed model.ID) []model.ObservationDisposition {
			return []model.ObservationDisposition{member(failed, "contradicts"), member(newID("RNA1"), "supports")}
		}, nil},
		{"no contradicting member", func(failed model.ID) []model.ObservationDisposition {
			return []model.ObservationDisposition{member(failed, "inconclusive"), member(newID("RNA1"), "inapplicable")}
		}, nil},
		{"a later criterion revision is current", func(failed model.ID) []model.ObservationDisposition {
			return []model.ObservationDisposition{member(failed, "contradicts"), member(newID("RNA1"), "inconclusive")}
		}, func(l *ledgerBuilder, claim model.RecordRef) { l.add(t, criterionRevision(claim, 2)) }},
		{"the contradicting run lost its instrument's trust", func(failed model.ID) []model.ObservationDisposition {
			return []model.ObservationDisposition{member(failed, "contradicts"), member(newID("RNA1"), "inconclusive")}
		}, func(l *ledgerBuilder, _ model.RecordRef) { l.add(t, withdrawal()) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, claim, failed, _ := familyLedger(t, true)
			if tc.prefix != nil {
				tc.prefix(l, claim)
			}
			l.add(t, refutation(claim, tc.members(failed.InvocationID)...))
			wantBoth(t, l, CodeInvalidTransition)
		})
	}
}

// R10.2: the contradicting run stays counterevidence for its own revision; a
// new criterion revision overcomes it, and its later supports proof is in force.
func TestR141NewCriterionRevisionProvesARefutedClaim(t *testing.T) {
	l, claim, failed, _ := familyLedger(t, true)
	l.add(t, refutation(claim, member(failed.InvocationID, "contradicts"), member(newID("RNA1"), "inconclusive")))
	wantClaim(t, mustReplay(t, l.out), claim, StatusRefuted)
	// Control for "same revision": a supports proof cannot resolve the contradiction.
	same := admitProof(claim, newID("RNA1"))
	same.Verdict = model.VerdictSupports
	same.Evidence = append(same.Evidence, member(failed.InvocationID, "contradicts"))
	l.add(t, same)
	wantBoth(t, l, CodeInvalidTransition)
	l.out = l.out[:len(l.out)-1]
	l.seq, l.prev = l.out[len(l.out)-1].Sequence, l.out[len(l.out)-1].CommandID

	l.add(t, criterionRevision(claim, 2))
	next := proofEnvelope(claim, newID("RNA3"))
	two := proofCriterion(claim)
	two.Revision = 2
	next.CriterionRef = proofKnown(two)
	l.add(t, &model.InvocationStart{Envelope: next})
	l.add(t, sealProof(next, 0))
	again := admitProof(claim, next.InvocationID)
	again.Verdict, again.CriterionRef = model.VerdictSupports, two
	again.Evidence = append(again.Evidence, member(failed.InvocationID, "inapplicable"), member(newID("RNA1"), "inapplicable"))
	l.add(t, again)
	p := wantClaim(t, wantBoth(t, l, ""), claim, StatusProven)
	if len(p.Proofs) != 2 || !p.Proofs[0].Admission.Refutes() {
		t.Fatalf("the refutation must stay visible beside the later proof: %+v", p.Proofs)
	}
}

// Every proof, supports or refutes, judges the claim's current criterion
// revision. A supports proof on a superseded revision would otherwise be the
// latest proof and flip a REFUTED claim back to PROVEN with no new evidence.
func TestProofOnASupersededCriterionRevisionIsRefusedForEitherVerdict(t *testing.T) {
	stale := func(claim model.RecordRef, failed model.ID) *model.ProofAdmit {
		p := admitProof(claim, newID("RNA1"))
		p.Verdict = model.VerdictSupports
		p.Evidence = append(p.Evidence, member(failed, "inconclusive"))
		return p
	}
	// Control: the same supports proof, before any later revision, is admitted.
	l, claim, failed, _ := familyLedger(t, true)
	l.add(t, stale(claim, failed.InvocationID))
	wantClaim(t, wantBoth(t, l, ""), claim, StatusProven)

	l, claim, failed, _ = familyLedger(t, true)
	l.add(t, refutation(claim, member(failed.InvocationID, "contradicts"), member(newID("RNA1"), "inconclusive")))
	l.add(t, criterionRevision(claim, 2))
	wantClaim(t, mustReplay(t, l.out), claim, StatusRefuted)
	l.add(t, stale(claim, failed.InvocationID))
	wantBoth(t, l, CodeInvalidTransition)
	last := len(l.out) - 1
	for name, err := range map[string]error{"replay": replayErr(l.out), "apply": applyErr(t, l)} {
		var f *model.Fault
		if !errors.As(err, &f) || !strings.HasSuffix(f.Path, "criterion_ref.revision") {
			t.Errorf("%s: the refusal must be the superseded revision, got %v", name, err)
		}
	}
	l.out = l.out[:last]
	wantClaim(t, mustReplay(t, l.out), claim, StatusRefuted)
}

func replayErr(out []model.Bundle) error {
	_, err := Replay(out)
	return err
}

func applyErr(t *testing.T, l *ledgerBuilder) error {
	last := len(l.out) - 1
	_, err := Apply(mustReplay(t, l.out[:last]), l.out[last])
	return err
}
