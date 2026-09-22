package write

// Negative tests for proof family closure over pending intake and same-set
// ordering, R9 instrument validation (known, resolvable, revocable), and root
// containment through every operation U12 enabled. The proof fixture and the
// core proof refusals live in gate_proof_test.go.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"datum/internal/evidence"
	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

func TestProofRefusesPendingFamilyMembersUntilAdmittedTogether(t *testing.T) {
	w := newProofWorld(t, true)
	pass, s1, e1 := w.run(w.criterion, proofPass)
	w.f.accept(s1, e1)
	fail, s2, e2 := w.run(w.criterion, proofFail)
	proof := w.proof(w.criterion, map[model.InvocationRef]string{pass: "supports"})
	// Both packets of the failing run are durable intake nobody reviewed.
	w.f.refuse(w.f.request(w.f.capture(nil, proof)), "pending-reconciliation")
	// Admitting only its start leaves an admitted member with no seal.
	w.f.accept(s2)
	w.f.refuse(w.f.request(w.f.capture(nil, proof)), "invalid-transition")
	// Admitted together, the failing run is counterevidence the proof cannot hide.
	proof.Evidence = append(proof.Evidence, model.ObservationDisposition{InvocationRef: fail, Disposition: "inapplicable", Reason: "the judgment dislikes it"})
	w.f.refuse(w.f.request(e2, w.f.capture(nil, proof)), "counterevidence-unresolved")
	if w.status(t) != reduce.StatusMeasured {
		t.Fatal("a proof hid a pending failing run")
	}
}

func TestProofPendingPassingRunAdmitsInTheSameSet(t *testing.T) {
	w := newProofWorld(t, true)
	first, s1, e1 := w.run(w.criterion, proofPass)
	w.f.accept(s1, e1)
	// The proof is captured before the run's packets, so only its dependency
	// on the seal, not command-id order, can place it after that seal.
	env := w.envelope(w.criterion)
	second := model.InvocationRef{Project: w.f.project.ID, InvocationID: env.InvocationID}
	proof := w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{first: "supports", second: "supports"}))
	s2 := w.f.capture(nil, &model.InvocationStart{Envelope: env})
	e2 := w.f.capture([][]byte{[]byte(proofPass)}, proofSealed(env, proofPass))
	w.f.accept(proof, s2, e2)
	if w.status(t) != reduce.StatusProven {
		t.Fatal("a complete family admitted in one set did not reach PROVEN")
	}
}

func TestProofSameSetMemberOrderedAfterTheProofIsStillCounted(t *testing.T) {
	w := newProofWorld(t, true)
	pass, s1, e1 := w.run(w.criterion, proofPass)
	w.f.accept(s1, e1)
	// Captured first, so the gate orders the proof before the failing run it omits.
	proof := w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{pass: "supports"}))
	_, s2, e2 := w.run(w.criterion, proofFail)
	w.f.refuse(w.f.request(proof, s2, e2), "incomplete-family")
}

func TestProofWithdrawnTrustAndVanishedValidationRefuse(t *testing.T) {
	w := newProofWorld(t, true)
	pass, s, e := w.run(w.criterion, proofPass)
	w.f.accept(s, e)
	proof := w.proof(w.criterion, map[model.InvocationRef]string{pass: "supports"})
	// Validation bytes gone from the locator and the artifact store.
	validation := proofPin(`{"validated":"fixture"}`, "validation/instrument.json")
	for _, path := range []string{"validation/instrument.json", filepath.Join(evidence.DefaultArtifactDir, string(validation.Content.SHA256))} {
		if err := os.Remove(filepath.Join(w.f.project.Root, path)); err != nil {
			t.Fatal(err)
		}
	}
	w.f.refuse(w.f.request(w.f.capture(nil, proof)), "validation-unavailable")
	proofPut(t, w.f.project.Root, "validation/instrument.json", `{"validated":"fixture"}`)
	withdraw := &model.TrustWithdraw{Instrument: w.instrument, Scope: w.f.task().Spec.Scope, RevalidationCondition: "repeat the validation"}
	w.f.accept(w.f.capture(nil, withdraw))
	w.f.refuse(w.f.request(w.f.capture(nil, proof)), "invalid-transition")
}

