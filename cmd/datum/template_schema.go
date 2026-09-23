package main

// The few schema facts `datum template` cannot read off the model's Go types
// by reflection live here: the closed event list, enum members (they sit in
// validate methods), tagged unions and which ids an event mints. Each table is
// held to the model by template_test.go: every template must decode through
// the strict decoder once filled, every enum set in internal/model must appear
// here, and every event type the model declares must be listed. Rendering the
// skeleton lives in template.go.

import (
	"reflect"

	"datum/internal/model"
)

// templateEvents is the closed event set, in the model's registry order.
var templateEvents = []model.TypedEvent{
	&model.TaskCreate{}, &model.TaskAmend{}, &model.TaskStart{}, &model.TaskTakeover{}, &model.AttemptTerminal{},
	&model.TaskClose{}, &model.BlockerHold{}, &model.BlockerClear{}, &model.InvocationStart{}, &model.InvocationSeal{},
	&model.SourceIntake{}, &model.ClaimAssert{}, &model.ClaimRevise{}, &model.CriterionFix{}, &model.ProofAdmit{},
	&model.DecisionOpen{}, &model.DecisionRevise{}, &model.DecisionDispose{}, &model.Supersede{}, &model.Correction{},
	&model.InstrumentDeclare{}, &model.InstrumentRevise{}, &model.TrustWithdraw{}, &model.ReviewAdmit{}, &model.ArtifactDispose{},
}

// templateMints names, per event type, the id paths the event creates. They
// are minted fresh; every other id is a reference the author must look up.
var templateMints = map[model.EventType][]string{
	"task.create":        {"id", "spec.acceptance_criteria[0].id"},
	"claim.assert":       {"id"},
	"decision.open":      {"id"},
	"instrument.declare": {"id"},
	"task.start":         {"attempt_id"},
	"task.takeover":      {"attempt_id"},
	"blocker.hold":       {"blocker_id"},
	"source.intake":      {"source_id"},
	"criterion.fix":      {"criterion_id"},
	"invocation.start":   {"envelope.invocation_id"},
}

var templateMintNotes = map[model.EventType]string{
	"criterion.fix": "criterion_id is fresh for revision 1; a later revision reuses the criterion's existing id",
}

// templateEnumTypes are named string types whose members a validate method lists.
var templateEnumTypes = map[reflect.Type][]string{
	reflect.TypeOf(model.AttemptOutcome("")):     {"success", "stopped", "refused", "no-reading", "measurement-impossible", "runner-died", "harness-broken", "out-of-scope", "blocked-mid-task"},
	reflect.TypeOf(model.ClosureOutcome("")):     {"success", "cancelled", "withdrawn", "waived"},
	reflect.TypeOf(model.BlockerReason("")):      {"prerequisite", "awaiting-acceptance", "resume", "reconciliation"},
	reflect.TypeOf(model.ComparisonOperator("")): {"eq", "ne", "lt", "le", "gt", "ge"},
	reflect.TypeOf(model.CriterionReducer("")):   {"all", "any", "count"},
	reflect.TypeOf(model.Isolation("")):          {"clean"},
	reflect.TypeOf(model.SelfAdmissionState("")): {"true", "false", "unknown"},
}

type templateField struct {
	owner reflect.Type
	field string
}

