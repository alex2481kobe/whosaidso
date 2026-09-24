package write

// Proof family tests for rejected runs: a run turned away at review is
// recorded in the ledger and must be dispositioned inapplicable or
// inconclusive by any proof of its criterion, on any machine, while the
// byte-identity rule still refuses a substitute reading. Pending intake and
// same-set ordering live in gate_family_test.go.

import (
	"context"
	"os"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
)

// A rejected contradicting run never leaves the family. The review
// records its invocation facts in the ledger, so the proof must disposition it
// inapplicable or inconclusive even on a machine whose inbox never held it, and
// a replacement seal with a different (passing) reading cannot stand in for it.
func TestProofRejectedRunStaysInTheFamily(t *testing.T) {
	for _, route := range []string{"rejected-failing-run", "correction-requested", "substitute-passing-seal", "recaptured-identical"} {
		t.Run(route, func(t *testing.T) {
			w := newProofWorld(t, true)
			pass, s1, e1 := w.run(w.criterion, proofPass)
			w.f.accept(s1, e1)
			env := w.envelope(w.criterion)
			fail := model.InvocationRef{Project: w.f.project.ID, InvocationID: env.InvocationID}
			start := w.f.capture(nil, &model.InvocationStart{Envelope: env})
			failSeal := proofSealed(env, proofFail)
			seal := w.f.capture([][]byte{[]byte(proofFail)}, failSeal)
			disposition := w.f.request(start, seal)
			disposition.Outcome = "rejected"
			if route == "correction-requested" {
				disposition.Outcome = route
			}
			if _, err := Admit(context.Background(), w.f.project, disposition); err != nil {
				t.Fatal(err)
			}
			members := map[model.InvocationRef]string{pass: "supports"}
			switch route {
			case "rejected-failing-run", "correction-requested":
				wantReviewedInvocations(t, w.f, fail.InvocationID, 2)
				// Another machine: the ledger alone, with no intake inbox at all.
				dropInbox(t, w.f)
				w.f.refuse(w.f.request(w.f.capture(nil, w.proof(w.criterion, members))), "rejected-family-member")
				for _, disposition := range []string{"supports", "contradicts"} {
					members[fail] = disposition
					w.f.refuse(w.f.request(w.f.capture(nil, w.proof(w.criterion, members))), "rejected-family-member")
				}
				if w.status(t) != reduce.StatusMeasured {
					t.Fatal("a rejected failing run was hidden by rejection")
				}
				members[fail] = "inapplicable"
				w.f.accept(w.f.capture(nil, w.proof(w.criterion, members)))
				if w.status(t) != reduce.StatusProven {
					t.Fatal("a rejected run dispositioned under the judgment did not resolve")
				}
				return
			case "substitute-passing-seal":
				// Same invocation, a passing reading nobody observed, captured fresh.
				w.f.accept(w.f.capture(nil, &model.InvocationStart{Envelope: env}), w.f.capture([][]byte{[]byte(proofPass)}, proofSealed(env, proofPass)))
				members[fail] = "supports"
				w.f.refuse(w.f.request(w.f.capture(nil, w.proof(w.criterion, members))), "rejected-family-member")
			case "recaptured-identical":
				// The same bytes admitted: the run is back in the admitted family,
				// where its failing reading is counterevidence like any other.
				w.f.accept(w.f.capture(nil, &model.InvocationStart{Envelope: env}), w.f.capture([][]byte{[]byte(proofFail)}, failSeal))
				members[fail] = "inconclusive"
				w.f.refuse(w.f.request(w.f.capture(nil, w.proof(w.criterion, members))), "counterevidence-unresolved")
			}
			if w.status(t) != reduce.StatusMeasured {
				t.Fatal("a rejected failing run was hidden by rejection")
			}
		})
	}
}

// wantReviewedInvocations asserts the ledger's review recorded n invocation
// events for id, extracted facts only.
func wantReviewedInvocations(t *testing.T, f *admissionFixture, id model.ID, n int) {
	t.Helper()
	got := 0
	for _, review := range f.snapshot().Reviews() {
		for _, fact := range review.Invocations {
			if fact.InvocationID == id && fact.CriterionRef.State == model.Known {
				got++
			}
		}
	}
	if got != n {
		t.Fatalf("ledger recorded %d invocation facts for %s, want %d", got, id, n)
	}
}

