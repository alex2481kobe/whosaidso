package write

// Fixture and core negative tests for U12 proof admission: empty family,
// contradicting members, criteria fixed after the run, unknown validation,
// unnamed judgment and cross-revision evidence. Family closure over pending
// intake and artifact containment through the new operations live in
// gate_family_test.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

const (
	proofPass = `{"results":{"unit":"mm","population":"pose sweep","denominator":"poses","values":[0.0100,0.0200]},"population":{"population":"pose sweep","denominator":"poses","values":["pose-a","pose-b"]}}`
	proofFail = `{"results":{"unit":"mm","population":"pose sweep","denominator":"poses","values":[0.2000,0.0100]},"population":{"population":"pose sweep","denominator":"poses","values":["pose-a","pose-b"]}}`
	proofPath = "out/result.json"
)

type proofWorld struct {
	f          *admissionFixture
	claim      model.RecordRef
	instrument model.RecordRef
	criterion  model.CriterionRef
	attempt    model.ID
}

func proofKnown[T any](v T) model.Availability[T] {
	return model.Availability[T]{State: model.Known, Value: &v}
}
func proofUnknown[T any](why string) model.Availability[T] {
	return model.Availability[T]{State: model.Unknown, Reason: why}
}

func proofPin(body, path string) model.ArtifactRef {
	return model.ArtifactRef{Kind: "content", Content: &model.ContentPin{SHA256: model.HashBytes([]byte(body)), Length: uint64(len(body)), MediaType: "application/json", Locators: []model.Locator{{Path: path}}}, Selector: model.Selector{Kind: "whole"}}
}

