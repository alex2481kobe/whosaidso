// Lane E's independent U02 attacks on the four authored payloads and the
// invocation envelope. This file owns the shared payload constructors used by
// events_test.go too, so the attack fixtures are built once from the contract
// tables rather than from the implementation under test.
//
// Out of scope here on purpose: whether an event is admissible in current state
// (U08), what a reducer makes of it (U05/U06) and whether pinned bytes actually
// resolve (U07). This file only asks whether a shape can carry a lie.
package acceptance_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whosaidso/internal/model"
)

const recProject = model.ProjectID("example/acceptance")

var recWhen = time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

// recID mints a distinct valid ULID per fixture slot so a reference that goes
// missing in a walker cannot be hidden by another reference that looks the same.
func recID(n int) model.ID {
	return model.ID(fmt.Sprintf("01K5V8R%03d0000000000000000", n))
}

func recDigest(b byte) model.Digest {
	return model.Digest(strings.Repeat(string(rune(b)), 64))
}

func recKnown[T any](v T) model.Availability[T] {
	return model.Availability[T]{State: model.Known, Value: &v}
}

func recUnknown[T any](reason string) model.Availability[T] {
	return model.Availability[T]{State: model.Unknown, Reason: reason}
}

func recActor(id string) model.Actor { return model.Actor{ID: id} }

func recRef(id model.ID, revision uint64) model.RecordRef {
	return model.RecordRef{Project: recProject, RecordID: id, Revision: model.Revision(revision)}
}

func recGit(path string) model.ArtifactRef {
	return model.ArtifactRef{
		Kind:     "git",
		Git:      &model.GitPin{ObjectFormat: "sha1", Commit: strings.Repeat("a", 40), Path: path},
		Selector: model.Selector{Kind: "whole"},
	}
}

func recContent(d model.Digest, length uint64) model.ArtifactRef {
	return model.ArtifactRef{
		Kind: "content",
		Content: &model.ContentPin{
			SHA256: d, Length: length, MediaType: "application/json",
			Locators: []model.Locator{{Path: ".whosaidso/artifacts/" + string(d)}},
		},
		Selector: model.Selector{Kind: "whole"},
	}
}

// recScope takes its context reference as a parameter so every payload slot can
// hold a different one. A walker that drops one link is then visible.
func recScope(context model.ID) model.Scope {
	return model.Scope{
		SourcePaths: []string{"internal/model/records.go"},
		ContextRefs: []model.RecordRef{recRef(context, 1)},
		AppliesWhen: "the payload is decoded at the wire boundary",
		Limitations: "says nothing about whether this event may be admitted",
	}
}

func recAuthority(context model.ID) model.Authority {
	return model.Authority{
		Actor:     recActor("owner"),
		SourceRef: recGit("docs/decisions.md"),
		Selector:  model.Selector{Kind: "json-pointer", Pointer: "/rulings/3"},
		Scope:     recScope(context),
	}
}

func recProvenance() model.Provenance {
	return model.Provenance{SourceRefs: []model.ArtifactRef{recGit("docs/plan.md")}}
}

func recTaskSpec() model.TaskSpec {
	return model.TaskSpec{
		Intent:   "refuse a payload that can carry a forged observation",
		Subject:  "internal/model/records.go",
		Scope:    recScope(recID(20)),
		NonGoals: []string{"reducing events", "resolving artifact bytes"},
		AcceptanceCriteria: []model.AcceptanceCriterion{{
			ID: recID(21), Revision: 1, Criterion: "each negative fails for its own reason",
		}},
		ContextRefs:    []model.RecordRef{recRef(recID(22), 3)},
		ConstraintRefs: []model.RecordRef{recRef(recID(23), 2)},
		Prerequisites: []model.Prerequisite{{
			Kind:         "claim-proof",
			Target:       recRef(recID(24), 4),
			WaiverPolicy: "allow-with-authority",
			Authority:    ptr(recAuthority(recID(25))),
		}},
		NextActor: recActor("coordinator"),
	}
}