// dropInbox removes every intake packet, standing in for a machine that holds
// the same ledger but never saw the rejected packets.
func dropInbox(t *testing.T, f *admissionFixture) {
	t.Helper()
	dir, err := store.IntakeDir(f.project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
}

// A rejected run recaptured with identical bytes and admitted in the same set
// as the proof belongs to the admitted family, where it may support.
func TestProofRejectedRunReadmittedWithTheProof(t *testing.T) {
	w := newProofWorld(t, true)
	env := w.envelope(w.criterion)
	again := model.InvocationRef{Project: w.f.project.ID, InvocationID: env.InvocationID}
	reject := w.f.request(w.f.capture(nil, &model.InvocationStart{Envelope: env}), w.f.capture([][]byte{[]byte(proofPass)}, proofSealed(env, proofPass)))
	reject.Outcome = "rejected"
	if _, err := Admit(context.Background(), w.f.project, reject); err != nil {
		t.Fatal(err)
	}
	proof := w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{again: "supports"}))
	start := w.f.capture(nil, &model.InvocationStart{Envelope: env})
	seal := w.f.capture([][]byte{[]byte(proofPass)}, proofSealed(env, proofPass))
	w.f.accept(proof, start, seal)
	if w.status(t) != reduce.StatusProven {
		t.Fatal("an identical run readmitted with its proof did not prove")
	}
}

// A run launched before its criterion froze is refused, and once rejected it
// stays in that revision's family; it is resolvable by disposition.
func TestProofRefusesCriterionFixedAfterTheRun(t *testing.T) {
	w := newProofWorld(t, true)
	late := w.fixEvent(w.claim)
	lateRef := model.CriterionRef{Claim: w.claim, CriterionID: late.CriterionID, Revision: 1}
	// The run starts first and names a criterion nobody has admitted yet.
	env := w.envelope(lateRef)
	start := w.f.capture(nil, &model.InvocationStart{Envelope: env})
	seal := w.f.capture([][]byte{[]byte(proofPass)}, proofSealed(env, proofPass))
	fix := w.f.capture(nil, late)
	// In one set the gate would order the criterion first; that is not freezing.
	w.f.refuse(w.f.request(fix, start, seal), "criterion-not-frozen")
	// Admitted in its own earlier bundle, it is still later than the run.
	w.f.accept(fix)
	w.f.refuse(w.f.request(start, seal), "criterion-not-frozen")
	// A run started after the criterion is admitted is itself admissible.
	pass, s, e := w.run(lateRef, proofPass)
	w.f.accept(s, e)
	proof := w.f.capture(nil, w.proof(lateRef, map[model.InvocationRef]string{pass: "supports"}))
	// The refused run is still durable intake carrying this criterion.
	w.f.refuse(w.f.request(proof), "pending-reconciliation")
	// Rejecting it does not make it vanish: that criterion revision keeps it.
	reject := w.f.request(start, seal)
	reject.Outcome = "rejected"
	if _, err := Admit(context.Background(), w.f.project, reject); err != nil {
		t.Fatal(err)
	}
	w.f.refuse(w.f.request(proof), "rejected-family-member")
	// No longer permanently blocked. The judgment accounts for it.
	early := model.InvocationRef{Project: w.f.project.ID, InvocationID: env.InvocationID}
	w.f.refuse(w.f.request(w.f.capture(nil, w.proof(lateRef, map[model.InvocationRef]string{pass: "supports", early: "supports"}))), "rejected-family-member")
	w.f.accept(w.f.capture(nil, w.proof(lateRef, map[model.InvocationRef]string{pass: "supports", early: "inapplicable"})))
	if w.status(t) != reduce.StatusProven {
		t.Fatal("the dispositioned rejected run left the criterion revision blocked")
	}
	// The control: a criterion fixed before every run carrying it.
	clean, s2, e2 := w.run(w.criterion, proofPass)
	w.f.accept(s2, e2)
	w.f.accept(w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{clean: "supports"})))
}
