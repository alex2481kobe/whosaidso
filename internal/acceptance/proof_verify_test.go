package acceptance_test

// Independent review of U12's proof gate: run-directory binding of criterion
// contract paths, rejected family members, correction, reconciliation of a
// dead runner, and a real `datum run` through fresh processes. Every scenario
// is driven through the public capture/admit/reconcile API or the built CLI,
// and every refusal is preceded by a control that the same fixture admits.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
	"datum/internal/write"
)

const (
	pvPass = `{"results":{"unit":"mm","population":"pose sweep","denominator":"poses","values":[0.0100,0.0200]},"population":{"population":"pose sweep","denominator":"poses","values":["pose-a","pose-b"]}}`
	pvFail = `{"results":{"unit":"mm","population":"pose sweep","denominator":"poses","values":[0.0100,0.0900]},"population":{"population":"pose sweep","denominator":"poses","values":["pose-a","pose-b"]}}`
)

type pvWorld struct {
	t          *testing.T
	p          store.Project
	n          int
	lane       model.Actor
	claim      model.RecordRef
	instrument model.RecordRef
	attempt    model.ID
	criterion  model.CriterionRef
}

func pvPin(body []byte, paths ...string) model.ArtifactRef {
	locators := []model.Locator{}
	for _, p := range paths {
		locators = append(locators, model.Locator{Path: p})
	}
	return model.ArtifactRef{Kind: "content", Content: &model.ContentPin{SHA256: model.HashBytes(body), Length: uint64(len(body)), MediaType: "application/json", Locators: locators}, Selector: model.Selector{Kind: "whole"}}
}

func pvPut(t *testing.T, root, rel string, body []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, body, 0600); err != nil {
		t.Fatal(err)
	}
}

// pvNew admits a task, a claim, a KNOWN-validated instrument and an attempt.
// The criterion is fixed separately so runs can be admitted before it.
func pvNew(t *testing.T) *pvWorld {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	pvPut(t, root, "datum.toml", []byte("id = \"verify/proof\"\nledger = \".datum/events\"\n"))
	p, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	w := &pvWorld{t: t, p: p, n: 100, lane: model.Actor{ID: "lane"}}
	impl, validation := []byte(`{"tool":"measure"}`), []byte(`{"validated":"against a known pose sweep"}`)
	pvPut(t, root, "tools/measure.json", impl)
	pvPut(t, root, "validation/measure.json", validation)
	prov := model.Provenance{Author: w.lane, SourceRefs: []model.ArtifactRef{}}
	scope := model.Scope{SourcePaths: []string{}, ContextRefs: []model.RecordRef{}, AppliesWhen: "this fixture", Limitations: "not a real ledger"}
	task := &model.TaskCreate{ID: w.id(), Provenance: prov, Spec: model.TaskSpec{Intent: "measure", Subject: "pose sweep", Scope: scope, NonGoals: []string{"production writes"},
		AcceptanceCriteria: []model.AcceptanceCriterion{{ID: w.id(), Revision: 1, Criterion: "measured"}}, ContextRefs: []model.RecordRef{}, ConstraintRefs: []model.RecordRef{}, Prerequisites: []model.Prerequisite{}, NextActor: w.lane}}
	claim := &model.ClaimAssert{ID: w.id(), Provenance: prov, Spec: model.ClaimSpec{Assertion: "every pose is below 0.05 mm", Falsifier: "a pose reaches 0.05 mm", Scope: scope, ExternalRefs: []model.ExternalReference{}}}
	instrument := &model.InstrumentDeclare{ID: w.id(), Provenance: prov, Spec: model.InstrumentSpec{QuestionAnswered: "pose penetration depth", BlindTo: "unmeasured poses",
		NotAnswered: "production behaviour", ConfigSurface: []string{}, DangerousDefaults: []string{}, ValidRange: "the fixture sweep",
		ImplementationRef: pvPin(impl, "tools/measure.json"),
		Validation:        recKnown(model.InstrumentValidation{Ref: pvPin(validation, "validation/measure.json"), Version: "v1"})}}
	w.mustAdmit(w.lane, task, claim, instrument)
	w.claim = model.RecordRef{Project: p.ID, RecordID: claim.ID, Revision: 1}
	w.instrument = model.RecordRef{Project: p.ID, RecordID: instrument.ID, Revision: 1}
	w.attempt = w.id()
	w.mustAdmit(w.lane, &model.TaskStart{Task: model.RecordRef{Project: p.ID, RecordID: task.ID, Revision: 1}, Actor: w.lane, AttemptID: w.attempt})
	return w
}

func (w *pvWorld) id() model.ID { w.n++; return recID(w.n) }

func (w *pvWorld) capture(author model.Actor, events ...model.TypedEvent) model.ID {
	w.t.Helper()
	raw := make([]model.Event, len(events))
	for i, e := range events {
		raw[i] = recEncode(w.t, e)
	}
	ref, err := store.WriteIntake(context.Background(), w.p, store.IntakeRequest{CommandID: w.id(), Author: author, Events: raw})
	if err != nil {
		w.t.Fatalf("fixture must durably capture its packet: %v", err)
	}
	return ref.CommandID
}

func (w *pvWorld) review(outcome string, packets ...model.ID) error {
	w.t.Helper()
	_, err := write.Admit(context.Background(), w.p, write.AdmitRequest{CommandID: w.id(), PacketIDs: packets, Admitter: model.Actor{ID: "coordinator"}, Outcome: outcome, Reason: "independent proof verification"})
	return err
}

func (w *pvWorld) admit(author model.Actor, events ...model.TypedEvent) error {
	w.t.Helper()
	return w.review("accepted", w.capture(author, events...))
}

func (w *pvWorld) mustAdmit(author model.Actor, events ...model.TypedEvent) {
	w.t.Helper()
	if err := w.admit(author, events...); err != nil {
		w.t.Fatalf("control: fixture step must admit: %v", err)
	}
}

