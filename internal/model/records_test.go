package model

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func goodSchemaValue(t *testing.T, v any) {
	t.Helper()
	if err := ValidateSchema(v); err != nil {
		t.Fatalf("good control: %v", err)
	}
}
func badSchemaValue(t *testing.T, v any) {
	t.Helper()
	if err := ValidateSchema(v); err == nil {
		t.Fatalf("accepted malformed %T: %+v", v, v)
	}
}

func TestAvailabilityHonestUnknownAndObservedZero(t *testing.T) {
	goodSchemaValue(t, schemaKnown(false))
	goodSchemaValue(t, schemaKnown(json.Number("0")))
	goodSchemaValue(t, schemaKnown([]ArtifactRef{}))
	goodSchemaValue(t, schemaUnknown[bool]())
	for name, v := range map[string]Availability[bool]{
		"missing state": {}, "unknown state": {State: "assumed", Reason: "defaults"},
		"known missing value": {State: Known}, "known reason": {State: Known, Value: ptr(false), Reason: "assumed"},
		"unknown missing reason": {State: Unknown}, "unknown blank reason": {State: Unknown, Reason: " \t\n"},
		"unknown with value": {State: Unknown, Value: ptr(false), Reason: "not observed"},
	} {
		t.Run(name, func(t *testing.T) { badSchemaValue(t, v) })
	}
	raw := requireSchemaGood(t, &InvocationStart{Envelope: schemaEnvelope()})
	for _, at := range []string{"envelope.criterion_ref", "envelope.execution_source_identity.machine_id", "envelope.execution_source_identity.head", "envelope.execution_source_identity.dirty", "envelope.config_effective", "envelope.conditions_observed", "envelope.isolation", "envelope.observed_at", "envelope.outcome", "envelope.output_refs", "envelope.visual"} {
		requireSchemaRefusal(t, mutateSchema(t, raw, at, map[string]any{"state": "unknown", "reason": " "}, false), "invalid-field")
		requireSchemaRefusal(t, mutateSchema(t, raw, at, nil, false), "invalid-field")
	}
	e := schemaEnvelope()
	e.CriterionRef = schemaUnknown[CriterionRef]()
	requireSchemaGood(t, &InvocationStart{Envelope: e})
}

func TestRequiredSemanticWhitespace(t *testing.T) {
	fixtures := schemaEvents()
	cases := []struct {
		event int
		paths []string
	}{
		{0, []string{"spec.intent", "spec.subject", "spec.scope.applies_when", "spec.scope.limitations", "spec.progress.summary", "spec.progress.next_action"}},
		{4, []string{"reason", "next_action"}},
		{6, []string{"criterion"}},
		{11, []string{"spec.assertion", "spec.falsifier"}},
		{13, []string{"expression.unit", "expression.population.identity", "expression.population.denominator"}},
		{14, []string{"judgment.reason"}},
		{15, []string{"spec.question"}},
		{17, []string{"quote", "scope.applies_when", "authority.scope.limitations"}},
		{18, []string{"reason"}}, {19, []string{"reason"}},
		{20, []string{"spec.question_answered", "spec.blind_to", "spec.not_answered", "spec.valid_range"}},
		{22, []string{"revalidation_condition"}}, {23, []string{"reason"}},
	}
	for _, c := range cases {
		for _, at := range c.paths {
			t.Run(string(fixtures[c.event].EventType())+"/"+at, func(t *testing.T) {
				raw := requireSchemaGood(t, fixtures[c.event])
				for _, space := range []string{"", " ", "\t\r\n", "\u00a0\u2003"} {
					requireSchemaRefusal(t, mutateSchema(t, raw, at, space, false), "invalid-field")
				}
			})
		}
	}
	task := schemaTask()
	goodSchemaValue(t, task)
	task.NonGoals = []string{" "}
	badSchemaValue(t, task)
	task = schemaTask()
	task.AcceptanceCriteria[0].Criterion = " "
	badSchemaValue(t, task)
	decision := schemaDecision()
	goodSchemaValue(t, decision)
	decision.Options = []string{" "}
	badSchemaValue(t, decision)
	instrument := schemaInstrument()
	goodSchemaValue(t, instrument)
	instrument.DangerousDefaults = []string{" "}
	badSchemaValue(t, instrument)
}