func TestInstrumentKnownValidationMustResolve(t *testing.T) {
	body := `{"validated":"fixture"}`
	for _, route := range []string{"control", "self-admitted", "absent", "wrong-bytes", "symlink-escape", "blank-version", "revise-control", "revise-absent"} {
		t.Run(route, func(t *testing.T) {
			f := newAdmissionFixture(t)
			instrument := f.instrument()
			pin := proofPin(body, "validation/instrument.json")
			code := ""
			switch route {
			case "control", "self-admitted", "revise-control", "revise-absent":
				proofPut(t, f.project.Root, "validation/instrument.json", body)
			case "absent":
				code = "unavailable"
			case "wrong-bytes":
				proofPut(t, f.project.Root, "validation/instrument.json", `{"validated":"other"}`)
				code = "unavailable"
			case "symlink-escape":
				outside := t.TempDir()
				proofPut(t, outside, "instrument.json", body)
				if err := os.Symlink(outside, filepath.Join(f.project.Root, "validation")); err != nil {
					t.Fatal(err)
				}
				code = "unavailable"
			case "blank-version":
				proofPut(t, f.project.Root, "validation/instrument.json", body)
				code = "invalid-field"
			}
			known := proofKnown(model.InstrumentValidation{Ref: pin, Version: "v1"})
			if route == "blank-version" {
				known.Value.Version = " "
			}
			events := []model.TypedEvent{instrument}
			if route == "revise-control" || route == "revise-absent" {
				f.accept(f.capture([][]byte{[]byte("instrument implementation")}, instrument))
				replacement := instrument.Spec
				replacement.Validation = known
				if route == "revise-absent" {
					replacement.Validation = proofKnown(model.InstrumentValidation{Ref: proofPin(`{"validated":"never written"}`, "validation/missing.json"), Version: "v2"})
					code = "unavailable"
				}
				events = []model.TypedEvent{&model.InstrumentRevise{Provenance: instrument.Provenance, Target: f.ref(instrument.ID, 1), ExpectedRevision: 1, Replacement: replacement}}
			} else {
				instrument.Spec.Validation = known
			}
			var request AdmitRequest
			if route == "blank-version" {
				request = f.request(f.captureRaw(instrument))
			} else {
				request = f.request(f.capture([][]byte{[]byte("instrument implementation")}, events...))
			}
			if route == "self-admitted" {
				request.Admitter = f.author
			}
			if code != "" {
				f.refuse(request, code)
				return
			}
			bundle, err := Admit(context.Background(), f.project, request)
			if err != nil {
				t.Fatalf("resolvable known validation must admit: %v", err)
			}
			revision := model.Revision(1)
			if route == "revise-control" {
				revision = 2
			}
			got, ok := f.snapshot().InstrumentAt(f.ref(instrument.ID, revision))
			if !ok || got.Spec.Validation.State != model.Known || got.Support.ActiveTrust != reduce.TruthTrue {
				t.Fatalf("known validation did not project: %+v", got)
			}
			review, ok := f.snapshot().Review(reduce.ReviewKey{Project: f.project.ID, CommandID: request.PacketIDs[0]})
			want := model.SelfAdmissionFalse
			if route == "self-admitted" {
				want = model.SelfAdmissionTrue
			}
			if !ok || review.SelfAdmission != want || review.Origin.Sequence != bundle.Sequence {
				t.Fatalf("self-admission must stay recorded and queryable: %+v", review)
			}
		})
	}
}