// templateEnumFields are plain string fields whose members a validate method lists.
var templateEnumFields = map[templateField][]string{
	{reflect.TypeOf(model.DecisionDispose{}), "Disposition"}:        {"approved", "rejected", "withdrawn"},
	{reflect.TypeOf(model.ExternalReference{}), "Tag"}:              {"VERIFIED", "VENDOR CLAIM", "REPORTED MEASUREMENT"},
	{reflect.TypeOf(model.ObservationDisposition{}), "Disposition"}: {"supports", "contradicts", "inapplicable", "inconclusive"},
	{reflect.TypeOf(model.EvaluationPolicy{}), "Inclusion"}:         {"entire-criterion-family"},
	{reflect.TypeOf(model.EvaluationPolicy{}), "Retry"}:             {"retain-all"},
	{reflect.TypeOf(model.ReviewAdmit{}), "Outcome"}:                {"accepted", "correction-requested", "rejected"},
	{reflect.TypeOf(model.ReviewedInvocation{}), "Event"}:           {"invocation.start", "invocation.seal"},
	{reflect.TypeOf(model.Prerequisite{}), "Kind"}:                  {"task-success", "claim-proof", "decision-approved"},
	{reflect.TypeOf(model.GitPin{}), "ObjectFormat"}:                {"sha1", "sha256"},
	{reflect.TypeOf(model.GitHead{}), "ObjectFormat"}:               {"sha1", "sha256"},
	{reflect.TypeOf(model.ProofAdmit{}), "Verdict"}:                 {"supports", "refutes"},
}

// templateRequired are keys the model decodes when absent, so bundles written
// before the key existed still replay, but admission requires on every new
// event. Reflection would call them optional; the note says why they are not.
var templateRequired = map[templateField]string{
	{reflect.TypeOf(model.ProofAdmit{}), "Verdict"}: "required at admission; only a proof admitted before R14.1 has none",
}

// templateFieldUnions narrow a union at one field to the members the model
// accepts there. The skeleton holds only the kept members' keys.
var templateFieldUnions = map[templateField]templateUnion{
	{reflect.TypeOf(model.TaskSpec{}), "Accepter"}: {members: []string{"id"},
		branches: map[string][]string{"id": {"id"}},
		note:     "a known actor id; an unknown accepter could never close, so unknown_reason is refused; delete the whole key to let anyone accept"},
}

// templateUnion is a tagged choice: the tag's member decides which of the
// branch keys stay. An empty tag is an untagged exactly-one-of (Actor).
type templateUnion struct {
	tag      string
	members  []string
	branches map[string][]string
	note     string
}

// templateUnions lists each union with the conservative member first: the
// first member is what a filled template with no other choice decodes as.
var templateUnions = map[reflect.Type]templateUnion{
	reflect.TypeOf(model.Selector{}): {tag: "kind", members: []string{"whole", "json-pointer"},
		branches: map[string][]string{"json-pointer": {"pointer"}}},
	reflect.TypeOf(model.Scalar{}): {tag: "type", members: []string{"number", "string", "bool"},
		branches: map[string][]string{"number": {"number"}, "string": {"string"}, "bool": {"bool"}}},
	reflect.TypeOf(model.ProcessOutcome{}): {tag: "kind", members: []string{"exit", "signal", "spawn-failed"},
		branches: map[string][]string{"exit": {"exit_code"}, "signal": {"signal"}, "spawn-failed": {"diagnostic"}}},
	reflect.TypeOf(model.CorrectionTarget{}): {tag: "kind", members: []string{"record", "criterion", "support"},
		branches: map[string][]string{"record": {"record"}, "criterion": {"criterion"}, "support": {"support"}}},
	reflect.TypeOf(model.Prerequisite{}): {tag: "waiver_policy", members: []string{"forbid", "allow-with-authority"},
		branches: map[string][]string{"allow-with-authority": {"authority"}}},
	reflect.TypeOf(model.ArtifactRef{}): {tag: "kind", members: []string{"git", "content"},
		branches: map[string][]string{"git": {"git"}, "content": {"content"}},
		note:     "the other pin may stay as corroboration; both are then checked against each other"},
	reflect.TypeOf(model.Actor{}): {members: []string{"id", "unknown_reason"},
		branches: map[string][]string{"id": {"id"}, "unknown_reason": {"unknown_reason"}},
		note:     "keep exactly one: a known actor id, or the reason the actor is unknown"},
}

// templateAvailability is every Availability[T]: unknown needs a reason, known a value.
var templateAvailability = templateUnion{tag: "state", members: []string{"unknown", "known"},
	branches: map[string][]string{"unknown": {"reason"}, "known": {"value"}}}
