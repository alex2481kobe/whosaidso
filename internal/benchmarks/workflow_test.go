// This file defines the synthetic engineering workflow: tasks, measured claims,
// three-run proofs and handbacks. Storage setup and timed operations live elsewhere.
package benchmarks

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"whosaidso/internal/evidence"
	"whosaidso/internal/model"
)

var resultBody = []byte(`{"results":{"unit":"ms","population":"request corpus","denominator":"requests","values":[10,20]},"population":{"population":"request corpus","denominator":"requests","values":["request-a","request-b"]},"diagnostic":"` + strings.Repeat("x", 4096) + `"}`)
var implementation = []byte(`{"implementation":"synthetic request-latency measurement"}`)
var validationBody = []byte(`{"validated":"synthetic control and falsifier","version":"v1"}`)

func known[T any](v T) model.Availability[T] {
	return model.Availability[T]{State: model.Known, Value: &v}
}
func unknown[T any]() model.Availability[T] {
	return model.Availability[T]{State: model.Unknown, Reason: "not observed in this fixture"}
}

func pin(body []byte, path string) model.ArtifactRef {
	return model.ArtifactRef{Kind: "content", Content: &model.ContentPin{SHA256: model.HashBytes(body), Length: uint64(len(body)), MediaType: "application/json", Locators: []model.Locator{{Path: path}}}, Selector: model.Selector{Kind: "whole"}}
}

func provenance() model.Provenance {
	return model.Provenance{Author: author, SourceRefs: []model.ArtifactRef{}}
}
func scope() model.Scope {
	return model.Scope{SourcePaths: []string{"internal/service"}, ContextRefs: []model.RecordRef{}, AppliesWhen: "the frozen request corpus on the recorded source revision", Limitations: "synthetic measurements; no production load or network jitter"}
}

func (f *fixture) task() *model.TaskCreate {
	return &model.TaskCreate{ID: f.id(), Provenance: provenance(), Spec: model.TaskSpec{
		Intent: "verify request latency before integration", Subject: "service request path", Scope: scope(),
		NonGoals: []string{"production rollout"}, AcceptanceCriteria: []model.AcceptanceCriterion{{ID: f.id(), Revision: 1, Criterion: "all measured requests remain below the frozen latency threshold"}},
		ContextRefs: []model.RecordRef{}, ConstraintRefs: []model.RecordRef{}, Prerequisites: []model.Prerequisite{}, NextActor: author,
	}}
}

func (f *fixture) instrument() *model.InstrumentDeclare {
	return &model.InstrumentDeclare{ID: f.id(), Provenance: provenance(), Spec: model.InstrumentSpec{
		QuestionAnswered: "does every request in this corpus finish below the latency threshold",
		BlindTo:          "production contention and failures outside the request corpus", NotAnswered: "whether the service is semantically correct",
		ConfigSurface: []string{}, DangerousDefaults: []string{}, ValidRange: "synthetic local requests",
		ImplementationRef: pin(implementation, "tool.json"), Validation: known(model.InstrumentValidation{Ref: pin(validationBody, "validation.json"), Version: "v1"}),
	}}
}

func (f *fixture) criterion(claim model.RecordRef) *model.CriterionFix {
	result, population := pin(resultBody, "out/result.json"), pin(resultBody, "out/result.json")
	result.Selector = model.Selector{Kind: "json-pointer", Pointer: "/results"}
	population.Selector = model.Selector{Kind: "json-pointer", Pointer: "/population"}
	target := json.Number("50")
	return &model.CriterionFix{Claim: claim, CriterionID: f.id(), Revision: 1,
		Expression: model.CriterionExpression{ResultSelector: result, Unit: "ms", Population: model.Population{Identity: "request corpus", Selector: population, Denominator: "requests"}, Operator: model.Less, Target: model.Scalar{Type: "number", Number: &target}, Reducer: model.All},
		Policy:     model.EvaluationPolicy{Inclusion: "entire-criterion-family", Retry: "retain-all"}, Author: author, SourceRefs: []model.ArtifactRef{}}
}

func (f *fixture) envelope(attempt model.ID, criterion model.CriterionRef) model.InvocationEnvelope {
	return model.InvocationEnvelope{InvocationID: f.id(), AttemptID: attempt, InstrumentRef: f.Instrument, CriterionRef: known(criterion),
		ExecutionSourceIdentity: model.ExecutionIdentity{Project: f.Project.ID, MachineID: known(model.ID("7ZZZZZZZZZZZZZZZZZZZZZZZZZ")), SourceRefs: []model.ArtifactRef{}, Head: known(model.GitHead{ObjectFormat: "sha1", Commit: "0123456789abcdef0123456789abcdef01234567"}), Dirty: known(false)},
		Argv:                    []string{"measure-request-corpus"}, InputRefs: []model.ArtifactRef{}, ConfigRequested: map[string]model.Scalar{}, ConditionsDeclared: map[string]model.Scalar{},
		ConfigEffective: unknown[map[string]model.Availability[model.Scalar]](), ConditionsObserved: unknown[map[string]model.Availability[model.Scalar]](),
		Isolation: unknown[model.Isolation](), StartedAt: time.Now().UTC(), ObservedAt: unknown[time.Time](), Outcome: unknown[model.ProcessOutcome](), OutputRefs: unknown[[]model.ArtifactRef](), Visual: unknown[model.VisualObservation](),
	}
}