// Every operation U12 opened carries artifacts. Each is aimed through a
// symlink whose target, outside the root, holds exactly the pinned bytes.
func TestNewOperationsCannotEscapeRootThroughSymlink(t *testing.T) {
	for _, door := range []string{"criterion.source", "criterion.selector", "start.input", "seal.output", "claim.revise", "instrument.revise"} {
		t.Run(door, func(t *testing.T) {
			w := newProofWorld(t, true)
			f := w.f
			body := `{"outside":"the declared root"}`
			outside := t.TempDir()
			proofPut(t, outside, "secret.json", body)
			if err := os.Symlink(filepath.Join(outside, "secret.json"), filepath.Join(f.project.Root, "escape.json")); err != nil {
				t.Fatal(err)
			}
			pin := proofPin(body, "escape.json")
			var event model.TypedEvent
			switch door {
			case "criterion.source", "criterion.selector":
				fix := w.fixEvent(w.claim)
				if door == "criterion.source" {
					fix.SourceRefs = []model.ArtifactRef{pin}
				} else {
					fix.Expression.Population.Selector = pin
				}
				event = fix
			case "start.input":
				env := w.envelope(w.criterion)
				env.InputRefs = []model.ArtifactRef{pin}
				event = &model.InvocationStart{Envelope: env}
			case "seal.output":
				env := w.envelope(w.criterion)
				f.accept(f.capture(nil, &model.InvocationStart{Envelope: env}))
				seal := proofSealed(env, proofPass)
				seal.Envelope.OutputRefs = proofKnown([]model.ArtifactRef{pin})
				event = seal
			case "claim.revise":
				claim := f.claim()
				claim.Provenance.SourceRefs = []model.ArtifactRef{pin}
				event = &model.ClaimRevise{Provenance: claim.Provenance, Target: w.claim, ExpectedRevision: 1, Replacement: claim.Spec}
			case "instrument.revise":
				spec := f.instrument().Spec
				spec.Validation = proofKnown(model.InstrumentValidation{Ref: pin, Version: "v2"})
				event = &model.InstrumentRevise{Provenance: model.Provenance{Author: f.author, SourceRefs: []model.ArtifactRef{}}, Target: w.instrument, ExpectedRevision: 1, Replacement: spec}
			}
			before := f.snapshot().Watermark()
			_, err := Admit(context.Background(), f.project, f.request(f.capture([][]byte{[]byte("instrument implementation")}, event)))
			if admissionErrorCode(err) != "unavailable" || !strings.Contains(err.Error(), "resolved outside the root") {
				t.Fatalf("%s followed a symlink out of the root: %v", door, err)
			}
			if f.snapshot().Watermark() != before {
				t.Fatal("refused escape published")
			}
		})
	}
}

func TestGateOperationsStillUnavailable(t *testing.T) {
	// Isolate the operation boundary so schema or reference failures cannot
	// conceal a widened allowlist. These stay closed until a unit opens them.
	for _, event := range []model.TypedEvent{
		&model.DecisionDispose{},
		&model.Supersede{}, &model.ReviewAdmit{}, &model.ArtifactDispose{},
	} {
		t.Run(string(event.EventType()), func(t *testing.T) {
			if err := gateOperation(event, model.Actor{ID: "author"}); admissionErrorCode(err) != "unavailable-until-integrated" {
				t.Fatalf("operation became available: %v", err)
			}
		})
	}
}

func TestProofRefusesIncomparableOrUnreadableSupport(t *testing.T) {
	for _, route := range []string{"incomparable", "unreadable"} {
		t.Run(route, func(t *testing.T) {
			w := newProofWorld(t, true)
			first, s1, e1 := w.run(w.criterion, proofPass)
			env := w.envelope(w.criterion)
			seal := proofSealed(env, proofPass)
			if route == "incomparable" {
				// Both runs pass on their own; they did not run under the same conditions.
				seed := json.Number("8")
				seal.Envelope.ConditionsObserved = proofKnown(map[string]model.Availability[model.Scalar]{"seed": proofKnown(model.Scalar{Type: "number", Number: &seed})})
			} else {
				// The output exists but not at the criterion's declared path: UNKNOWN, not TRUE.
				seal.Envelope.OutputRefs = proofKnown([]model.ArtifactRef{proofPin(proofPass, "elsewhere/result.json")})
			}
			second := model.InvocationRef{Project: w.f.project.ID, InvocationID: env.InvocationID}
			w.f.accept(s1, e1, w.f.capture(nil, &model.InvocationStart{Envelope: env}), w.f.capture([][]byte{[]byte(proofPass)}, seal))
			w.f.refuse(w.f.request(w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{first: "supports", second: "supports"}))), "criterion-unsatisfied")
			// The same member dispositioned inconclusive leaves a satisfiable family.
			w.f.accept(w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{first: "supports", second: "inconclusive"})))
			if w.status(t) != reduce.StatusProven {
				t.Fatal("control with the unusable member marked inconclusive did not prove")
			}
		})
	}
}

// A rejected contradicting run never leaves the family (R10.3). The review
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
				w.f.refuse(w.f.request(w.f.capture(nil, w.proof(w.criterion, members))), "invalid-transition")
				for _, disposition := range []string{"supports", "contradicts"} {
					members[fail] = disposition
					w.f.refuse(w.f.request(w.f.capture(nil, w.proof(w.criterion, members))), "invalid-transition")
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