func recClaimSpec() model.ClaimSpec {
	return model.ClaimSpec{
		Assertion: "DecodeEvent refuses every member outside the closed set",
		Falsifier: "one unlisted type name decodes into a typed payload",
		Scope:     recScope(recID(30)),
		ExternalRefs: []model.ExternalReference{{
			Tag:       "VENDOR CLAIM",
			Citation:  "the encoding/json package documentation",
			RecordRef: ptr(recRef(recID(31), 1)),
		}},
	}
}

func recDecisionSpec() model.DecisionSpec {
	return model.DecisionSpec{
		Question:     "does the invocation envelope stay honest before launch",
		Options:      []string{"refuse observed fields in a start", "allow them"},
		WaitingActor: recActor("owner"),
		Scope:        recScope(recID(35)),
	}
}

func recInstrumentSpec() model.InstrumentSpec {
	return model.InstrumentSpec{
		QuestionAnswered:  "does this payload decode into exactly one typed event",
		BlindTo:           "whether the referenced records exist in the ledger",
		NotAnswered:       "whether the authored claim is true",
		ConfigSurface:     []string{"strict", "max_depth"},
		DangerousDefaults: []string{"strict defaults to on and hides a permissive caller"},
		ValidRange:        "payloads under one megabyte of UTF-8 JSON",
		ImplementationRef: recGit("internal/model/events.go"),
		Validation: recKnown(model.InstrumentValidation{
			Ref:     recGit("internal/model/events_test.go"),
			Version: "u02-1",
		}),
	}
}

// recEnvelope is a PRE-LAUNCH envelope: every field a running process supplies
// is honestly unknown, with a stated reason.
func recEnvelope() model.InvocationEnvelope {
	return model.InvocationEnvelope{
		InvocationID:  recID(40),
		AttemptID:     recID(41),
		InstrumentRef: recRef(recID(42), 2),
		CriterionRef:  recUnknown[model.CriterionRef]("this measurement has no executable criterion"),
		ExecutionSourceIdentity: model.ExecutionIdentity{
			Project:    recProject,
			MachineID:  recKnown(recID(43)),
			SourceRefs: []model.ArtifactRef{recGit("cmd/whosaidso/main.go")},
			Head:       recKnown(model.GitHead{ObjectFormat: "sha1", Commit: strings.Repeat("b", 40)}),
			Dirty:      recKnown(false),
		},
		Argv:               []string{"go", "test", "./internal/acceptance/"},
		InputRefs:          []model.ArtifactRef{},
		ConfigRequested:    map[string]model.Scalar{"strict": recBool(true)},
		ConfigEffective:    recUnknown[map[string]model.Availability[model.Scalar]]("the process has not run"),
		ConditionsDeclared: map[string]model.Scalar{"max_depth": recNumber("64")},
		ConditionsObserved: recUnknown[map[string]model.Availability[model.Scalar]]("the process has not run"),
		Isolation:          recUnknown[model.Isolation]("isolation is established by the runner, not by intent"),
		StartedAt:          recWhen,
		ObservedAt:         recUnknown[time.Time]("the process has not run"),
		Outcome:            recUnknown[model.ProcessOutcome]("the process has not run"),
		Outputs:            recUnknown[[]model.RunOutput]("the process has not run"),
		Visual:             recUnknown[model.VisualObservation]("the process has not run"),
	}
}

func recNumber(text string) model.Scalar {
	n := json.Number(text)
	return model.Scalar{Type: "number", Number: &n}
}

func recBool(v bool) model.Scalar { return model.Scalar{Type: "bool", Bool: &v} }

func ptr[T any](v T) *T { return &v }

// ---- fixture editing ------------------------------------------------------