func TestClosedUnionsAndExactReferences(t *testing.T) {
	e := &InstrumentDeclare{ID: schemaID(1), Spec: schemaInstrument(), Provenance: schemaProvenance()}
	raw := requireSchemaGood(t, e)
	for name, val := range map[string]any{
		"known missing value":      map[string]any{"state": "known"},
		"unknown with value":       map[string]any{"state": "unknown", "reason": "unvalidated", "value": InstrumentValidation{Ref: schemaArtifact(), Version: "v1"}},
		"validation version blank": schemaKnown(InstrumentValidation{Ref: schemaArtifact(), Version: " "}),
	} {
		t.Run(name, func(t *testing.T) {
			requireSchemaRefusal(t, mutateSchema(t, raw, "spec.validation", val, false), "invalid-field")
		})
	}
	for _, actor := range []Actor{{}, {ID: " "}, {UnknownReason: "\n"}, {ID: "lane", UnknownReason: "also unknown"}} {
		r := requireSchemaGood(t, schemaEvents()[2])
		requireSchemaRefusal(t, mutateSchema(t, r, "actor", actor, false), "invalid-field")
	}
	r := requireSchemaGood(t, schemaEvents()[2])
	requireSchemaRefusal(t, mutateSchema(t, r, "task.revision", nil, true), "invalid-field")
	requireSchemaRefusal(t, mutateSchema(t, r, "task.project", " ", false), "invalid-field")
	authority := schemaAuthority()
	goodSchemaValue(t, authority)
	authority.SourceRef.Selector = Selector{Kind: "json-pointer", Pointer: "/another/meaning"}
	badSchemaValue(t, authority)
	claim := schemaClaim()
	goodSchemaValue(t, claim)
	claim.ExternalRefs[0].Tag = "TRUSTED"
	badSchemaValue(t, claim)
	criterion := schemaExpression()
	goodSchemaValue(t, criterion)
	criterion.Reducer = "average"
	badSchemaValue(t, criterion)
	correction := schemaEvents()[19].(*Correction)
	requireSchemaGood(t, correction)
	correction.Target.Record = ptr(schemaRef(1))
	if _, err := EncodeEvent(correction); err == nil {
		t.Fatal("accepted two correction branches")
	}
	correction.Target = CorrectionTarget{Kind: "record", Record: ptr(schemaRef(1))}
	requireSchemaGood(t, correction)
	correction.Target = CorrectionTarget{Kind: "criterion", Criterion: ptr(schemaCriterionRef())}
	requireSchemaGood(t, correction)
}