func proofPut(t *testing.T, root, path, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

// newProofWorld admits a READY task, a claim, an instrument and, in a later
// bundle, a frozen criterion, then starts the attempt that runs will belong to.
func newProofWorld(t *testing.T, validated bool) *proofWorld {
	t.Helper()
	f := newAdmissionFixture(t)
	w := &proofWorld{f: f}
	proofPut(t, f.project.Root, proofPath, proofPass)
	proofPut(t, f.project.Root, "validation/instrument.json", `{"validated":"fixture"}`)
	task, claim, instrument := f.task(), f.claim(), f.instrument()
	if validated {
		instrument.Spec.Validation = proofKnown(model.InstrumentValidation{Ref: proofPin(`{"validated":"fixture"}`, "validation/instrument.json"), Version: "v1"})
	}
	f.accept(f.capture([][]byte{[]byte("instrument implementation")}, task, claim, instrument))
	w.claim, w.instrument = f.ref(claim.ID, 1), f.ref(instrument.ID, 1)
	w.criterion = w.fix(w.claim)
	w.attempt = f.id()
	f.accept(f.capture(nil, &model.TaskStart{Task: f.ref(task.ID, 1), Actor: f.author, AttemptID: w.attempt}))
	return w
}

func (w *proofWorld) fixEvent(claim model.RecordRef) *model.CriterionFix {
	result, population := proofPin(proofPass, proofPath), proofPin(proofPass, proofPath)
	result.Selector = model.Selector{Kind: "json-pointer", Pointer: "/results"}
	population.Selector = model.Selector{Kind: "json-pointer", Pointer: "/population"}
	target := json.Number("0.05")
	return &model.CriterionFix{Claim: claim, CriterionID: w.f.id(), Revision: 1,
		Expression: model.CriterionExpression{ResultSelector: result, Unit: "mm",
			Population: model.Population{Identity: "pose sweep", Selector: population, Denominator: "poses"},
			Operator:   model.Less, Target: model.Scalar{Type: "number", Number: &target}, Reducer: model.All},
		Policy: model.EvaluationPolicy{Inclusion: "entire-criterion-family", Retry: "retain-all"},
		Author: w.f.author, SourceRefs: []model.ArtifactRef{}}
}

func (w *proofWorld) fix(claim model.RecordRef) model.CriterionRef {
	fix := w.fixEvent(claim)
	w.f.accept(w.f.capture(nil, fix))
	return model.CriterionRef{Claim: claim, CriterionID: fix.CriterionID, Revision: fix.Revision}
}

func (w *proofWorld) envelope(criterion model.CriterionRef) model.InvocationEnvelope {
	return model.InvocationEnvelope{InvocationID: w.f.id(), AttemptID: w.attempt, InstrumentRef: w.instrument, CriterionRef: proofKnown(criterion),
		ExecutionSourceIdentity: model.ExecutionIdentity{Project: w.f.project.ID, MachineID: proofUnknown[model.ID]("fixture"), SourceRefs: []model.ArtifactRef{}, Head: proofUnknown[model.GitHead]("fixture"), Dirty: proofUnknown[bool]("fixture")},
		Argv:                    []string{"fixture-measurement"}, InputRefs: []model.ArtifactRef{}, ConfigRequested: map[string]model.Scalar{}, ConditionsDeclared: map[string]model.Scalar{},
		ConfigEffective: proofUnknown[map[string]model.Availability[model.Scalar]]("not launched"), ConditionsObserved: proofUnknown[map[string]model.Availability[model.Scalar]]("not launched"),
		Isolation: proofUnknown[model.Isolation]("not enforced"), StartedAt: time.Now().UTC(), ObservedAt: proofUnknown[time.Time]("not launched"),
		Outcome: proofUnknown[model.ProcessOutcome]("not launched"), OutputRefs: proofUnknown[[]model.ArtifactRef]("not launched"), Visual: proofUnknown[model.VisualObservation]("numeric"),
	}
}

func proofSealed(env model.InvocationEnvelope, body string) *model.InvocationSeal {
	exit := 0
	if body == proofFail {
		exit = 1
	}
	env.ObservedAt = proofKnown(env.StartedAt.Add(time.Millisecond))
	env.Outcome = proofKnown(model.ProcessOutcome{Kind: "exit", ExitCode: &exit})
	env.OutputRefs = proofKnown([]model.ArtifactRef{proofPin(body, proofPath)})
	env.ConfigEffective = proofKnown(map[string]model.Availability[model.Scalar]{})
	env.ConditionsObserved = proofKnown(map[string]model.Availability[model.Scalar]{})
	return &model.InvocationSeal{StartRef: model.InvocationRef{Project: env.ExecutionSourceIdentity.Project, InvocationID: env.InvocationID}, Envelope: env}
}

// run captures a start and a seal as two packets, as write.Run does.
func (w *proofWorld) run(criterion model.CriterionRef, body string) (model.InvocationRef, model.PacketRef, model.PacketRef) {
	env := w.envelope(criterion)
	start := w.f.capture(nil, &model.InvocationStart{Envelope: env})
	seal := w.f.capture([][]byte{[]byte(body)}, proofSealed(env, body))
	return model.InvocationRef{Project: w.f.project.ID, InvocationID: env.InvocationID}, start, seal
}

func (w *proofWorld) proof(criterion model.CriterionRef, members map[model.InvocationRef]string) *model.ProofAdmit {
	p := &model.ProofAdmit{Claim: criterion.Claim, CriterionRef: criterion, Evidence: []model.ObservationDisposition{},
		Judgment: model.ResponsibleJudgment{Actor: w.f.author, Reason: "the complete family satisfies the frozen criterion"}}
	for ref, disposition := range members {
		p.Evidence = append(p.Evidence, model.ObservationDisposition{InvocationRef: ref, Disposition: disposition, Reason: "reviewed"})
	}
	return p
}

func (w *proofWorld) status(t *testing.T) reduce.ClaimStatus {
	t.Helper()
	p, ok := w.f.snapshot().ClaimAt(w.claim)
	if !ok {
		t.Fatal("claim missing")
	}
	return p.Status
}

func (f *admissionFixture) captureRaw(event model.TypedEvent) model.PacketRef {
	f.t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		f.t.Fatal(err)
	}
	ref, err := store.WriteIntake(context.Background(), f.project, store.IntakeRequest{CommandID: f.id(), Author: f.author, Events: []model.Event{{Type: event.EventType(), Data: data}}, Blobs: []io.Reader{}})
	if err != nil {
		f.t.Fatal(err)
	}
	return ref
}

func TestProofControlReachesProvenAndRecordsTheIntakeLimit(t *testing.T) {
	w := newProofWorld(t, true)
	pass, start, seal := w.run(w.criterion, proofPass)
	w.f.accept(start, seal)
	if w.status(t) != reduce.StatusMeasured {
		t.Fatal("an admitted observation must make the claim MEASURED")
	}
	bundle := w.f.accept(w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{pass: "supports"})))
	if w.status(t) != reduce.StatusProven {
		t.Fatal("control proof did not reach PROVEN")
	}
	review := bundle.Events[len(bundle.Events)-1]
	if !bytes.Contains(review.Data, []byte("intake inbox only")) {
		t.Fatalf("proof admission did not record the intake completeness limit: %s", review.Data)
	}
}

func TestProofRefusesEmptyOrInventedFamily(t *testing.T) {
	w := newProofWorld(t, true)
	// No run at all: an empty evidence list never decodes.
	w.f.refuse(w.f.request(w.f.captureRaw(w.proof(w.criterion, nil))), "invalid-field")
	// A named invocation that no run produced is not evidence either.
	ghost := model.InvocationRef{Project: w.f.project.ID, InvocationID: w.f.id()}
	w.f.refuse(w.f.request(w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{ghost: "supports"}))), "unknown-reference")
	if w.status(t) != reduce.StatusUnmeasured {
		t.Fatal("refused proofs changed the claim")
	}
}