// fix freezes a criterion whose result and population selectors name
// contractPath, pinning example bytes that must resolve at admission.
func (w *pvWorld) fix(contractPath string, example []byte) {
	w.t.Helper()
	result, population := pvPin(example, contractPath), pvPin(example, contractPath)
	result.Selector, population.Selector = model.Selector{Kind: "json-pointer", Pointer: "/results"}, model.Selector{Kind: "json-pointer", Pointer: "/population"}
	target := json.Number("0.05")
	fix := &model.CriterionFix{Claim: w.claim, CriterionID: w.id(), Revision: 1, Author: w.lane, SourceRefs: []model.ArtifactRef{},
		Expression: model.CriterionExpression{ResultSelector: result, Unit: "mm", Population: model.Population{Identity: "pose sweep", Selector: population, Denominator: "poses"},
			Operator: model.Less, Target: model.Scalar{Type: "number", Number: &target}, Reducer: model.All},
		Policy: model.EvaluationPolicy{Inclusion: "entire-criterion-family", Retry: "retain-all"}}
	w.mustAdmit(w.lane, fix)
	w.criterion = model.CriterionRef{Claim: w.claim, CriterionID: fix.CriterionID, Revision: 1}
	// The criterion's admitting bundle must be recorded strictly before any start.
	time.Sleep(2 * time.Millisecond)
}

func (w *pvWorld) start(id model.ID, criterion bool) model.InvocationEnvelope {
	unknownMap := recUnknown[map[string]model.Availability[model.Scalar]]("not launched")
	env := model.InvocationEnvelope{InvocationID: id, AttemptID: w.attempt, InstrumentRef: w.instrument,
		CriterionRef:            recUnknown[model.CriterionRef]("no criterion named for this run"),
		ExecutionSourceIdentity: model.ExecutionIdentity{Project: w.p.ID, SourceRefs: []model.ArtifactRef{}, MachineID: recUnknown[model.ID]("fixture"), Head: recUnknown[model.GitHead]("fixture"), Dirty: recUnknown[bool]("fixture")},
		Argv:                    []string{"/bin/sh", "tools/measure.sh"}, InputRefs: []model.ArtifactRef{}, ConfigRequested: map[string]model.Scalar{}, ConditionsDeclared: map[string]model.Scalar{},
		ConfigEffective: unknownMap, ConditionsObserved: unknownMap, Isolation: recUnknown[model.Isolation]("not enforced"),
		StartedAt: time.Now().UTC(), ObservedAt: recUnknown[time.Time]("not launched"), Outcome: recUnknown[model.ProcessOutcome]("not launched"),
		OutputRefs: recUnknown[[]model.ArtifactRef]("not launched"), Visual: recUnknown[model.VisualObservation]("numeric")}
	if criterion {
		env.CriterionRef = recKnown(w.criterion)
	}
	return env
}

func (w *pvWorld) seal(env model.InvocationEnvelope, outputs ...model.ArtifactRef) *model.InvocationSeal {
	exit := 0
	env.ObservedAt = recKnown(env.StartedAt.Add(time.Millisecond))
	env.Outcome = recKnown(model.ProcessOutcome{Kind: "exit", ExitCode: &exit})
	env.OutputRefs = recKnown(outputs)
	env.ConfigEffective = recKnown(map[string]model.Availability[model.Scalar]{})
	env.ConditionsObserved = recKnown(map[string]model.Availability[model.Scalar]{})
	return &model.InvocationSeal{StartRef: model.InvocationRef{Project: w.p.ID, InvocationID: env.InvocationID}, Envelope: env}
}

// run admits a hand-captured start and seal whose outputs the caller declares.
func (w *pvWorld) run(criterion bool, outputs func(id model.ID) []model.ArtifactRef) (model.ID, error) {
	w.t.Helper()
	id := w.id()
	env := w.start(id, criterion)
	return id, w.admit(w.lane, &model.InvocationStart{Envelope: env}, w.seal(env, outputs(id)...))
}

func (w *pvWorld) proof(members map[model.ID]string) *model.ProofAdmit {
	evidence := []model.ObservationDisposition{}
	for id, disposition := range members {
		evidence = append(evidence, model.ObservationDisposition{InvocationRef: model.InvocationRef{Project: w.p.ID, InvocationID: id}, Disposition: disposition, Reason: "dispositioned by the reviewer"})
	}
	// R14.1: a new proof states its verdict.
	return &model.ProofAdmit{Claim: w.claim, CriterionRef: w.criterion, Evidence: evidence, Judgment: model.ResponsibleJudgment{Actor: w.lane, Reason: "the complete family satisfies the frozen criterion"}, Verdict: "supports"}
}

func (w *pvWorld) prove(members map[model.ID]string) error {
	w.t.Helper()
	return w.admit(w.lane, w.proof(members))
}

func (w *pvWorld) snapshot() reduce.Snapshot {
	w.t.Helper()
	prefix, err := store.ReadPrefix(w.p)
	if err != nil {
		w.t.Fatal(err)
	}
	s, err := reduce.Replay(prefix)
	if err != nil {
		w.t.Fatal(err)
	}
	return s
}

func (w *pvWorld) status() reduce.ClaimStatus {
	w.t.Helper()
	p, ok := w.snapshot().ClaimAt(w.claim)
	if !ok {
		w.t.Fatal("claim missing from a fresh replay")
	}
	return p.Status
}

func pvRunPath(id model.ID, rel string) string {
	return ".datum/artifacts/runs/" + string(id) + "/" + rel
}

// ---- run-directory binding -------------------------------------------------

// Control: a run whose result file really sits in its own run directory,
// named by the bare contract path out/result.json, reaches PROVEN.
func pvOwnRunDirControl(t *testing.T, example []byte) *pvWorld {
	t.Helper()
	w := pvNew(t)
	pvPut(t, w.p.Root, "out/result.json", example)
	w.fix("out/result.json", example)
	id, err := w.run(true, func(id model.ID) []model.ArtifactRef {
		pvPut(t, w.p.Root, pvRunPath(id, "out/result.json"), []byte(pvPass))
		return []model.ArtifactRef{pvPin([]byte(pvPass), pvRunPath(id, "out/result.json"))}
	})
	if err != nil {
		t.Fatalf("control: a run whose output is in its own run directory must admit: %v", err)
	}
	if err := w.prove(map[model.ID]string{id: "supports"}); err != nil || w.status() != reduce.StatusProven {
		t.Fatalf("control: proof over the run's own run-dir output must reach PROVEN: %v, status %s", err, w.status())
	}
	return w
}