// recEncode is the good control for every negative below: a payload that will
// not encode cannot prove anything about a payload that will not decode.
func recEncode(t *testing.T, payload model.TypedEvent) model.Event {
	t.Helper()
	e, err := model.EncodeEvent(payload)
	if err != nil {
		t.Fatalf("control payload %s must encode before any refusal is claimed: %v", payload.EventType(), err)
	}
	if _, err := model.DecodeEvent(e); err != nil {
		t.Fatalf("control payload %s must decode before any refusal is claimed: %v", payload.EventType(), err)
	}
	return e
}

func recSplitPath(path string) []string {
	return strings.Split(strings.ReplaceAll(strings.ReplaceAll(path, "[", ".["), "..[", ".["), ".")
}

// recEdit rewrites one JSON location in an already valid payload so a negative
// differs from its control by exactly one thing.
func recEdit(t *testing.T, e model.Event, path string, value any, drop bool) model.Event {
	t.Helper()
	var root any
	dec := json.NewDecoder(bytes.NewReader(e.Data))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	cur := root
	parts := recSplitPath(path)
	for i, part := range parts {
		last := i == len(parts)-1
		if strings.HasPrefix(part, "[") {
			var idx int
			if _, err := fmt.Sscanf(part, "[%d]", &idx); err != nil {
				t.Fatalf("bad fixture path %q", path)
			}
			arr, ok := cur.([]any)
			if !ok || idx >= len(arr) {
				t.Fatalf("fixture path %q has no element %s", path, part)
			}
			if last {
				arr[idx] = value
				break
			}
			cur = arr[idx]
			continue
		}
		obj, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("fixture path %q reaches %q outside an object", path, part)
		}
		if last {
			if drop {
				if _, present := obj[part]; !present {
					t.Fatalf("fixture path %q is already absent, so dropping it proves nothing", path)
				}
				delete(obj, part)
			} else {
				obj[part] = value
			}
			break
		}
		next, present := obj[part]
		if !present {
			t.Fatalf("fixture path %q stops at a missing %q", path, part)
		}
		cur = next
	}
	out, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("edited fixture cannot be re-encoded: %v", err)
	}
	return model.Event{Type: e.Type, Data: out}
}

func recSet(t *testing.T, e model.Event, path string, value any) model.Event {
	t.Helper()
	return recEdit(t, e, path, value, false)
}

func recDrop(t *testing.T, e model.Event, path string) model.Event {
	t.Helper()
	return recEdit(t, e, path, nil, true)
}

func recCode(err error) string {
	var f *model.Fault
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
}

// recMustRefuse is the assertion every attack ends in. It names the input in the
// failure line so a reader knows what got through without opening this file.
func recMustRefuse(t *testing.T, what string, e model.Event) {
	t.Helper()
	decoded, err := model.DecodeEvent(e)
	if err == nil {
		t.Errorf("%s was ACCEPTED as %s. It must be refused. payload: %s", what, e.Type, e.Data)
		return
	}
	if decoded != nil {
		t.Errorf("%s returned both a payload and an error: %v", what, err)
	}
	if code := recCode(err); code == "" {
		t.Errorf("%s was refused with an untyped error %v. A machine-readable Fault code is required", what, err)
	}
}

func recMustAccept(t *testing.T, what string, e model.Event) {
	t.Helper()
	if _, err := model.DecodeEvent(e); err != nil {
		t.Errorf("%s must be accepted and was refused: %v. payload: %s", what, err, e.Data)
	}
}

// recBlanks are strings a human reads as no answer at all. The first group is
// Unicode White_Space. The second group renders as nothing but is not
// White_Space, so a TrimSpace based emptiness rule never sees it.
var recBlanks = []struct {
	name string
	text string
}{
	{"a plain space", " "},
	{"a tab", "\t"},
	{"a no-break space U+00A0", string(rune(0x00a0))},
	{"an em space U+2003", string(rune(0x2003))},
	{"an ideographic space U+3000", string(rune(0x3000))},
	{"a zero width space U+200B", string(rune(0x200b))},
	{"a right-to-left mark U+200F", string(rune(0x200f))},
	{"a byte order mark U+FEFF", string(rune(0xfeff))},
	{"a word joiner U+2060", string(rune(0x2060))},
}