func TestProofRefusesUnresolvedCounterevidence(t *testing.T) {
	for _, disposition := range []string{"contradicts", "inconclusive", "inapplicable", "supports", "omitted"} {
		t.Run(disposition, func(t *testing.T) {
			w := newProofWorld(t, true)
			pass, s1, e1 := w.run(w.criterion, proofPass)
			fail, s2, e2 := w.run(w.criterion, proofFail)
			w.f.accept(s1, e1, s2, e2)
			members := map[model.InvocationRef]string{pass: "supports", fail: disposition}
			if disposition == "omitted" {
				delete(members, fail)
			}
			code := map[string]string{"contradicts": "invalid-transition", "omitted": "invalid-transition"}[disposition]
			if code == "" {
				code = "counterevidence-unresolved"
			}
			w.f.refuse(w.f.request(w.f.capture(nil, w.proof(w.criterion, members))), code)
			if w.status(t) != reduce.StatusMeasured {
				t.Fatal("a family with a failing run reached PROVEN")
			}
		})
	}
}

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
	// The control: a criterion fixed before every run carrying it.
	clean, s2, e2 := w.run(w.criterion, proofPass)
	w.f.accept(s2, e2)
	w.f.accept(w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{clean: "supports"})))
}

func TestProofRefusesUnknownValidationInstrument(t *testing.T) {
	w := newProofWorld(t, false)
	pass, s, e := w.run(w.criterion, proofPass)
	w.f.accept(s, e)
	w.f.refuse(w.f.request(w.f.capture(nil, w.proof(w.criterion, map[model.InvocationRef]string{pass: "supports"}))), "invalid-transition")
	// The gate layer refuses on its own too, independently of the reducer.
	err := gateInstrumentValidation(context.Background(), nil, w.f.snapshot(), w.instrument, "evidence[0]")
	if admissionErrorCode(err) != "validation-unknown" {
		t.Fatalf("gate accepted an UNKNOWN-validation instrument: %v", err)
	}
}

func TestProofRequiresANamedJudgmentByItsAuthor(t *testing.T) {
	w := newProofWorld(t, true)
	pass, s, e := w.run(w.criterion, proofPass)
	w.f.accept(s, e)
	proof := w.proof(w.criterion, map[model.InvocationRef]string{pass: "supports"})
	proof.Judgment.Actor = model.Actor{ID: "someone-who-did-not-sign"}
	w.f.refuse(w.f.request(w.f.capture(nil, proof)), "attribution-mismatch")
	proof.Judgment.Actor = model.Actor{UnknownReason: "nobody named"}
	w.f.refuse(w.f.request(w.f.captureRaw(proof)), "invalid-field")
	w.f.author = model.Actor{UnknownReason: "packet author not recorded"}
	proof.Judgment.Actor = model.Actor{ID: "lane-c2"}
	w.f.refuse(w.f.request(w.f.capture(nil, proof)), "attribution-mismatch")
	if w.status(t) != reduce.StatusMeasured {
		t.Fatal("an unnamed judgment reached PROVEN")
	}
}

func TestProofCannotBorrowAnotherRevisionsObservations(t *testing.T) {
	w := newProofWorld(t, true)
	pass, s, e := w.run(w.criterion, proofPass)
	w.f.accept(s, e)
	claim := w.f.claim()
	claim.Spec.Assertion = "a revised finding"
	w.f.accept(w.f.capture(nil, &model.ClaimRevise{Provenance: claim.Provenance, Target: w.claim, ExpectedRevision: 1, Replacement: claim.Spec}))
	revised := w.claim
	revised.Revision = 2
	// A criterion for the revision exists, but no run carries it.
	other := w.fix(revised)
	w.f.refuse(w.f.request(w.f.capture(nil, w.proof(other, map[model.InvocationRef]string{pass: "supports"}))), "invalid-transition")
	// A second criterion on the SAME revision cannot use the first one's run.
	sibling := w.fix(w.claim)
	w.f.refuse(w.f.request(w.f.capture(nil, w.proof(sibling, map[model.InvocationRef]string{pass: "supports"}))), "invalid-transition")
	// Naming the revised claim with the old criterion is not even well formed.
	mixed := w.proof(w.criterion, map[model.InvocationRef]string{pass: "supports"})
	mixed.Claim = revised
	w.f.refuse(w.f.request(w.f.captureRaw(mixed)), "invalid-field")
	if p, _ := w.f.snapshot().ClaimAt(revised); p.Status != reduce.StatusUnmeasured || !strings.Contains(string(p.Spec.Assertion), "revised") {
		t.Fatalf("revision 2 borrowed revision 1's observation: %+v", p.Status)
	}
}