// The rule says the contract path is resolved in the observing run's own
// directory. The check is made on the declared locator string; the bytes are
// then read by digest, and the content store answers for any digest ever
// admitted, including the criterion's own pinned example. A run whose
// directory never held a result file is observed as having produced one.
func TestProofVerifyRunDirReadingMustComeFromTheRunNotTheContentStore(t *testing.T) {
	pvOwnRunDirControl(t, []byte(pvPass))
	w := pvNew(t)
	// The criterion pins an example result. Admission copies its bytes into
	// the content store; the working copy is then gone.
	pvPut(t, w.p.Root, "out/result.json", []byte(pvPass))
	w.fix("out/result.json", []byte(pvPass))
	if err := os.Remove(filepath.Join(w.p.Root, "out", "result.json")); err != nil {
		t.Fatal(err)
	}
	var runDir string
	id, sealErr := w.run(true, func(id model.ID) []model.ArtifactRef {
		runDir = filepath.Join(w.p.Root, filepath.FromSlash(pvRunPath(id, "")))
		// No file is written: the run directory does not even exist.
		return []model.ArtifactRef{pvPin([]byte(pvPass), pvRunPath(id, "out/result.json"))}
	})
	if _, err := os.Stat(runDir); !os.IsNotExist(err) {
		t.Fatalf("fixture: run directory %s must not exist, stat=%v", runDir, err)
	}
	proofErr := errors.New("not attempted")
	if sealErr == nil {
		proofErr = w.prove(map[model.ID]string{id: "supports"})
	}
	if status := w.status(); status == reduce.StatusProven {
		t.Errorf("expected the seal or the proof to be refused: the seal declares %s, which never existed, and its only copy of those bytes is the criterion's pinned example in the content store. Got seal admission error %v, proof admission error %v, claim %s. The run-dir rule is checked on the locator string while the reading is served by digest, so the criterion's own example was observed as this run's result - the failure Observe's comment says must not happen",
			pvRunPath(id, "out/result.json"), sealErr, proofErr, status)
	}
}

// A contract path spelled with a redundant "." segment names another run's
// directory but does not start with the literal runs/ prefix the matcher
// excludes, so it is admitted as a bare path and reads that other run's file.
func TestProofVerifyContractPathNamingAnotherRunNeverMatches(t *testing.T) {
	pvOwnRunDirControl(t, []byte(pvPass))
	for _, form := range []string{"clean", "dot-segment", "double-slash"} {
		t.Run(form, func(t *testing.T) {
			w := pvNew(t)
			// Run B, under no criterion, leaves a passing result in its own dir.
			other, err := w.run(false, func(id model.ID) []model.ArtifactRef {
				pvPut(t, w.p.Root, pvRunPath(id, "out/result.json"), []byte(pvPass))
				return []model.ArtifactRef{pvPin([]byte(pvPass), pvRunPath(id, "out/result.json"))}
			})
			if err != nil {
				t.Fatalf("control: run B must admit: %v", err)
			}
			contract := pvRunPath(other, "out/result.json")
			switch form {
			case "dot-segment":
				contract = ".datum/./artifacts/runs/" + string(other) + "/out/result.json"
			case "double-slash":
				contract = ".datum//artifacts/runs/" + string(other) + "/out/result.json"
			}
			w.fix(contract, []byte(pvPass))
			// Run A declares B's file under the same spelling the criterion uses.
			id, sealErr := w.run(true, func(model.ID) []model.ArtifactRef {
				return []model.ArtifactRef{pvPin([]byte(pvPass), contract)}
			})
			proofErr := errors.New("not attempted")
			if sealErr == nil {
				proofErr = w.prove(map[model.ID]string{id: "supports"})
			}
			if status := w.status(); status == reduce.StatusProven {
				t.Errorf("expected run %s's observation to find nothing: the contract path %q names run %s's directory, which must never match. Got seal error %v, proof error %v, claim %s. The matcher excludes other runs by the literal prefix .datum/artifacts/runs/, not by the path it resolves to",
					id, contract, other, sealErr, proofErr, status)
			}
		})
	}
}

// Guard: declaring both the run-dir form and the bare contract path is
// ambiguous, so a seal carrying the bare form is refused at admission and the
// run never proves, even though both paths hold passing bytes.
func TestProofVerifyRunDirAndBareFormTogetherAreAmbiguous(t *testing.T) {
	// R9 migration: the bare form is no longer expressible; assert its refusal at admission.
	pvOwnRunDirControl(t, []byte(pvPass))
	w := pvNew(t)
	pvPut(t, w.p.Root, "out/result.json", []byte(pvPass))
	w.fix("out/result.json", []byte(pvPass))
	id, err := w.run(true, func(id model.ID) []model.ArtifactRef {
		pvPut(t, w.p.Root, pvRunPath(id, "out/result.json"), []byte(pvPass))
		return []model.ArtifactRef{pvPin([]byte(pvPass), pvRunPath(id, "out/result.json")), pvPin([]byte(pvPass), "out/result.json")}
	})
	if recCode(err) != "invalid-field" {
		t.Errorf("expected a seal declaring the bare contract path to be refused at admission with invalid-field, got %v; a bare-form locator can borrow the criterion's example bytes", err)
	}
	if err == nil {
		_ = w.prove(map[model.ID]string{id: "supports"})
	}
	if w.status() == reduce.StatusProven {
		t.Error("an ambiguous run reached PROVEN")
	}
}

// ---- rejected family members ----------------------------------------------