func TestExactDecimalComparisons(t *testing.T) {
	ok, err := CompareScalars(schemaNumber("9007199254740993"), Greater, schemaNumber("9007199254740992"))
	if err != nil || !ok {
		t.Fatalf("good control lost integer precision: %v %v", ok, err)
	}
	cases := []struct {
		a    string
		op   ComparisonOperator
		b    string
		want bool
	}{
		{"0.1000000000000000000000000001", Greater, "0.1", true},
		{"1e-3", Equal, "0.001", true}, {"-0", Equal, "0.0", true},
		{"1", NotEqual, "2", true}, {"-3", Less, "-2", true}, {"1.00", LessEqual, "1", true}, {"2", GreaterEqual, "2e0", true},
		{"2", Less, "1", false},
	}
	for _, c := range cases {
		got, err := CompareScalars(schemaNumber(c.a), c.op, schemaNumber(c.b))
		if err != nil || got != c.want {
			t.Errorf("%s %s %s: got %v %v", c.a, c.op, c.b, got, err)
		}
	}
	for _, s := range []string{"1/3", "NaN", "Inf", "0x10", "+1", "01", "1.", ".1", " 1", "1e"} {
		if _, err := DecimalRat(json.Number(s)); err == nil {
			t.Errorf("accepted non-JSON decimal %q", s)
		}
	}
	for _, c := range []struct {
		a  Scalar
		op ComparisonOperator
		b  Scalar
	}{
		{schemaNumber("1"), Equal, Scalar{Type: "string", String: ptr("1")}},
		{Scalar{Type: "bool", Bool: ptr(false)}, Less, Scalar{Type: "bool", Bool: ptr(true)}},
		{schemaNumber("1"), "approx", schemaNumber("1")},
		{Scalar{Type: "number", Number: ptr(json.Number("1")), Bool: ptr(false)}, Equal, schemaNumber("1")},
	} {
		if _, err := CompareScalars(c.a, c.op, c.b); err == nil {
			t.Fatal("accepted invalid predicate")
		}
	}
	expr := schemaExpression()
	goodSchemaValue(t, expr)
	expr.Reducer = Count
	expr.Target = Scalar{Type: "string", String: ptr("3")}
	badSchemaValue(t, expr)
	raw := requireSchemaGood(t, schemaEvents()[13])
	requireSchemaRefusal(t, mutateSchema(t, raw, "expression.target.number", "1.5", false), "invalid-field")
}

func TestEffectiveConfigurationAndProcessObservation(t *testing.T) {
	e := schemaEvents()[9].(*InvocationSeal).Envelope
	s := schemaInstrument()
	goodSchemaValue(t, e)
	if err := ValidateInvocationConfig(e, s); err != nil {
		t.Fatalf("good control: %v", err)
	}
	e.ConfigRequested["typo"] = schemaNumber("1")
	if err := ValidateInvocationConfig(e, s); err == nil {
		t.Fatal("accepted undeclared requested knob")
	}
	delete(e.ConfigRequested, "typo")
	(*e.ConfigEffective.Value)["typo"] = schemaKnown(schemaNumber("1"))
	if err := ValidateInvocationConfig(e, s); err == nil {
		t.Fatal("accepted undeclared effective knob")
	}
	delete(*e.ConfigEffective.Value, "typo")
	delete(*e.ConfigEffective.Value, "sample_count")
	if err := ValidateInvocationConfig(e, s); err == nil {
		t.Fatal("missing effective knob became an implicit default")
	}
	(*e.ConfigEffective.Value)["sample_count"] = schemaUnknown[Scalar]()
	if err := ValidateInvocationConfig(e, s); err != nil {
		t.Fatalf("explicitly unavailable knob: %v", err)
	}
	s.ConfigSurface = []string{"sample_count", "sample_count"}
	badSchemaValue(t, s)
	for _, o := range []ProcessOutcome{{Kind: "exit", ExitCode: ptr(0)}, {Kind: "signal", Signal: ptr("SIGTERM")}, {Kind: "spawn-failed", Diagnostic: ptr("executable missing")}} {
		goodSchemaValue(t, schemaKnown(o))
	}
	for _, o := range []ProcessOutcome{{Kind: "exit"}, {Kind: "exit", ExitCode: ptr(0), Signal: ptr("SIGTERM")}, {Kind: "signal", Signal: ptr(" ")}, {Kind: "spawn-failed", Diagnostic: ptr(" ")}, {Kind: "runner-died"}} {
		badSchemaValue(t, schemaKnown(o))
	}
	goodSchemaValue(t, schemaUnknown[ProcessOutcome]())
	goodSchemaValue(t, schemaKnown(IsolationClean))
	badSchemaValue(t, schemaKnown(Isolation("unknown")))
	e = schemaEnvelope()
	e.ExecutionSourceIdentity.Dirty = schemaKnown(true)
	goodSchemaValue(t, e)
	e.ExecutionSourceIdentity.SourceRefs = []ArtifactRef{}
	badSchemaValue(t, e)
	e = schemaEnvelope()
	e.Argv = []string{" "}
	badSchemaValue(t, e)
	e = schemaEnvelope()
	e.Argv = []string{"executable", "", "argument with spaces"}
	goodSchemaValue(t, e)
	e.ObservedAt = schemaKnown(e.StartedAt.Add(-time.Second))
	badSchemaValue(t, e)
}