// ---- attacks --------------------------------------------------------------

func TestSchemaAuthoredPayloadsAndEnvelopeRoundTripBeforeAnyRefusalIsClaimed(t *testing.T) {
	controls := []model.TypedEvent{
		&model.TaskCreate{Provenance: recProvenance(), ID: recID(1), Spec: recTaskSpec()},
		&model.ClaimAssert{Provenance: recProvenance(), ID: recID(2), Spec: recClaimSpec()},
		&model.DecisionOpen{Provenance: recProvenance(), ID: recID(3), Spec: recDecisionSpec()},
		&model.InstrumentDeclare{Provenance: recProvenance(), ID: recID(4), Spec: recInstrumentSpec()},
		&model.InvocationStart{Envelope: recEnvelope()},
	}
	for _, payload := range controls {
		t.Run(string(payload.EventType()), func(t *testing.T) {
			e := recEncode(t, payload)
			decoded, err := model.DecodeEvent(e)
			if err != nil {
				t.Fatalf("valid payload refused: %v", err)
			}
			if decoded.EventType() != payload.EventType() {
				t.Fatalf("decoded as %s, authored as %s", decoded.EventType(), payload.EventType())
			}
			again, err := model.EncodeEvent(decoded)
			if err != nil {
				t.Fatalf("decoded payload no longer encodes: %v", err)
			}
			if !bytes.Equal(again.Data, e.Data) {
				t.Fatalf("round trip changed the bytes:\n before %s\n after  %s", e.Data, again.Data)
			}
		})
	}
}

func TestSchemaRequiredSemanticsRefuseEveryStringThatRendersAsNothing(t *testing.T) {
	instrument := recEncode(t, &model.InstrumentDeclare{Provenance: recProvenance(), ID: recID(4), Spec: recInstrumentSpec()})
	task := recEncode(t, &model.TaskCreate{Provenance: recProvenance(), ID: recID(1), Spec: recTaskSpec()})
	claim := recEncode(t, &model.ClaimAssert{Provenance: recProvenance(), ID: recID(2), Spec: recClaimSpec()})
	decision := recEncode(t, &model.DecisionOpen{Provenance: recProvenance(), ID: recID(3), Spec: recDecisionSpec()})

	fields := []struct {
		name  string
		event model.Event
		path  string
	}{
		// An instrument that declares no blind spot is the thing the whole
		// system exists to refuse, so this is the sharpest case in the table.
		{"instrument.declare spec.blind_to", instrument, "spec.blind_to"},
		{"instrument.declare spec.question_answered", instrument, "spec.question_answered"},
		{"instrument.declare spec.not_answered", instrument, "spec.not_answered"},
		{"instrument.declare spec.valid_range", instrument, "spec.valid_range"},
		{"instrument.declare spec.validation.value.version", instrument, "spec.validation.value.version"},
		{"task.create spec.intent", task, "spec.intent"},
		{"task.create spec.subject", task, "spec.subject"},
		{"task.create spec.non_goals[0]", task, "spec.non_goals.[0]"},
		{"task.create spec.acceptance_criteria[0].criterion", task, "spec.acceptance_criteria.[0].criterion"},
		{"task.create spec.scope.applies_when", task, "spec.scope.applies_when"},
		{"task.create spec.scope.limitations", task, "spec.scope.limitations"},
		{"claim.assert spec.assertion", claim, "spec.assertion"},
		{"claim.assert spec.falsifier", claim, "spec.falsifier"},
		{"decision.open spec.question", decision, "spec.question"},
		{"decision.open spec.options[0]", decision, "spec.options.[0]"},
	}
	for _, f := range fields {
		recMustAccept(t, f.name+" unmodified control", f.event)
		for _, blank := range recBlanks {
			t.Run(f.name+"/"+blank.name, func(t *testing.T) {
				recMustRefuse(t, fmt.Sprintf("%s set to %s (%q)", f.name, blank.name, blank.text),
					recSet(t, f.event, f.path, blank.text))
			})
		}
	}
}