func TestProofVerifyRejectedRunStaysInTheFamily(t *testing.T) {
	passing := func(t *testing.T, w *pvWorld) model.ID {
		id, err := w.run(true, func(id model.ID) []model.ArtifactRef {
			pvPut(t, w.p.Root, pvRunPath(id, "out/result.json"), []byte(pvPass))
			return []model.ArtifactRef{pvPin([]byte(pvPass), pvRunPath(id, "out/result.json"))}
		})
		if err != nil {
			t.Fatalf("control: passing run must admit: %v", err)
		}
		return id
	}
	pvOwnRunDirControl(t, []byte(pvPass))
	for _, outcome := range []string{"rejected", "correction-requested"} {
		for _, packing := range []string{"start-and-seal", "seal-only"} {
			t.Run(outcome+"/"+packing, func(t *testing.T) {
				w := pvNew(t)
				pvPut(t, w.p.Root, "out/result.json", []byte(pvPass))
				w.fix("out/result.json", []byte(pvPass))
				// A failing run is captured, then turned away at review.
				failed := w.id()
				env := w.start(failed, true)
				pvPut(t, w.p.Root, pvRunPath(failed, "out/result.json"), []byte(pvFail))
				seal := w.seal(env, pvPin([]byte(pvFail), pvRunPath(failed, "out/result.json")))
				var packet model.ID
				if packing == "seal-only" {
					w.mustAdmit(w.lane, &model.InvocationStart{Envelope: env})
					packet = w.capture(w.lane, seal)
				} else {
					packet = w.capture(w.lane, &model.InvocationStart{Envelope: env}, seal)
				}
				if err := w.review(outcome, packet); err != nil {
					t.Fatalf("control: the reviewer may turn the packet away: %v", err)
				}
				good := passing(t, w)
				members := map[model.ID]string{good: "supports"}
				if packing == "seal-only" {
					// Bury the real seal under an attributed UNKNOWN one.
					ref, err := write.Reconcile(context.Background(), w.p, write.ReconcileRequest{Author: w.lane, InvocationID: failed, Reason: "runner presumed dead"})
					if err != nil {
						t.Fatalf("control: a rejected seal is not pending, so reconciliation captures: %v", err)
					}
					if err := w.review("accepted", ref.CommandID); err != nil {
						t.Fatalf("control: the reconciliation seal itself admits: %v", err)
					}
					members[failed] = "inconclusive"
				}
				err := w.prove(members)
				if recCode(err) != "rejected-family-member" {
					t.Errorf("expected rejected-family-member, got %v; a %s packet carrying a failing run of this criterion must not let the family be proven without it", err, outcome)
				}
				if w.status() == reduce.StatusProven {
					t.Error("claim reached PROVEN with a failing run turned away at review")
				}
			})
		}
	}
	t.Run("identical-bytes-admitted", func(t *testing.T) {
		w := pvNew(t)
		pvPut(t, w.p.Root, "out/result.json", []byte(pvPass))
		w.fix("out/result.json", []byte(pvPass))
		again := w.id()
		env := w.start(again, true)
		pvPut(t, w.p.Root, pvRunPath(again, "out/result.json"), []byte(pvPass))
		seal := w.seal(env, pvPin([]byte(pvPass), pvRunPath(again, "out/result.json")))
		if err := w.review("rejected", w.capture(w.lane, &model.InvocationStart{Envelope: env}, seal)); err != nil {
			t.Fatal(err)
		}
		// The identical start and seal are later admitted from a fresh packet.
		w.mustAdmit(w.lane, &model.InvocationStart{Envelope: env}, seal)
		if err := w.prove(map[model.ID]string{again: "supports"}); err != nil || w.status() != reduce.StatusProven {
			t.Errorf("expected PROVEN once the ledger holds the identical run, got %v, %s", err, w.status())
		}
	})
}

// ---- reconciliation ---------------------------------------------------------

func TestProofVerifyReconciliationRules(t *testing.T) {
	w := pvNew(t)
	pvPut(t, w.p.Root, "out/result.json", []byte(pvPass))
	w.fix("out/result.json", []byte(pvPass))
	ctx := context.Background()
	dead := w.id()
	env := w.start(dead, true)
	reconcile := func(author model.Actor, id model.ID, reason string) (model.PacketRef, error) {
		return write.Reconcile(ctx, w.p, write.ReconcileRequest{Author: author, InvocationID: id, Reason: reason})
	}
	if _, err := reconcile(w.lane, dead, "runner host lost power"); err == nil {
		t.Error("reconcile captured a seal for an invocation whose start was never admitted")
	}
	w.mustAdmit(w.lane, &model.InvocationStart{Envelope: env})
	for name, author := range map[string]model.Actor{"blank-id": {ID: " "}, "unknown": {UnknownReason: "not recorded"}} {
		if _, err := reconcile(author, dead, "runner host lost power"); err == nil {
			t.Errorf("reconcile accepted an unidentified author (%s)", name)
		}
	}
	if _, err := reconcile(w.lane, dead, " "); err == nil {
		t.Error("reconcile accepted a blank reason")
	}
	// The gate, not only write.Reconcile, refuses a reading on an UNKNOWN
	// outcome and an unidentified author.
	hand := w.seal(env)
	hand.Envelope.Outcome = recUnknown[model.ProcessOutcome]("observer died")
	for _, field := range []string{"observed_at", "output_refs", "config_effective", "conditions_observed"} {
		bad := *hand
		bad.Envelope.ObservedAt = recUnknown[time.Time]("observer died")
		bad.Envelope.OutputRefs = recUnknown[[]model.ArtifactRef]("observer died")
		bad.Envelope.ConfigEffective = recUnknown[map[string]model.Availability[model.Scalar]]("observer died")
		bad.Envelope.ConditionsObserved = recUnknown[map[string]model.Availability[model.Scalar]]("observer died")
		switch field {
		case "observed_at":
			bad.Envelope.ObservedAt = recKnown(env.StartedAt.Add(time.Second))
		case "output_refs":
			pvPut(t, w.p.Root, pvRunPath(dead, "out/result.json"), []byte(pvPass))
			bad.Envelope.OutputRefs = recKnown([]model.ArtifactRef{pvPin([]byte(pvPass), pvRunPath(dead, "out/result.json"))})
		case "config_effective":
			bad.Envelope.ConfigEffective = recKnown(map[string]model.Availability[model.Scalar]{})
		case "conditions_observed":
			bad.Envelope.ConditionsObserved = recKnown(map[string]model.Availability[model.Scalar]{})
		}
		packet := w.capture(w.lane, &bad)
		if err := w.review("accepted", packet); recCode(err) != "reconciliation-reading" {
			t.Errorf("expected reconciliation-reading for an UNKNOWN-outcome seal carrying %s, got %v", field, err)
		}
		// Turned away, so it is no longer a pending seal.
		if err := w.review("rejected", packet); err != nil {
			t.Fatal(err)
		}
	}
	ref, err := reconcile(w.lane, dead, "runner host lost power")
	if err != nil {
		t.Fatalf("control: an admitted, unsealed invocation reconciles: %v", err)
	}
	packets, err := store.ReadIntake(w.p, []model.ID{ref.CommandID})
	if err != nil || len(packets) != 1 {
		t.Fatal(err)
	}
	// The identical seal from an unidentified packet author is refused.
	anon, err := store.WriteIntake(ctx, w.p, store.IntakeRequest{CommandID: w.id(), Author: model.Actor{UnknownReason: "not recorded"}, Events: packets[0].Events})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.review("accepted", anon.CommandID); recCode(err) != "attribution-unknown" {
		t.Errorf("expected attribution-unknown for a reconciliation seal from an unidentified author, got %v", err)
	}
	if err := w.review("rejected", anon.CommandID); err != nil {
		t.Fatal(err)
	}
	if err := w.review("accepted", ref.CommandID); err != nil {
		t.Fatalf("control: the reconciliation seal admits: %v", err)
	}
	if _, err := reconcile(w.lane, dead, "again"); err == nil {
		t.Error("reconcile captured a second seal for an already sealed invocation")
	}
	inv, _ := w.snapshot().Invocation(reduce.InvocationKey{Project: w.p.ID, InvocationID: dead})
	if inv.Seal == nil || inv.Seal.Outcome.State != model.Unknown || inv.Seal.ObservedAt.State != model.Unknown || inv.Seal.OutputRefs.State != model.Unknown ||
		inv.Seal.ConfigEffective.State != model.Unknown || inv.Seal.ConditionsObserved.State != model.Unknown || inv.Seal.Isolation.State != model.Unknown || inv.Seal.Visual.State != model.Unknown {
		t.Fatalf("reconciled seal carries a reading: %+v", inv.Seal)
	}
	if err := w.prove(map[model.ID]string{dead: "supports"}); err == nil || w.status() == reduce.StatusProven {
		t.Errorf("a reconciled run supported PROVEN: %v, %s", err, w.status())
	}
}