func TestVisualMetadataStaysUnavailableUntilObserved(t *testing.T) {
	v := VisualObservation{Framing: schemaUnknown[VisualFraming](), Transforms: schemaUnknown[ArtifactRef](), Clip: schemaUnknown[string](), Time: schemaUnknown[json.Number](), Seed: schemaUnknown[string](), Backend: schemaUnknown[string](), Appearance: schemaUnknown[VisualAppearance](), Limits: schemaUnknown[VisualLimits]()}
	goodSchemaValue(t, v)
	e := schemaEnvelope()
	e.Visual = schemaKnown(v)
	requireSchemaGood(t, &InvocationStart{Envelope: e})
	v.Time = schemaKnown(json.Number("0"))
	v.Backend = schemaKnown("webgpu")
	v.Framing = schemaKnown(schemaVisualFraming())
	goodSchemaValue(t, v)
	v.Transforms = Availability[ArtifactRef]{State: Known}
	badSchemaValue(t, v)
}

func TestPinnedArtifactSemantics(t *testing.T) {
	good := schemaArtifact()
	goodSchemaValue(t, good)
	git := ArtifactRef{Kind: "git", Git: &GitPin{ObjectFormat: "sha1", Commit: strings.Repeat("a", 40), Path: "historical/file.go"}, Selector: Selector{Kind: "json-pointer", Pointer: "/a~1b/~0"}}
	goodSchemaValue(t, git)
	cases := []ArtifactRef{}
	bad := git
	bad.Git = &GitPin{ObjectFormat: "sha1", Commit: strings.Repeat("z", 40), Path: "historical/file.go"}
	cases = append(cases, bad)
	for _, p := range []string{" ", "/absolute", "../escape", "C:\\path"} {
		bad := git
		pin := *git.Git
		pin.Path = p
		bad.Git = &pin
		cases = append(cases, bad)
	}
	for _, p := range []string{"not/a/pointer", "/bad~2", "/bad~"} {
		bad := git
		bad.Selector.Pointer = p
		cases = append(cases, bad)
	}
	for _, bad := range cases {
		badSchemaValue(t, bad)
	}
}

func TestReferenceWalkerMatchesTypedReferences(t *testing.T) {
	// The test oracle uses Go types, independently of the production visitor's
	// handwritten field list; it catches a new nested reference omitted from that list.
	for _, e := range schemaEvents() {
		t.Run(string(e.EventType()), func(t *testing.T) {
			requireSchemaGood(t, e)
			want := map[string]any{}
			var collect func(reflect.Value, string)
			collect = func(v reflect.Value, p string) {
				if v.Kind() == reflect.Pointer {
					if v.IsNil() {
						return
					}
					collect(v.Elem(), p)
					return
				}
				switch v.Interface().(type) {
				case RecordRef, CriterionRef, InvocationRef, BlockerRef:
					want[p] = v.Interface()
					return
				case time.Time:
					return
				}
				switch v.Kind() {
				case reflect.Struct:
					for i := 0; i < v.NumField(); i++ {
						f := v.Type().Field(i)
						name := strings.Split(f.Tag.Get("json"), ",")[0]
						if name == "" {
							continue
						}
						next := name
						if p != "" {
							next = p + "." + name
						}
						collect(v.Field(i), next)
					}
				case reflect.Slice:
					for i := 0; i < v.Len(); i++ {
						collect(v.Index(i), fmtPath(p, i))
					}
				}
			}
			collect(reflect.ValueOf(e), "")
			refs, err := EventReferences(e)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]any{}
			for _, r := range refs {
				switch {
				case r.Record != nil:
					got[r.Path] = *r.Record
				case r.Criterion != nil:
					got[r.Path] = *r.Criterion
				case r.Invocation != nil:
					got[r.Path] = *r.Invocation
				case r.Blocker != nil:
					got[r.Path] = *r.Blocker
				}
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("walker omitted or changed typed references\nwant %v\ngot %v", want, got)
			}
		})
	}
}
func fmtPath(p string, i int) string {
	return p + "[" + strconv.Itoa(i) + "]"
}