func TestSchemaAvailabilityIsEitherAKnownValueOrAStatedUnknownReason(t *testing.T) {
	good := recEncode(t, &model.InstrumentDeclare{Provenance: recProvenance(), ID: recID(4), Spec: recInstrumentSpec()})
	unknownSpec := recInstrumentSpec()
	unknownSpec.Validation = recUnknown[model.InstrumentValidation]("this instrument has never been validated")
	honestUnknown := recEncode(t, &model.InstrumentDeclare{Provenance: recProvenance(), ID: recID(4), Spec: unknownSpec})

	recMustAccept(t, "validation known with a value", good)
	recMustAccept(t, "validation unknown with a stated reason", honestUnknown)

	validation := map[string]any{
		"ref":     map[string]any{"kind": "git", "git": map[string]any{"object_format": "sha1", "commit": strings.Repeat("a", 40), "path": "x.go"}, "selector": map[string]any{"kind": "whole"}},
		"version": "u02-1",
	}
	cases := []struct {
		name  string
		value any
	}{
		{"known with no value", map[string]any{"state": "known"}},
		{"known carrying a reason as well as a value", map[string]any{"state": "known", "value": validation, "reason": "also unknown"}},
		{"known carrying only a reason", map[string]any{"state": "known", "reason": "never validated"}},
		{"unknown carrying a value", map[string]any{"state": "unknown", "value": validation, "reason": "never validated"}},
		{"unknown with no reason", map[string]any{"state": "unknown"}},
		{"unknown with an empty reason", map[string]any{"state": "unknown", "reason": ""}},
		{"unknown with a space for a reason", map[string]any{"state": "unknown", "reason": " "}},
		{"unknown with a zero width space for a reason", map[string]any{"state": "unknown", "reason": string(rune(0x200b))}},
		{"unknown with a right-to-left mark for a reason", map[string]any{"state": "unknown", "reason": string(rune(0x200f))}},
		{"state spelled in capitals", map[string]any{"state": "KNOWN", "value": validation}},
		{"state outside the union", map[string]any{"state": "maybe", "value": validation}},
		{"state empty", map[string]any{"state": "", "value": validation}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			recMustRefuse(t, "instrument.declare spec.validation "+c.name, recSet(t, good, "spec.validation", c.value))
		})
	}
	t.Run("envelope outcome known with no value", func(t *testing.T) {
		seal := recEncode(t, recSeal())
		recMustRefuse(t, "invocation.seal envelope.outcome known with no value",
			recSet(t, seal, "envelope.outcome", map[string]any{"state": "known"}))
	})
}