// write.Reconcile refuses while a real seal is pending in intake, but that
// check runs at capture only. Admission of the reconciliation packet does not
// repeat it, so a slow runner whose real seal lands between the two is
// overwritten by UNKNOWN, and its real observation can never enter the ledger.
func TestProofVerifyReconciliationRefusedWhileARealSealIsPending(t *testing.T) {
	for _, route := range []string{"control", "reconcile-then-real-seal", "hand-captured-unknown-seal"} {
		t.Run(route, func(t *testing.T) {
			w := pvNew(t)
			pvPut(t, w.p.Root, "out/result.json", []byte(pvPass))
			w.fix("out/result.json", []byte(pvPass))
			id := w.id()
			env := w.start(id, true)
			w.mustAdmit(w.lane, &model.InvocationStart{Envelope: env})
			pvPut(t, w.p.Root, pvRunPath(id, "out/result.json"), []byte(pvFail))
			real := w.seal(env, pvPin([]byte(pvFail), pvRunPath(id, "out/result.json")))
			var unknown, realPacket model.ID
			switch route {
			case "control":
				w.capture(w.lane, real)
				if _, err := write.Reconcile(context.Background(), w.p, write.ReconcileRequest{Author: w.lane, InvocationID: id, Reason: "presumed dead"}); err == nil {
					t.Fatal("control: write.Reconcile must refuse while the real seal is pending")
				}
				return
			case "reconcile-then-real-seal":
				ref, err := write.Reconcile(context.Background(), w.p, write.ReconcileRequest{Author: w.lane, InvocationID: id, Reason: "presumed dead"})
				if err != nil {
					t.Fatalf("control: nothing is pending yet, so reconciliation captures: %v", err)
				}
				unknown = ref.CommandID
				realPacket = w.capture(w.lane, real) // the slow runner's real seal arrives
			case "hand-captured-unknown-seal":
				realPacket = w.capture(w.lane, real)
				hand := w.seal(env)
				hand.Envelope.Outcome = recUnknown[model.ProcessOutcome]("observer died")
				hand.Envelope.ObservedAt = recUnknown[time.Time]("observer died")
				hand.Envelope.OutputRefs = recUnknown[[]model.ArtifactRef]("observer died")
				hand.Envelope.ConfigEffective = recUnknown[map[string]model.Availability[model.Scalar]]("observer died")
				hand.Envelope.ConditionsObserved = recUnknown[map[string]model.Availability[model.Scalar]]("observer died")
				unknown = w.capture(w.lane, hand)
			}
			err := w.review("accepted", unknown)
			if err == nil {
				inv, _ := w.snapshot().Invocation(reduce.InvocationKey{Project: w.p.ID, InvocationID: id})
				realErr := w.review("accepted", realPacket)
				t.Errorf("expected admission of the UNKNOWN-outcome seal to be refused while a real seal for %s is pending in intake; it was admitted and the ledger now records outcome %s (%q). Admitting the real seal (exit 0, failing reading) afterwards: %v. The pending-seal rule lives only in write.Reconcile at capture time, so a seal that arrives after capture, or an UNKNOWN seal captured by hand, reaches the ledger unchecked and the real observation is shut out",
					id, inv.Seal.Outcome.State, inv.Seal.Outcome.Reason, realErr)
			}
		})
	}
}

// ---- correction -------------------------------------------------------------

