package write

// R14.1 through admission: every new proof states its verdict; a refuting
// proof is admitted (so it leaves intake) when its contradicting members
// really fail the criterion, and the claim projects REFUTED. The replay-side
// verdict rules are tested in internal/reduce; these reach them through Apply.

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
)

func (w *proofWorld) refutation(members map[model.InvocationRef]string) *model.ProofAdmit {
	p := w.proof(w.criterion, members)
	p.Verdict = model.VerdictRefutes
	p.Judgment.Reason = "the failing run contradicts the frozen criterion"
	return p
}

// R18.2: a proof without a verdict is refused by the event schema, whether the
// key is omitted or blank; it is never read as supports.
func TestProofVerdictIsRequiredOnANewProof(t *testing.T) {
	w := newProofWorld(t, true)
	pass, s, e := w.run(w.criterion, proofPass)
	w.f.accept(s, e)
	missing := w.proof(w.criterion, map[model.InvocationRef]string{pass: "supports"})
	missing.Verdict = ""
	if _, err := model.EncodeEvent(missing); err == nil {
		t.Fatal("a proof without a verdict encoded")
	}
	data, err := json.Marshal(missing)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	blank := data
	delete(fields, "verdict")
	if data, err = json.Marshal(fields); err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{data, blank} {
		ref, err := store.WriteIntake(context.Background(), w.f.project, store.IntakeRequest{CommandID: w.f.id(), Author: w.f.author,
			Events: []model.Event{{Type: "proof.admit", Data: raw}}, Blobs: []io.Reader{}})
		if err != nil {
			t.Fatal(err)
		}
		w.f.refuse(w.f.request(ref), "invalid-field")
		// An undecodable pending packet holds every later proof until it is
		// dispositioned (gatePendingIntake), so turn it away.
		reject := w.f.request(ref)
		reject.Outcome = "rejected"
		if _, err := Admit(context.Background(), w.f.project, reject); err != nil {
			t.Fatalf("the refused packet must be rejectable: %v", err)
		}
	}
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

// The path the reducer rule closes, through admission: a claim refuted on its
// current criterion revision cannot be proven again by a supports proof on a
// superseded revision whose runs passed.
func TestSupportsProofOnASupersededRevisionCannotUndoARefutation(t *testing.T) {
	w := newProofWorld(t, true)
	old, s1, e1 := w.run(w.criterion, proofPass)
	w.f.accept(s1, e1)
	rev2 := w.reviseCriterion()
	fail, s2, e2 := w.run(rev2, proofFail)
	w.f.accept(s2, e2)
	refute := w.proof(rev2, map[model.InvocationRef]string{fail: "contradicts", old: "inapplicable"})
	refute.Verdict, refute.Judgment.Reason = model.VerdictRefutes, "the failing run contradicts revision 2"
	w.f.accept(w.f.capture(nil, refute))
	if w.status(t) != reduce.StatusRefuted {
		t.Fatalf("control: the refutation on revision 2 must project REFUTED, got %s", w.status(t))
	}
	w.f.refuse(w.f.request(w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{old: "supports"}))), "invalid-transition")
	if w.status(t) != reduce.StatusRefuted {
		t.Fatalf("a proof on the superseded revision changed the claim to %s", w.status(t))
	}
}