func (f *fixture) seal(env model.InvocationEnvelope) *model.InvocationSeal {
	env.ObservedAt = known(time.Now().UTC())
	exit := 0
	env.Outcome = known(model.ProcessOutcome{Kind: "exit", ExitCode: &exit})
	env.OutputRefs = known([]model.ArtifactRef{pin(resultBody, evidence.RunDir(env.InvocationID)+"/out/result.json")})
	env.ConfigEffective = known(map[string]model.Availability[model.Scalar]{})
	env.ConditionsObserved = known(map[string]model.Availability[model.Scalar]{})
	return &model.InvocationSeal{StartRef: model.InvocationRef{Project: f.Project.ID, InvocationID: env.InvocationID}, Envelope: env}
}

// Each ten-bundle cycle has two tasks, one claim, one frozen criterion, three
// runs, a proof, and ten reviews. Every fourth measured task remains in flight;
// others alternate stopped and success-awaiting-acceptance. Every fourth cycle
// also opens a decision; every fifth backlog task has an explicit resume hold.
// Five stable instruments are declared once. Output blobs are ~4 KiB per run.
func (f *fixture) cycle(i int) {
	task := f.task()
	claim := &model.ClaimAssert{ID: f.id(), Provenance: provenance(), Spec: model.ClaimSpec{
		Assertion: fmt.Sprintf("request corpus %d completes below 50 ms", i), Falsifier: "one request at or above 50 ms", Scope: scope(), ExternalRefs: []model.ExternalReference{}}}
	events := []model.TypedEvent{task, claim}
	if i == 0 {
		for j := 0; j < 5; j++ {
			instrument := f.instrument()
			if j == 0 {
				f.Instrument = f.ref(instrument.ID)
			}
			events = append(events, instrument)
		}
		for path, body := range map[string][]byte{"tool.json": implementation, "validation.json": validationBody, "out/result.json": resultBody} {
			put(f.t, filepath.Join(f.Project.Root, path), body)
			put(f.t, filepath.Join(f.Project.Root, f.Project.ArtifactDir(), string(model.HashBytes(body))), body)
		}
	}
	f.append(nil, events...)
	fix := f.criterion(f.ref(claim.ID))
	f.append(nil, fix)
	criterion := model.CriterionRef{Claim: fix.Claim, CriterionID: fix.CriterionID, Revision: 1}
	attempt := f.id()
	f.append(nil, &model.TaskStart{Task: f.ref(task.ID), Actor: author, AttemptID: attempt})
	proof := model.ProofAdmit{Claim: criterion.Claim, CriterionRef: criterion, Verdict: model.VerdictSupports,
		Evidence: []model.ObservationDisposition{}, Judgment: model.ResponsibleJudgment{Actor: author, Reason: "all three comparable runs satisfy the frozen criterion; production behavior remains unmeasured"}}
	for j := 0; j < 3; j++ {
		env := f.envelope(attempt, criterion)
		start := &model.InvocationStart{Envelope: env}
		if j == 0 {
			f.append(nil, start)
			f.append([][]byte{resultBody}, f.seal(env))
		} else {
			f.append([][]byte{resultBody}, start, f.seal(env))
		}
		proof.Evidence = append(proof.Evidence, model.ObservationDisposition{InvocationRef: model.InvocationRef{Project: f.Project.ID, InvocationID: env.InvocationID}, Disposition: "supports", Reason: "measured below the threshold on the same source and machine"})
	}
	f.append(nil, &proof)
	if i%4 == 0 {
		f.append(nil, &model.DecisionOpen{ID: f.id(), Provenance: provenance(), Spec: model.DecisionSpec{Question: "which integration window should receive this change", Options: []string{"next window", "after production rehearsal"}, WaitingActor: reviewer, Scope: scope()}})
	} else {
		outcome := model.AttemptSuccess
		if i%4 == 3 {
			outcome = model.AttemptStopped
		}
		f.append(nil, &model.AttemptTerminal{Task: f.ref(task.ID), AttemptID: attempt, Outcome: outcome, Reason: "local measurement work concluded", NextAction: "review integration and acceptance evidence", DeliveryRefs: []model.ArtifactRef{}})
	}
	backlog := f.task()
	backlog.Spec.ContextRefs = []model.RecordRef{f.ref(claim.ID), f.Instrument}
	events = []model.TypedEvent{backlog}
	// Keep a terminal handback even in the ten-bundle smoke fixture, while
	// preserving the first measured task's live attempt for whosaidso run.
	if i == 0 {
		secondary := f.id()
		events = append(events, &model.TaskStart{Task: f.ref(backlog.ID), Actor: author, AttemptID: secondary},
			&model.AttemptTerminal{Task: f.ref(backlog.ID), AttemptID: secondary, Outcome: model.AttemptStopped, Reason: "waiting for review before more work", NextAction: "resume after independent review", DeliveryRefs: []model.ArtifactRef{}})
	}
	if i%5 == 0 {
		events = append(events, &model.BlockerHold{Task: f.ref(backlog.ID), BlockerID: f.id(), Reason: model.BlockerResume, Actor: reviewer, Criterion: "review the predecessor measurement before dispatch"})
	}
	f.append(nil, events...)
	if i == 0 {
		f.Task = f.ref(task.ID)
		f.Claim = criterion.Claim
		f.Criterion = criterion
		f.Attempt = attempt
		f.Proof = proof
	}
}