func pvProven(t *testing.T) (*pvWorld, model.ID) {
	t.Helper()
	w := pvNew(t)
	pvPut(t, w.p.Root, "out/result.json", []byte(pvPass))
	w.fix("out/result.json", []byte(pvPass))
	id, err := w.run(true, func(id model.ID) []model.ArtifactRef {
		pvPut(t, w.p.Root, pvRunPath(id, "out/result.json"), []byte(pvPass))
		return []model.ArtifactRef{pvPin([]byte(pvPass), pvRunPath(id, "out/result.json"))}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.prove(map[model.ID]string{id: "supports"}); err != nil || w.status() != reduce.StatusProven {
		t.Fatalf("control: fixture must reach PROVEN: %v", err)
	}
	return w, id
}

func (w *pvWorld) correction(kind string, corrective model.ArtifactRef) *model.Correction {
	c := &model.Correction{Target: model.CorrectionTarget{Kind: kind}, AffectedRevisions: []model.RecordRef{w.claim}, Reason: "the sweep missed a pose", CorrectiveRef: corrective}
	switch kind {
	case "record":
		c.Target.Record = &w.instrument
	case "criterion":
		c.Target.Criterion = &w.criterion
	case "support":
		c.Target.Support = &model.SupportLink{Dependent: w.claim, Evidence: corrective}
	}
	return c
}

func TestProofVerifyCorrectionInvalidatesProofTransitively(t *testing.T) {
	for _, kind := range []string{"record", "criterion", "support"} {
		t.Run(kind, func(t *testing.T) {
			w, run := pvProven(t)
			body := []byte(`{"missed":"pose-c"}`)
			pvPut(t, w.p.Root, "corrections/missed.json", body)
			c := w.correction(kind, pvPin(body, "corrections/missed.json"))
			if kind == "record" {
				// Only the instrument is named; the claim is reached through
				// its proof's observations, not listed.
				c.AffectedRevisions = []model.RecordRef{w.instrument}
			}
			if err := w.admit(w.lane, c); err != nil {
				t.Fatalf("control: a typed correction with a resolvable corrective artifact must admit: %v", err)
			}
			p, _ := w.snapshot().ClaimAt(w.claim)
			if p.Status != reduce.StatusProven || len(p.Proofs) != 1 {
				t.Errorf("correction erased PROVEN history: %s with %d proofs", p.Status, len(p.Proofs))
			}
			if p.Support.CorrectionFree != reduce.TruthFalse || p.Support.Current() == reduce.TruthTrue {
				t.Errorf("a %s correction left the PROVEN claim with current support %+v", kind, p.Support)
			}
			if err := w.prove(map[model.ID]string{run: "supports"}); err == nil {
				t.Error("a second proof admitted over an unresolved correction")
			}
		})
	}
	t.Run("refusals", func(t *testing.T) {
		w, _ := pvProven(t)
		body := []byte(`{"missed":"pose-c"}`)
		pvPut(t, w.p.Root, "corrections/missed.json", body)
		outside := t.TempDir()
		secret := []byte(`{"outside":"the root"}`)
		pvPut(t, outside, "secret.json", secret)
		if err := os.Symlink(filepath.Join(outside, "secret.json"), filepath.Join(w.p.Root, "corrections", "link.json")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(w.p.Root, "escape")); err != nil {
			t.Fatal(err)
		}
		cases := map[string]func(*model.Correction){
			"blank-reason":          func(c *model.Correction) { c.Reason = " " },
			"no-affected-revisions": func(c *model.Correction) { c.AffectedRevisions = []model.RecordRef{} },
			"unknown-affected": func(c *model.Correction) {
				c.AffectedRevisions = []model.RecordRef{{Project: w.p.ID, RecordID: recID(9999), Revision: 1}}
			},
			"untyped-target": func(c *model.Correction) { c.Target.Kind = "invocation" },
			"corrective-missing": func(c *model.Correction) {
				c.CorrectiveRef = pvPin([]byte(`{"absent":true}`), "corrections/absent.json")
			},
			"corrective-symlink":     func(c *model.Correction) { c.CorrectiveRef = pvPin(secret, "corrections/link.json") },
			"corrective-parent-link": func(c *model.Correction) { c.CorrectiveRef = pvPin(secret, "escape/secret.json") },
			"support-evidence-link": func(c *model.Correction) {
				c.Target = model.CorrectionTarget{Kind: "support", Support: &model.SupportLink{Dependent: w.claim, Evidence: pvPin(secret, "escape/secret.json")}}
			},
		}
		for name, mutate := range cases {
			c := w.correction("record", pvPin(body, "corrections/missed.json"))
			mutate(c)
			raw := recEncodeLoose(t, c)
			if _, err := w.admitRawEvent(raw); err == nil {
				t.Errorf("%s: correction admitted; typed target, exact affected revisions, a reason and an in-root corrective artifact are all required", name)
			}
		}
		if w.snapshot().Corrections() != nil && len(w.snapshot().Corrections()) != 0 {
			t.Errorf("a refused correction reached the ledger: %+v", w.snapshot().Corrections())
		}
	})
}

// recEncodeLoose bypasses the encoder's own validation so that admission, not
// the helper, is the thing refusing a malformed correction.
func recEncodeLoose(t *testing.T, e model.TypedEvent) model.Event {
	t.Helper()
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return model.Event{Type: e.EventType(), Data: data}
}

func (w *pvWorld) admitRawEvent(raw model.Event) (model.Bundle, error) {
	w.t.Helper()
	ref, err := store.WriteIntake(context.Background(), w.p, store.IntakeRequest{CommandID: w.id(), Author: w.lane, Events: []model.Event{raw}})
	if err != nil {
		return model.Bundle{}, err
	}
	return write.Admit(context.Background(), w.p, write.AdmitRequest{CommandID: w.id(), PacketIDs: []model.ID{ref.CommandID}, Admitter: model.Actor{ID: "coordinator"}, Outcome: "accepted", Reason: "independent proof verification"})
}

// Every stored path is relative to the datum root (contract, datum.toml).
// A git pin's path is handed to `git cat-file <commit>:<path>`, which git reads
// relative to the repository's top level. When the datum root is a
// subdirectory of its repository, a corrective artifact therefore resolves to
// bytes outside the datum root, and a path that is inside the root does not
// resolve at all.
func TestProofVerifyCorrectiveGitPinResolvesInsideTheDatumRoot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	w, _ := pvProven(t)
	body := []byte(`{"missed":"pose-c"}`)
	pvPut(t, w.p.Root, "corrections/missed.json", body)
	if err := w.admit(w.lane, w.correction("support", pvPin(body, "corrections/missed.json"))); err != nil {
		t.Fatalf("control: an in-root content-pinned correction must admit: %v", err)
	}
	// Place the datum root inside a repository, one level down.
	repo := t.TempDir()
	root := filepath.Join(repo, "project")
	if err := os.Rename(w.p.Root, root); err != nil {
		t.Fatal(err)
	}
	w.p.Root, w.p.Ledger = root, filepath.Join(root, ".datum", "events")
	outsideBody := []byte(`{"secret":"repository root, outside the datum root"}`)
	insideBody := []byte(`{"inside":"the datum root"}`)
	pvPut(t, repo, "secret.json", outsideBody)
	pvPut(t, root, "corrections/inside.json", insideBody)
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=review", "-c", "user.email=review@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "--object-format=sha1")
	git("add", "secret.json", "project/corrections/inside.json")
	git("commit", "-q", "-m", "fixture")
	commit := git("rev-parse", "HEAD")
	gitPin := func(p string) model.ArtifactRef {
		return model.ArtifactRef{Kind: "git", Git: &model.GitPin{ObjectFormat: "sha1", Commit: commit, Path: p}, Selector: model.Selector{Kind: "whole"}}
	}
	escapeErr := w.admit(w.lane, w.correction("support", gitPin("secret.json")))
	insideErr := w.admit(w.lane, w.correction("support", gitPin("corrections/inside.json")))
	if escapeErr == nil || insideErr != nil {
		t.Errorf("expected the corrective git pin %q (no such file under datum root %s) to be refused and %q (a committed file inside it) to admit. Got escape=%v, inside=%v. The datum-root-relative path is resolved against the repository top level %s, so the correction cites bytes outside the datum root",
			"secret.json", root, "corrections/inside.json", escapeErr, insideErr, repo)
	}
}