func schemaVisualFraming() VisualFraming {
	return VisualFraming{Subject: schemaKnown("fixture"), ProjectedBounds: schemaKnown(schemaArtifact()), CameraPosition: schemaKnown(Vector3{X: "0", Y: "1", Z: "2"}), CameraTarget: schemaUnknown[Vector3](), Projection: schemaKnown("perspective"), Viewport: schemaKnown(Viewport{Width: 800, Height: 600}), DPR: schemaKnown(json.Number("1"))}
}
func TestVisualFramingAndAppearanceRequiredFacts(t *testing.T) {
	f := schemaVisualFraming()
	goodSchemaValue(t, f)
	f.DPR = schemaKnown(json.Number("0"))
	badSchemaValue(t, f)
	f = schemaVisualFraming()
	f.Subject = schemaKnown(" ")
	badSchemaValue(t, f)
	f = schemaVisualFraming()
	f.CameraPosition = schemaKnown(Vector3{X: "0", Y: "1"})
	badSchemaValue(t, f)
	a := VisualAppearance{Lights: schemaUnknown[ArtifactRef](), Exposure: schemaUnknown[json.Number](), ColourSpace: schemaUnknown[string](), Materials: schemaUnknown[ArtifactRef](), DiagnosticOverrides: schemaUnknown[ArtifactRef]()}
	goodSchemaValue(t, a)
	a.ColourSpace = schemaKnown(" ")
	badSchemaValue(t, a)
	l := VisualLimits{Occlusion: schemaUnknown[string](), UnviewedSurfaces: schemaUnknown[[]string](), UntestedBackends: schemaKnown([]string{"webgl"}), StillLimitations: "a still cannot show temporal behavior"}
	goodSchemaValue(t, l)
	l.UntestedBackends = schemaKnown([]string{" "})
	badSchemaValue(t, l)
}

func TestKnownBranchesAndPartialAuthoredProgress(t *testing.T) {
	instrument := schemaInstrument()
	instrument.Validation = schemaKnown(InstrumentValidation{Ref: schemaArtifact(), Version: "validated-implementation-v1"})
	goodSchemaValue(t, instrument)
	for _, progress := range []TaskProgress{{Summary: "fixtures authored", WitnessRefs: []ArtifactRef{}}, {NextAction: "run fixtures", WitnessRefs: []ArtifactRef{}}} {
		s := schemaTask()
		s.Progress = &progress
		goodSchemaValue(t, s)
	}
	for _, scalar := range []Scalar{{Type: "string", String: ptr("")}, {Type: "bool", Bool: ptr(false)}, schemaNumber("0")} {
		goodSchemaValue(t, scalar)
		if ok, err := CompareScalars(scalar, Equal, scalar); err != nil || !ok {
			t.Fatalf("valid equality: %v %v", ok, err)
		}
		e := schemaEvents()[13].(*CriterionFix)
		e.Expression.Target = scalar
		requireSchemaGood(t, e)
	}
	// A Go json.Number zero value marshals as 0; validate it before encoding so it
	// cannot masquerade as an observation of zero.
	e := schemaEvents()[13].(*CriterionFix)
	requireSchemaGood(t, e)
	e.Expression.Target = schemaNumber("")
	if _, err := EncodeEvent(e); err == nil {
		t.Fatal("empty number became an observed zero")
	}
	e = schemaEvents()[13].(*CriterionFix)
	e.Policy.Retry = "latest-passing"
	if _, err := EncodeEvent(e); err == nil {
		t.Fatal("retry policy dropped inconvenient observations")
	}
}
