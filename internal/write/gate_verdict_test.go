package write

// R14.1 through admission: every new proof states its verdict; a refuting
// proof is admitted (so it leaves intake) when its contradicting members
// really fail the criterion, and the claim projects REFUTED. The replay-side
// verdict rules are tested in internal/reduce; these reach them through Apply.

import (
	"testing"

	"datum/internal/model"
	"datum/internal/reduce"
)

func (w *proofWorld) refutation(members map[model.InvocationRef]string) *model.ProofAdmit {
	p := w.proof(w.criterion, members)
	p.Verdict = model.VerdictRefutes
	p.Judgment.Reason = "the failing run contradicts the frozen criterion"
	return p
}

func TestProofVerdictIsRequiredOnANewProof(t *testing.T) {
	w := newProofWorld(t, true)
	pass, s, e := w.run(w.criterion, proofPass)
	w.f.accept(s, e)
	legacy := w.proof(w.criterion, map[model.InvocationRef]string{pass: "supports"})
	legacy.Verdict = ""
	w.f.refuse(w.f.request(w.f.capture(nil, legacy)), "verdict-required")
	w.f.accept(w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{pass: "supports"})))
	if w.status(t) != reduce.StatusProven {
		t.Fatal("control: a supports proof with its verdict must prove the claim")
	}
}

func TestRefutingProofIsAdmittedAndProjectsRefuted(t *testing.T) {
	w := newProofWorld(t, true)
	pass, s1, e1 := w.run(w.criterion, proofPass)
	fail, s2, e2 := w.run(w.criterion, proofFail)
	w.f.accept(s1, e1, s2, e2)
	packet := w.f.capture(nil, w.refutation(map[model.InvocationRef]string{fail: "contradicts", pass: "inconclusive"}))
	w.f.accept(packet)
	if w.status(t) != reduce.StatusRefuted {
		t.Fatalf("an admitted refutation must project REFUTED, got %s", w.status(t))
	}
	review, ok := w.f.snapshot().Review(reduce.ReviewKey{Project: w.f.project.ID, CommandID: packet.CommandID})
	if !ok || review.Outcome != "accepted" {
		t.Fatalf("the refuting proof must be admitted, so it leaves intake: %+v", review)
	}
}

func TestRefutingProofRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		members    func(pass, fail model.InvocationRef) map[model.InvocationRef]string
	}{
		// A passing run named as the contradiction: nothing failed.
		{"contradiction that passes", "contradiction-unfounded", func(pass, fail model.InvocationRef) map[model.InvocationRef]string {
			return map[model.InvocationRef]string{fail: "contradicts", pass: "contradicts"}
		}},
		{"a member counted as support", "invalid-transition", func(pass, fail model.InvocationRef) map[model.InvocationRef]string {
			return map[model.InvocationRef]string{fail: "contradicts", pass: "supports"}
		}},
		{"no contradicting member", "invalid-transition", func(pass, fail model.InvocationRef) map[model.InvocationRef]string {
			return map[model.InvocationRef]string{fail: "inconclusive", pass: "inconclusive"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newProofWorld(t, true)
			pass, s1, e1 := w.run(w.criterion, proofPass)
			fail, s2, e2 := w.run(w.criterion, proofFail)
			w.f.accept(s1, e1, s2, e2)
			w.f.refuse(w.f.request(w.f.capture(nil, w.refutation(tc.members(pass, fail)))), tc.code)
			if w.status(t) != reduce.StatusMeasured {
				t.Fatalf("a refused refutation changed the claim to %s", w.status(t))
			}
		})
	}
}