// ---- end to end through fresh processes -------------------------------------

var (
	pvBinaryOnce sync.Once
	pvBinary     string
	pvBinaryErr  error
)

func pvDatum(t *testing.T) string {
	t.Helper()
	pvBinaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "datum-review-bin-")
		if err != nil {
			pvBinaryErr = err
			return
		}
		pvBinary = filepath.Join(dir, "datum")
		out, err := exec.Command("go", "build", "-o", pvBinary, "datum/cmd/datum").CombinedOutput()
		if err != nil {
			pvBinaryErr = fmt.Errorf("%v: %s", err, out)
		}
	})
	if pvBinaryErr != nil {
		t.Fatalf("building the datum CLI: %v", pvBinaryErr)
	}
	return pvBinary
}

func (w *pvWorld) cli(stdin []byte, args ...string) ([]byte, error) {
	w.t.Helper()
	cmd := exec.Command(pvDatum(w.t), args...)
	cmd.Dir, cmd.Stdin = w.p.Root, bytes.NewReader(stdin)
	cmd.Env = append(os.Environ(), "HOME="+os.Getenv("HOME"), "DATUM_ACTOR=lane")
	bindProjectHome(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("datum %s: %v: %s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.Bytes(), nil
}

func (w *pvWorld) cliAdmit(packets ...model.ID) error {
	w.t.Helper()
	// R19: writes print a one-line acknowledgement by default; --json is the full result this test decodes.
	args := []string{"admit", "--json", "--command-id", string(w.id()), "--actor", "coordinator", "--outcome", "accepted", "--reason", "fresh-process review"}
	for _, p := range packets {
		args = append(args, string(p))
	}
	_, err := w.cli(nil, args...)
	return err
}

func (w *pvWorld) cliRun(script string) (model.InvocationEnvelope, []model.ID, error) {
	w.t.Helper()
	pvPut(w.t, w.p.Root, "tools/run.sh", []byte(script))
	// R19: writes print a one-line acknowledgement by default; --json is the full result this test decodes.
	out, err := w.cli(nil, "run", "--json", "--attempt-id", string(w.attempt), "--instrument", string(w.instrument.RecordID),
		"--claim", string(w.claim.RecordID), "--claim-revision", "1", "--criterion-id", string(w.criterion.CriterionID), "--criterion-revision", "1", "--", "/bin/sh", "tools/run.sh")
	var result struct {
		// datum run prints snake_case keys (coordinator change, in the open).
		Envelope    model.InvocationEnvelope `json:"envelope"`
		StartPacket model.PacketRef          `json:"start_packet"`
		SealPacket  model.PacketRef          `json:"seal_packet"`
	}
	if len(out) > 0 {
		if jsonErr := json.Unmarshal(out, &result); jsonErr != nil {
			w.t.Fatalf("run printed unparseable output %s: %v", out, jsonErr)
		}
	}
	packets := []model.ID{}
	for _, p := range []model.PacketRef{result.StartPacket, result.SealPacket} {
		if p.CommandID != "" {
			packets = append(packets, p.CommandID)
		}
	}
	return result.Envelope, packets, err
}

func pvProducer(body string) string {
	return "mkdir \"$DATUM_RUN_DIR/out\"\nprintf '%s' '" + body + "' > \"$DATUM_RUN_DIR/out/result.json\"\n" +
		"printf '%s' '{\"version\":1,\"config_effective\":{},\"conditions_observed\":{},\"outputs\":[{\"path\":\"out/result.json\",\"media_type\":\"application/json\"}]}' > \"$DATUM_RUN_REPORT\"\n"
}

// A real `datum run` through fresh processes reaches PROVEN, and the reading
// is the run's own: the criterion's pinned example FAILS, so PROVEN is only
// reachable by reading the bytes the producer wrote. A second real run that
// fails must be dispositioned; a runner killed mid-run is reconciled through
// the CLI and cannot support the proof.
func TestProofVerifyFreshProcessRunReachesProvenOnItsOwnBytes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the producer is a POSIX shell script")
	}
	w := pvNew(t)
	pvPut(t, w.p.Root, "out/result.json", []byte(pvFail))
	w.fix("out/result.json", []byte(pvFail))

	pass, packets, err := w.cliRun(pvProducer(pvPass))
	if err != nil || len(packets) != 2 {
		t.Fatalf("control: datum run must print its start and seal packets: %v, %v", packets, err)
	}
	if err := w.cliAdmit(packets...); err != nil {
		t.Fatalf("control: the run's packets admit through the CLI: %v", err)
	}
	if w.status() != reduce.StatusMeasured {
		t.Fatalf("an admitted run left the claim %s", w.status())
	}

	// A runner killed before it can seal: the producer kills its observer.
	dead, packets, runErr := w.cliRun("kill -9 $PPID\nsleep 1\n")
	if runErr == nil || len(packets) != 0 || dead.InvocationID != "" {
		t.Fatalf("fixture: the observer must die without printing packets, got %v, %v", packets, runErr)
	}
	intake, err := store.ReadIntake(w.p, nil)
	if err != nil {
		t.Fatal(err)
	}
	var deadStart model.ID
	var deadID model.ID
	reviewed := w.snapshot()
	for _, packet := range intake {
		if _, ok := reviewed.Review(reduce.ReviewKey{Project: w.p.ID, CommandID: packet.CommandID}); ok {
			continue
		}
		for _, raw := range packet.Events {
			if e, err := model.DecodeEvent(raw); err == nil {
				if s, ok := e.(*model.InvocationStart); ok {
					deadStart, deadID = packet.CommandID, s.Envelope.InvocationID
				}
			}
		}
	}
	if deadStart == "" {
		t.Fatal("the killed run left no start packet in intake; intent must be persisted before launch")
	}
	if err := w.cliAdmit(deadStart); err != nil {
		t.Fatal(err)
	}
	if err := w.prove(map[model.ID]string{pass.InvocationID: "supports"}); !strings.Contains(fmt.Sprint(err), "reconciliation") {
		t.Errorf("expected proof to wait for the dead run's reconciliation, got %v", err)
	}
	// R19: writes print a one-line acknowledgement by default; --json is the full result this test decodes.
	out, err := w.cli(nil, "reconcile", "--json", "--invocation-id", string(deadID), "--reason", "observer was killed")
	if err != nil {
		t.Fatalf("control: the dead run reconciles through the CLI: %v", err)
	}
	var sealRef model.PacketRef
	if err := json.Unmarshal(out, &sealRef); err != nil || sealRef.CommandID == "" {
		t.Fatalf("reconcile printed no packet: %s", out)
	}
	if err := w.cliAdmit(sealRef.CommandID); err != nil {
		t.Fatal(err)
	}
	if err := w.prove(map[model.ID]string{pass.InvocationID: "supports", deadID: "supports"}); err == nil {
		t.Error("a reconciled run with no reading was admitted as support")
	}

	fail, packets, err := w.cliRun(pvProducer(pvFail))
	if err != nil || len(packets) != 2 {
		t.Fatalf("control: a failing measurement still seals: %v, %v", packets, err)
	}
	if err := w.cliAdmit(packets...); err != nil {
		t.Fatal(err)
	}
	for _, disposition := range []string{"supports", "inconclusive", "inapplicable"} {
		err := w.prove(map[model.ID]string{pass.InvocationID: "supports", deadID: "inconclusive", fail.InvocationID: disposition})
		if err == nil {
			t.Errorf("a real failing run dispositioned %q did not refuse the proof", disposition)
		}
	}
	if err := w.prove(map[model.ID]string{pass.InvocationID: "supports", deadID: "inconclusive"}); recCode(err) != "incomplete-family" && !strings.Contains(fmt.Sprint(err), "omits") {
		t.Errorf("expected a proof omitting the failing run to be refused as incomplete, got %v", err)
	}
	if w.status() == reduce.StatusProven {
		t.Fatal("claim is PROVEN with a failing family member")
	}

	// A clean family on a second claim revision path is not available here, so
	// the positive end-to-end runs in a fresh world with one passing run.
	v := pvNew(t)
	pvPut(t, v.p.Root, "out/result.json", []byte(pvFail))
	v.fix("out/result.json", []byte(pvFail))
	ok, packets, err := v.cliRun(pvProducer(pvPass))
	if err != nil || len(packets) != 2 {
		t.Fatalf("control: datum run must seal: %v", err)
	}
	if err := v.cliAdmit(packets...); err != nil {
		t.Fatal(err)
	}
	proof, err := model.Encode([]model.Event{recEncode(t, v.proof(map[model.ID]string{ok.InvocationID: "supports"}))})
	if err != nil {
		t.Fatal(err)
	}
	// R19: writes print a one-line acknowledgement by default; --json is the full result this test decodes.
	captured, err := v.cli(proof, "capture", "--json", "--command-id", string(v.id()))
	if err != nil {
		t.Fatal(err)
	}
	var proofPacket model.PacketRef
	if err := json.Unmarshal(captured, &proofPacket); err != nil {
		t.Fatal(err)
	}
	if err := v.cliAdmit(proofPacket.CommandID); err != nil {
		t.Fatalf("a real run's proof was refused through fresh processes: %v", err)
	}
	if status := v.status(); status != reduce.StatusProven {
		t.Fatalf("a real datum run's proof left the claim %s", status)
	}
}