func TestSchemaAmendmentIsAFullReplacementCarryingItsTargetRevision(t *testing.T) {
	amend := recEncode(t, &model.TaskAmend{
		Provenance:  recProvenance(),
		Target:      recRef(recID(1), 2),
		Replacement: recTaskSpec(),
	})
	recMustAccept(t, "a full task.amend replacement of its target revision", amend)

	t.Run("replacement missing one authored field is a patch", func(t *testing.T) {
		recMustRefuse(t, "task.amend replacement without spec.intent", recDrop(t, amend, "replacement.intent"))
	})
	t.Run("replacement missing its non-goals is a patch", func(t *testing.T) {
		recMustRefuse(t, "task.amend replacement without non_goals", recDrop(t, amend, "replacement.non_goals"))
	})
	t.Run("empty replacement object is a patch", func(t *testing.T) {
		recMustRefuse(t, "task.amend with an empty replacement", recSet(t, amend, "replacement", map[string]any{}))
	})
	// The target names the revision replaced; a second copy of that number
	// (expected_revision, which could disagree with it) is not a field.
	t.Run("a separate expected revision is not a field", func(t *testing.T) {
		recMustRefuse(t, "task.amend carrying expected_revision", recSet(t, amend, "expected_revision", json.Number("2")))
	})
	t.Run("target revision zero", func(t *testing.T) {
		recMustRefuse(t, "task.amend targeting revision 0", recSet(t, amend, "target.revision", json.Number("0")))
	})
	t.Run("target revision absent", func(t *testing.T) {
		recMustRefuse(t, "task.amend whose target carries no revision", recDrop(t, amend, "target.revision"))
	})

	partials := []struct {
		name  string
		event model.Event
		path  string
	}{
		{"claim.revise", recEncode(t, &model.ClaimRevise{Provenance: recProvenance(), Target: recRef(recID(2), 2), Replacement: recClaimSpec()}), "replacement.falsifier"},
		{"decision.revise", recEncode(t, &model.DecisionRevise{Provenance: recProvenance(), Target: recRef(recID(3), 2), Replacement: recDecisionSpec()}), "replacement.options"},
		{"instrument.revise", recEncode(t, &model.InstrumentRevise{Provenance: recProvenance(), Target: recRef(recID(4), 2), Replacement: recInstrumentSpec()}), "replacement.blind_to"},
	}
	for _, p := range partials {
		t.Run(p.name+" replacement missing "+p.path, func(t *testing.T) {
			recMustAccept(t, p.name+" full replacement control", p.event)
			recMustRefuse(t, p.name+" replacement without "+p.path, recDrop(t, p.event, p.path))
			recMustRefuse(t, p.name+" without target.revision", recDrop(t, p.event, "target.revision"))
		})
	}
}

func TestSchemaEveryReferenceThatNeedsAnExactRevisionCarriesOne(t *testing.T) {
	slots := []struct {
		name  string
		event model.Event
		path  string
	}{
		{"task.amend target", recEncode(t, &model.TaskAmend{Provenance: recProvenance(), Target: recRef(recID(1), 2), Replacement: recTaskSpec()}), "target"},
		{"task.start task", recEncode(t, recStart()), "task"},
		{"criterion.fix claim", recEncode(t, recCriterionFix()), "claim"},
		{"proof.admit claim", recEncode(t, recProof()), "claim"},
		{"proof.admit criterion_ref.claim", recEncode(t, recProof()), "criterion_ref.claim"},
		{"supersede prior", recEncode(t, recSupersede()), "prior"},
		{"supersede replacement", recEncode(t, recSupersede()), "replacement"},
		{"trust.withdraw instrument", recEncode(t, recTrustWithdraw()), "instrument"},
		{"correction affected_revisions[0]", recEncode(t, recCorrection()), "affected_revisions.[0]"},
		{"task.create spec.prerequisites[0].target", recEncode(t, &model.TaskCreate{Provenance: recProvenance(), ID: recID(1), Spec: recTaskSpec()}), "spec.prerequisites.[0].target"},
	}
	for _, s := range slots {
		t.Run(s.name, func(t *testing.T) {
			recMustAccept(t, s.name+" control", s.event)
			recMustRefuse(t, s.name+" at revision 0", recSet(t, s.event, s.path+".revision", json.Number("0")))
			recMustRefuse(t, s.name+" with no revision at all", recDrop(t, s.event, s.path+".revision"))
		})
	}
}

