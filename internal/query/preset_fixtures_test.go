package query

// Builders for the view tests: instruments, claims at each status,
// decisions and invocations shared by several test files. Assertions live in
// the *_test.go files that use these.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"whosaidso/internal/model"
	"whosaidso/internal/store"
)

// presetStart follows the clock: store.Transact stamps each fixture bundle
// with the wall clock, and a run must start after its criterion's bundle was
// recorded (reduce/freeze.go).
var presetStart = time.Now().UTC().Truncate(time.Second).Add(time.Hour)

func known[T any](v T) model.Availability[T] {
	return model.Availability[T]{State: model.Known, Value: &v}
}
func notKnown[T any](why string) model.Availability[T] {
	return model.Availability[T]{State: model.Unknown, Reason: why}
}
func prov(who string) model.Provenance {
	return model.Provenance{Author: model.Actor{ID: who}, SourceRefs: []model.ArtifactRef{}}
}
func gitInput(path string) model.ArtifactRef {
	return model.ArtifactRef{Kind: "git", Git: &model.GitPin{ObjectFormat: "sha1", Commit: strings.Repeat("a", 40), Path: path},
		Selector: model.Selector{Kind: "whole"}}
}
func instrumentSpec(validated bool) model.InstrumentSpec {
	v := notKnown[model.InstrumentValidation]("never checked against a case with a known answer")
	if validated {
		v = known(model.InstrumentValidation{Ref: testArtifact(), Version: "1"})
	}
	return model.InstrumentSpec{QuestionAnswered: "how many lines each file has", BlindTo: "generated code",
		NotAnswered: "whether a file should be shorter", ConfigSurface: []string{"root"}, DangerousDefaults: []string{"reports zero outside a module"},
		ValidRange: "Go modules", ImplementationRef: testArtifact(), Validation: v}
}
func claimSpec(tags ...string) model.ClaimSpec {
	refs := []model.ExternalReference{}
	for _, tag := range tags {
		refs = append(refs, model.ExternalReference{Tag: tag, Citation: "a vendor page says so"})
	}
	return model.ClaimSpec{Assertion: "replay is deterministic", Falsifier: "two replays disagree", Scope: testScope(), ExternalRefs: refs}
}
func decisionSpec() model.DecisionSpec {
	return model.DecisionSpec{Question: "does BLOCKED win over READY", Options: []string{"yes", "no"},
		WaitingActor: model.Actor{ID: "owner"}, Scope: testScope()}
}
func criterionRef(claim int) model.CriterionRef {
	return model.CriterionRef{Claim: testRef(claim, 1), CriterionID: testID(claim + 600), Revision: 1}
}
func criterionFix(claim int) *model.CriterionFix {
	n := json.Number("0")
	return &model.CriterionFix{Claim: testRef(claim, 1), CriterionID: testID(claim + 600), Revision: 1,
		Expression: model.CriterionExpression{ResultSelector: testArtifact(), Unit: "failures",
			Population: model.Population{Identity: "all cases", Selector: testArtifact(), Denominator: "all cases"},
			Operator:   model.Equal, Target: model.Scalar{Type: "number", Number: &n}, Reducer: model.All},
		Policy: model.EvaluationPolicy{Inclusion: "entire-criterion-family", Retry: "retain-all"},
		Author: model.Actor{ID: "lane-a"}, SourceRefs: []model.ArtifactRef{}}
}
func envelope(id, attempt, instrument, claim int, inputs ...model.ArtifactRef) model.InvocationEnvelope {
	criterion := notKnown[model.CriterionRef]("no criterion named")
	if claim > 0 {
		criterion = known(criterionRef(claim))
	}
	return model.InvocationEnvelope{InvocationID: testID(id), AttemptID: testID(attempt), InstrumentRef: testRef(instrument, 1),
		CriterionRef: criterion,
		ExecutionSourceIdentity: model.ExecutionIdentity{Project: projectID, MachineID: notKnown[model.ID]("not persisted"),
			SourceRefs: []model.ArtifactRef{testArtifact()}, Head: notKnown[model.GitHead]("not observed"), Dirty: notKnown[bool]("not observed")},
		Argv: []string{"go", "test", "./..."}, InputRefs: append([]model.ArtifactRef{}, inputs...),
		ConfigRequested: map[string]model.Scalar{}, ConfigEffective: notKnown[map[string]model.Availability[model.Scalar]]("not observed"),
		ConditionsDeclared: map[string]model.Scalar{}, ConditionsObserved: notKnown[map[string]model.Availability[model.Scalar]]("not observed"),
		Isolation: notKnown[model.Isolation]("not observed"), StartedAt: presetStart,
		ObservedAt: notKnown[time.Time]("not yet observed"), Outcome: notKnown[model.ProcessOutcome]("not yet observed"),
		Outputs: notKnown[[]model.RunOutput]("not yet observed"), Visual: notKnown[model.VisualObservation]("not visual")}
}
func seal(env model.InvocationEnvelope, exit int, after time.Duration) *model.InvocationSeal {
	env.ObservedAt = known(env.StartedAt.Add(after))
	env.Outcome = known(model.ProcessOutcome{Kind: "exit", ExitCode: &exit})
	env.Outputs = known([]model.RunOutput{{Name: "stdout", SHA256: model.HashBytes([]byte("U09")), Length: 3, MediaType: "text/plain"}})
	return &model.InvocationSeal{StartRef: model.InvocationRef{Project: projectID, InvocationID: env.InvocationID}, Envelope: env}
}
func authority() model.Authority {
	return model.Authority{Actor: model.Actor{ID: "owner"}, SourceRef: testArtifact(), Selector: model.Selector{Kind: "whole"}, Scope: testScope()}
}