// DATUM-CONTRACT, criterion.fix: "Changing it mints a new criterion revision
// and cannot erase known counterevidence." The family is matched on the exact
// criterion revision, so a failing run under revision 1 is outside revision
// 2's family, and re-fixing the same criterion after a failure proves the
// claim without that failure ever being dispositioned.
func TestProofVerifyNewCriterionRevisionCannotEraseCounterevidence(t *testing.T) {
	passing := func(w *pvWorld) model.ID {
		id, err := w.run(true, func(id model.ID) []model.ArtifactRef {
			pvPut(t, w.p.Root, pvRunPath(id, "out/result.json"), []byte(pvPass))
			return []model.ArtifactRef{pvPin([]byte(pvPass), pvRunPath(id, "out/result.json"))}
		})
		if err != nil {
			t.Fatalf("control: passing run must admit: %v", err)
		}
		return id
	}
	pvOwnRunDirControl(t, []byte(pvPass))
	w := pvNew(t)
	pvPut(t, w.p.Root, "out/result.json", []byte(pvPass))
	w.fix("out/result.json", []byte(pvPass))
	failed, err := w.run(true, func(id model.ID) []model.ArtifactRef {
		pvPut(t, w.p.Root, pvRunPath(id, "out/result.json"), []byte(pvFail))
		return []model.ArtifactRef{pvPin([]byte(pvFail), pvRunPath(id, "out/result.json"))}
	})
	if err != nil {
		t.Fatalf("control: a failing run is admitted as family evidence: %v", err)
	}
	first := w.criterion
	if err := w.prove(map[model.ID]string{failed: "supports"}); err == nil {
		t.Fatal("control: the failing run must refuse proof under revision 1")
	}
	// Revision 2 of the same criterion, identical expression, fixed afterwards.
	result, population := pvPin([]byte(pvPass), "out/result.json"), pvPin([]byte(pvPass), "out/result.json")
	result.Selector, population.Selector = model.Selector{Kind: "json-pointer", Pointer: "/results"}, model.Selector{Kind: "json-pointer", Pointer: "/population"}
	target := json.Number("0.05")
	fix := &model.CriterionFix{Claim: w.claim, CriterionID: first.CriterionID, Revision: 2, Author: w.lane, SourceRefs: []model.ArtifactRef{},
		Expression: model.CriterionExpression{ResultSelector: result, Unit: "mm", Population: model.Population{Identity: "pose sweep", Selector: population, Denominator: "poses"},
			Operator: model.Less, Target: model.Scalar{Type: "number", Number: &target}, Reducer: model.All},
		Policy: model.EvaluationPolicy{Inclusion: "entire-criterion-family", Retry: "retain-all"}}
	if err := w.admit(w.lane, fix); err != nil {
		t.Skipf("a second revision of the criterion is not admissible here (%v); nothing to probe", err)
	}
	w.criterion = model.CriterionRef{Claim: w.claim, CriterionID: first.CriterionID, Revision: 2}
	time.Sleep(2 * time.Millisecond)
	good := passing(w)
	err = w.prove(map[model.ID]string{good: "supports"})
	if err == nil || w.status() == reduce.StatusProven {
		t.Errorf("expected proof under criterion %s revision 2 to be refused while run %s, a failing observation under revision 1 of the same criterion, stays undispositioned; got %v and claim %s. Re-fixing the criterion erased known counterevidence",
			first.CriterionID, failed, err, w.status())
	}
}