func TestSchemaNumericTargetsCompareAsExactDecimalsNotFloat64(t *testing.T) {
	number := func(text string) model.Scalar { return recNumber(text) }

	// Control: ordinary comparisons work, and a numeric comparison is not a
	// string comparison in disguise.
	ok, err := model.CompareScalars(number("2"), model.Less, number("10"))
	if err != nil || !ok {
		t.Fatalf("control: 2 lt 10 must hold, got %v, %v", ok, err)
	}
	ok, err = model.CompareScalars(number("1.10"), model.Equal, number("1.1"))
	if err != nil || !ok {
		t.Fatalf("control: 1.10 and 1.1 are the same number, got %v, %v", ok, err)
	}

	cases := []struct {
		name  string
		left  string
		op    model.ComparisonOperator
		right string
		want  bool
	}{
		// float64 rounds both of these to the same value, so an implementation
		// that parses through float64 answers eq=true here.
		{"two to the fifty-three plus one is not two to the fifty-three", "9007199254740993", model.Equal, "9007199254740992", false},
		{"two to the fifty-three plus one is greater", "9007199254740993", model.Greater, "9007199254740992", true},
		{"two to the fifty-three plus three is not plus two", "9007199254740995", model.Equal, "9007199254740994", false},
		// The exact decimal expansion of the float64 nearest to 0.1.
		{"nearest float to a tenth is not a tenth", "0.1000000000000000055511151231257827021181583404541015625", model.Equal, "0.1", false},
		// What 0.1 plus 0.2 actually produces in float64.
		{"the float64 sum of a tenth and a fifth is not three tenths", "0.30000000000000004", model.Equal, "0.3", false},
		{"the float64 sum of a tenth and a fifth exceeds three tenths", "0.30000000000000004", model.Greater, "0.3", true},
		// Trailing zeros are spelling, not value.
		{"one point one zero equals one point one", "1.10", model.Equal, "1.1", true},
		{"one point one zero is not unequal to one point one", "1.10", model.NotEqual, "1.1", false},
		{"one point one zero is not less than one point one", "1.10", model.Less, "1.1", false},
		{"one point ten thousandths equals its shorter spelling", "0.10000", model.Equal, "0.1", true},
		{"exponent spelling equals plain spelling", "1e2", model.Equal, "100", true},
		{"negative zero equals zero", "-0", model.Equal, "0", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := model.CompareScalars(number(c.left), c.op, number(c.right))
			if err != nil {
				t.Fatalf("%s %s %s returned an error: %v", c.left, c.op, c.right, err)
			}
			if got != c.want {
				t.Errorf("%s %s %s = %v, want %v. a float64 comparison gives the wrong answer here",
					c.left, c.op, c.right, got, c.want)
			}
		})
	}

	t.Run("a numeric token keeps its exact spelling through a criterion payload", func(t *testing.T) {
		fix := recCriterionFix()
		fix.Expression.Target = number("1.10")
		e := recEncode(t, fix)
		if !bytes.Contains(e.Data, []byte(`"number":"1.10"`)) && !bytes.Contains(e.Data, []byte(`"number":1.10`)) {
			t.Errorf("criterion target 1.10 was reformatted on the wire: %s", e.Data)
		}
		decoded, err := model.DecodeEvent(e)
		if err != nil {
			t.Fatalf("criterion payload refused: %v", err)
		}
		got := decoded.(*model.CriterionFix).Expression.Target
		if got.Number == nil || got.Number.String() != "1.10" {
			t.Errorf("criterion target decoded as %v, want the exact token 1.10", got.Number)
		}
	})

	t.Run("number spellings outside JSON decimal text are refused", func(t *testing.T) {
		for _, bad := range []string{"0x10", "1/2", ".5", "1.", "+1", "01", "Infinity", "NaN", "1_000", ""} {
			if _, err := model.CompareScalars(number(bad), model.Equal, number("1")); err == nil {
				t.Errorf("the token %q was accepted as a numeric target", bad)
			}
		}
	})

	t.Run("an exponent no rational can hold is refused rather than expanded", func(t *testing.T) {
		done := make(chan error, 1)
		go func() {
			_, err := model.CompareScalars(number("1e1000000000"), model.Equal, number("1"))
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil {
				t.Error("1e1000000000 was accepted as a criterion target")
			}
		case <-time.After(20 * time.Second):
			t.Fatal("comparing 1e1000000000 did not return within 20 seconds, so a criterion target can hang the gate")
		}
	})
}