// presetWorld admits: task 1 IN FLIGHT (attempt 70); instruments 10 (validated)
// and 11 (UNKNOWN); claims 20 UNMEASURED with a VERIFIED tag, 21 MEASURED by a
// failed two-hour run inside scope, 22 PROVEN by a run outside scope; decision
// 30 OPEN, 31 DECIDED; and run 52, never sealed, with a pathless input.
func presetWorld(t *testing.T, p store.Project) {
	t.Helper()
	readyControl(t, p)
	appendEvents(t, p, 101, admitted(101,
		&model.InstrumentDeclare{ID: testID(10), Provenance: prov("lane-a"), Spec: instrumentSpec(true)},
		&model.InstrumentDeclare{ID: testID(11), Provenance: prov("lane-a"), Spec: instrumentSpec(false)},
		&model.ClaimAssert{ID: testID(20), Provenance: prov("lane-e"), Spec: claimSpec("VERIFIED")},
		&model.ClaimAssert{ID: testID(21), Provenance: prov("lane-e"), Spec: claimSpec()},
		&model.ClaimAssert{ID: testID(22), Provenance: prov("lane-e"), Spec: claimSpec()},
		criterionFix(21), criterionFix(22),
		&model.DecisionOpen{ID: testID(30), Provenance: prov("lane-c"), Spec: decisionSpec()},
		&model.DecisionOpen{ID: testID(31), Provenance: prov("lane-c"), Spec: decisionSpec()})...)
	appendEvents(t, p, 102, &model.TaskStart{Task: testRef(1, 1), AttemptID: testID(70), Actor: model.Actor{ID: "worker"}})
	inside := envelope(50, 70, 10, 21, gitInput("internal/query/query.go"))
	outside := envelope(51, 70, 10, 22, gitInput("internal/query/query.go"), gitInput("internal/reduce/task.go"))
	unsealed := envelope(52, 70, 11, 0, testArtifact())
	starts := []model.TypedEvent{&model.InvocationStart{Envelope: inside}, &model.InvocationStart{Envelope: outside},
		&model.InvocationStart{Envelope: unsealed}}
	appendEvents(t, p, 103, admitted(103, starts...)...)
	appendEvents(t, p, 104, seal(inside, 1, 2*time.Hour), seal(outside, 0, 3*time.Second))
	appendEvents(t, p, 105, admitted(105,
		&model.ProofAdmit{Claim: testRef(22, 1), CriterionRef: criterionRef(22),
			Evidence: []model.ObservationDisposition{{InvocationRef: model.InvocationRef{Project: projectID, InvocationID: testID(51)},
				Disposition: "supports", Reason: "all cases pass"}},
			Judgment: model.ResponsibleJudgment{Actor: model.Actor{ID: "reviewer"}, Reason: "the criterion holds"}, Verdict: model.VerdictSupports},
		&model.DecisionDispose{Decision: testRef(31, 1), Disposition: "approved", Quote: "yes, BLOCKED wins",
			Scope: testScope(), Authority: authority()})...)
}

// admitted appends admission's review to events: one packet per event,
// authored by the actor the event names (the criterion author, the proof
// judgment, otherwise lane-a) and captured a minute after presetStart, so the
// reducer can check authorship and freezing from the ledger.
func admitted(n int, events ...model.TypedEvent) []model.TypedEvent {
	return admittedAs(n, nil, events...)
}

// admittedAs is admitted with the packet author of event i set to as[i]: a
// receipt is its attempt holder's own packet, so a fixture names that holder.
func admittedAs(n int, as map[int]string, events ...model.TypedEvent) []model.TypedEvent {
	review := &model.ReviewAdmit{Outcome: "accepted", Actor: model.Actor{ID: "reviewer"}, Reason: "fixture admission",
		Authors: map[model.ID]model.Actor{}, CapturedAt: map[model.ID]model.Availability[time.Time]{}}
	for i, e := range events {
		packet := testID(n*100 + i)
		author := model.Actor{ID: "lane-a"}
		switch e := e.(type) {
		case *model.CriterionFix:
			author = e.Author
		case *model.ProofAdmit:
			author = e.Judgment.Actor
		}
		if id, ok := as[i]; ok {
			author = model.Actor{ID: id}
		}
		review.Packets = append(review.Packets, model.PacketRef{CommandID: packet, Digest: model.HashBytes([]byte(packet))})
		review.EventPackets = append(review.EventPackets, packet)
		review.Authors[packet] = author
		review.CapturedAt[packet] = known(presetStart.Add(time.Minute).UTC())
	}
	return append(events, review)
}