func TestSchemaReservedStatusNameSurvivesInvisiblePadding(t *testing.T) {
	// Control: the reserved name is enforced, both as a declared knob and as a
	// key anywhere in a payload.
	spec := recInstrumentSpec()
	spec.ConfigSurface = []string{"status"}
	if err := model.ValidateSchema(spec); err == nil {
		t.Fatal("control: an instrument declaring a knob named status must be refused")
	}
	good := recEncode(t, &model.TaskCreate{Provenance: recProvenance(), ID: recID(1), Spec: recTaskSpec()})
	recMustRefuse(t, "task.create carrying a top level status", recSet(t, good, "status", "READY"))
	recMustRefuse(t, "task.create carrying a nested status", recSet(t, good, "spec.status", "READY"))

	// Attack: the guard compares the exact string, while every other emptiness
	// rule in this package trims first. A knob named "status " reads as status.
	for _, padded := range []string{"status ", " status", "status" + string(rune(0x00a0)), "status" + string(rune(0x200b))} {
		t.Run(fmt.Sprintf("config knob %q", padded), func(t *testing.T) {
			spec := recInstrumentSpec()
			spec.ConfigSurface = []string{padded}
			envelope := recEnvelope()
			envelope.ConfigRequested = map[string]model.Scalar{padded: recBool(true)}
			if err := model.ValidateSchema(spec); err != nil {
				t.Logf("instrument spec refused the padded knob name: %v", err)
				return
			}
			if err := model.ValidateInvocationConfig(envelope, spec); err == nil {
				t.Errorf("an instrument declared a configuration knob named %q and an invocation set it. "+
					"the reserved name status is bypassed by padding that renders as nothing", padded)
			}
		})
	}
}

// TestSchemaCheckedInFixturesDecideTheSameWayTheyAreNamed drives the five named
// U02 fixture families from files rather than from Go values, so the negatives
// are reviewable data. A file named good must decode. A file named bad must be
// refused, and the family it sits in says which single relationship it breaks.
func TestSchemaCheckedInFixturesDecideTheSameWayTheyAreNamed(t *testing.T) {
	families, err := os.ReadDir(filepath.Join("testdata", "schema"))
	if err != nil {
		t.Fatalf("the U02 fixture families are missing: %v", err)
	}
	if len(families) == 0 {
		t.Fatal("no U02 fixture family is checked in, so nothing here is proven")
	}
	for _, family := range families {
		family := family
		t.Run(family.Name(), func(t *testing.T) {
			dir := filepath.Join("testdata", "schema", family.Name())
			files, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			goods, bads := 0, 0
			for _, file := range files {
				if !strings.HasSuffix(file.Name(), ".json") {
					continue
				}
				name := strings.TrimSuffix(file.Name(), ".json")
				body, err := os.ReadFile(filepath.Join(dir, file.Name()))
				if err != nil {
					t.Fatal(err)
				}
				var fixture struct {
					Type model.EventType `json:"type"`
					Data json.RawMessage `json:"data"`
				}
				if err := json.Unmarshal(body, &fixture); err != nil {
					t.Fatalf("%s is not a type and data fixture: %v", file.Name(), err)
				}
				e := model.Event{Type: fixture.Type, Data: fixture.Data}
				if strings.HasPrefix(name, "good") {
					goods++
					recMustAccept(t, family.Name()+"/"+name, e)
					continue
				}
				if !strings.HasPrefix(name, "bad-") {
					t.Fatalf("%s is named neither good nor bad, so its expectation is undeclared", file.Name())
				}
				bads++
				recMustRefuse(t, family.Name()+"/"+name, e)
			}
			// A validator that refuses everything passes every negative while
			// being completely broken, so each family carries its own control.
			if goods == 0 {
				t.Errorf("family %s has no accepted control, so its refusals prove nothing", family.Name())
			}
			if bads == 0 {
				t.Errorf("family %s has no attack", family.Name())
			}
		})
	}
}
