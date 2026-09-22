package model

import (
	"encoding/json"
	"fmt"
	"math/big"
	"path"
	"reflect"
	"regexp"
	"strings"
	"time"
)

// Availability keeps absence separate from observed zero, false, or an empty set.
// U07 evaluates it, U10 captures it, and U12 refuses inferences it cannot support.
type Availability[T any] struct {
	State  AvailabilityState `json:"state"`
	Value  *T                `json:"value,omitempty"`
	Reason string            `json:"reason,omitempty"`
}

type AvailabilityState string

const (
	Known   AvailabilityState = "known"
	Unknown AvailabilityState = "unknown"
)

func (a Availability[T]) validate(p string) error {
	switch a.State {
	case Known:
		if a.Value == nil || a.Reason != "" {
			return invalid(p, "known requires a value and no reason")
		}
	case Unknown:
		if a.Value != nil || strings.TrimSpace(a.Reason) == "" {
			return invalid(p, "unknown requires a reason and no value")
		}
	default:
		return invalid(p+".state", "expected known or unknown")
	}
	return nil
}

// Scope is consumed by U07/U12 for applicability and U13 for traversal. Source
// paths only select candidates; the authored applicability and limits do the work.
type Scope struct {
	SourcePaths []string    `json:"source_paths"`
	ContextRefs []RecordRef `json:"context_refs"`
	AppliesWhen string      `json:"applies_when" semantic:"text"`
	Limitations string      `json:"limitations" semantic:"text"`
}

func (s Scope) validate(p string) error { return relativePaths(s.SourcePaths, p+".source_paths") }

// Authority lets U12 resolve an actor's exact words within their actual scope.
// SourceRef pins the complete source; Selector selects the ruling within it.
type Authority struct {
	Actor     Actor       `json:"actor"`
	SourceRef ArtifactRef `json:"source_ref"`
	Selector  Selector    `json:"selector"`
	Scope     Scope       `json:"scope"`
}

func (a Authority) validate(p string) error {
	if a.SourceRef.Selector.Kind != "whole" {
		return invalid(p+".source_ref.selector", "authority pins the whole source before selecting the ruling")
	}
	return nil
}

// TaskSpec feeds U05 prerequisites/revisions, U11 acceptance and continuation,
// and U13 context/constraint reads. Authorship comes from the enclosing packet;
// reducers must retain that packet's author and provenance for every revision.
type TaskSpec struct {
	Intent             string                `json:"intent" semantic:"text"`
	Subject            string                `json:"subject" semantic:"text"`
	Scope              Scope                 `json:"scope"`
	NonGoals           []string              `json:"non_goals" semantic:"texts"`
	AcceptanceCriteria []AcceptanceCriterion `json:"acceptance_criteria"`
	ContextRefs        []RecordRef           `json:"context_refs"`
	ConstraintRefs     []RecordRef           `json:"constraint_refs"`
	Prerequisites      []Prerequisite        `json:"prerequisites"`
	NextActor          Actor                 `json:"next_actor"`
	Progress           *TaskProgress         `json:"progress,omitempty"`
}

// AcceptanceCriterion belongs to the task, independently of CLAIM proof criteria.
type AcceptanceCriterion struct {
	ID        ID       `json:"id"`
	Revision  Revision `json:"revision"`
	Criterion string   `json:"criterion" semantic:"text"`
}

// TaskProgress gives U11/U13 witnessed progress and a concrete continuation.
type TaskProgress struct {
	Summary     string        `json:"summary" semantic:"text"`
	NextAction  string        `json:"next_action" semantic:"text"`
	WitnessRefs []ArtifactRef `json:"witness_refs"`
}

// Prerequisite gives U05 a typed predicate at an exact revision. A consumer that
// accepts a waiver must declare it here and supply the authority for doing so.
type Prerequisite struct {
	Kind         string     `json:"kind"`
	Target       RecordRef  `json:"target"`
	WaiverPolicy string     `json:"waiver_policy"`
	Authority    *Authority `json:"authority,omitempty"`
}

func (s TaskSpec) validate(p string) error {
	if len(s.NonGoals) == 0 || len(s.AcceptanceCriteria) == 0 {
		return invalid(p, "non-goals and acceptance criteria must be explicit")
	}
	seen := map[ID]bool{}
	for i, c := range s.AcceptanceCriteria {
		if seen[c.ID] {
			return invalid(fmt.Sprintf("%s.acceptance_criteria[%d].id", p, i), "duplicate acceptance criterion")
		}
		seen[c.ID] = true
	}
	return nil
}
func (r Prerequisite) validate(p string) error {
	if err := oneOf(r.Kind, p+".kind", "task-success", "claim-proof", "decision-approved"); err != nil {
		return err
	}
	switch r.WaiverPolicy {
	case "forbid":
		if r.Authority != nil {
			return invalid(p+".authority", "forbid cannot carry waiver authority")
		}
	case "allow-with-authority":
		if r.Authority == nil {
			return invalid(p+".authority", "waiver policy requires authority")
		}
	default:
		return invalid(p+".waiver_policy", "unknown waiver policy")
	}
	return nil
}

// ClaimSpec feeds U06 assertion history and U12 proof applicability. External
// references are context for U13; their tags cannot stand in for local observations.
type ClaimSpec struct {
	Assertion    string              `json:"assertion" semantic:"text"`
	Falsifier    string              `json:"falsifier" semantic:"text"`
	Scope        Scope               `json:"scope"`
	ExternalRefs []ExternalReference `json:"external_refs"`
}
type ExternalReference struct {
	Tag       string       `json:"tag"`
	Citation  string       `json:"citation" semantic:"text"`
	SourceRef *ArtifactRef `json:"source_ref,omitempty"`
	RecordRef *RecordRef   `json:"record_ref,omitempty"`
}

func (r ExternalReference) validate(p string) error {
	return oneOf(r.Tag, p+".tag", "VERIFIED", "VENDOR CLAIM", "REPORTED MEASUREMENT")
}

// DecisionSpec feeds U06 revision history and U12/U13 authority and waiting views.
// A disposition can only enter through decision.dispose.
type DecisionSpec struct {
	Question     string   `json:"question" semantic:"text"`
	Options      []string `json:"options" semantic:"texts"`
	WaitingActor Actor    `json:"waiting_actor"`
	Scope        Scope    `json:"scope"`
}

func (s DecisionSpec) validate(p string) error {
	if len(s.Options) == 0 {
		return invalid(p+".options", "at least one option is required")
	}
	return nil
}

// InstrumentSpec supplies U07 validity, U10 capture/configuration, U12 trust,
// and U13 instrument discovery. No validation observation is inferred from a declaration.
type InstrumentSpec struct {
	QuestionAnswered  string                             `json:"question_answered" semantic:"text"`
	BlindTo           string                             `json:"blind_to" semantic:"text"`
	NotAnswered       string                             `json:"not_answered" semantic:"text"`
	ConfigSurface     []string                           `json:"config_surface" semantic:"texts"`
	DangerousDefaults []string                           `json:"dangerous_defaults" semantic:"texts"`
	ValidRange        string                             `json:"valid_range" semantic:"text"`
	ImplementationRef ArtifactRef                        `json:"implementation_ref"`
	Validation        Availability[InstrumentValidation] `json:"validation"`
}
type InstrumentValidation struct {
	Ref     ArtifactRef `json:"ref"`
	Version string      `json:"version" semantic:"text"`
}

func (s InstrumentSpec) validate(p string) error {
	seen := map[string]bool{}
	for i, n := range s.ConfigSurface {
		if n == "status" || seen[n] {
			return invalid(fmt.Sprintf("%s.config_surface[%d]", p, i), "reserved or duplicate configuration name")
		}
		seen[n] = true
	}
	return nil
}

// ExecutionIdentity is captured by U10 and compared by U07/U12. MachineID is a
// persisted generated ID, never a guessed hostname. Dirty source needs content pins.
type ExecutionIdentity struct {
	Project    ProjectID             `json:"project"`
	MachineID  Availability[ID]      `json:"machine_id"`
	SourceRefs []ArtifactRef         `json:"source_refs"`
	Head       Availability[GitHead] `json:"head"`
	Dirty      Availability[bool]    `json:"dirty"`
}
type GitHead struct {
	ObjectFormat string `json:"object_format"`
	Commit       string `json:"commit"`
}

func (h GitHead) validate(p string) error { return validateCommit(h.ObjectFormat, h.Commit, p) }
func (s ExecutionIdentity) validate(p string) error {
	if s.Dirty.State == Known && s.Dirty.Value != nil && *s.Dirty.Value {
		found := false
		for _, r := range s.SourceRefs {
			if r.Content != nil {
				found = true
			}
		}
		if !found {
			return invalid(p+".source_refs", "dirty execution requires captured source content")
		}
	}
	return nil
}

// CriterionRef names both the assertion revision and the independent criterion
// revision. U06 retains authorship; U07/U12 compare the entire evaluation family.
type CriterionRef struct {
	Claim       RecordRef `json:"claim"`
	CriterionID ID        `json:"criterion_id"`
	Revision    Revision  `json:"revision"`
}

// Scalar is a closed value union. Numeric tokens retain their decimal text all
// the way from artifact decoding to rational comparison; false and zero are values.
type Scalar struct {
	Type   string       `json:"type"`
	Number *json.Number `json:"number,omitempty"`
	String *string      `json:"string,omitempty"`
	Bool   *bool        `json:"bool,omitempty"`
}

func (s Scalar) validate(p string) error {
	n := 0
	if s.Number != nil {
		n++
	}
	if s.String != nil {
		n++
	}
	if s.Bool != nil {
		n++
	}
	if n != 1 {
		return invalid(p, "scalar requires exactly one value branch")
	}
	switch s.Type {
	case "number":
		if s.Number == nil {
			return invalid(p, "number branch is missing")
		}
		_, err := DecimalRat(*s.Number)
		return err
	case "string":
		if s.String == nil {
			return invalid(p, "string branch is missing")
		}
	case "bool":
		if s.Bool == nil {
			return invalid(p, "bool branch is missing")
		}
	default:
		return invalid(p+".type", "unknown scalar type")
	}
	return nil
}

var decimalNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// DecimalRat accepts JSON decimal number text, never fractions, hex or float64.
func DecimalRat(n json.Number) (*big.Rat, error) {
	if !decimalNumber.MatchString(string(n)) {
		return nil, invalid("number", "expected JSON decimal number text")
	}
	r, ok := new(big.Rat).SetString(string(n))
	if !ok {
		return nil, invalid("number", "decimal exponent is outside the supported range")
	}
	return r, nil
}

// CompareScalars is the exact predicate used by U07 after it resolves selectors.
// Numeric ordering is rational; strings and booleans only support equality.
func CompareScalars(left Scalar, op ComparisonOperator, right Scalar) (bool, error) {
	if err := left.validate("left"); err != nil {
		return false, err
	}
	if err := right.validate("right"); err != nil {
		return false, err
	}
	if err := op.validate("operator"); err != nil {
		return false, err
	}
	if left.Type != right.Type {
		return false, invalid("comparison", "operand types differ")
	}
	var cmp int
	switch left.Type {
	case "number":
		a, err := DecimalRat(*left.Number)
		if err != nil {
			return false, err
		}
		b, err := DecimalRat(*right.Number)
		if err != nil {
			return false, err
		}
		cmp = a.Cmp(b)
	case "string":
		cmp = strings.Compare(*left.String, *right.String)
	case "bool":
		if *left.Bool != *right.Bool {
			cmp = 1
		}
	}
	if left.Type != "number" && op != Equal && op != NotEqual {
		return false, invalid("comparison", "ordering requires numbers")
	}
	switch op {
	case Equal:
		return cmp == 0, nil
	case NotEqual:
		return cmp != 0, nil
	case Less:
		return cmp < 0, nil
	case LessEqual:
		return cmp <= 0, nil
	case Greater:
		return cmp > 0, nil
	case GreaterEqual:
		return cmp >= 0, nil
	}
	return false, invalid("comparison", "unknown operator")
}

type ComparisonOperator string

const (
	Equal        ComparisonOperator = "eq"
	NotEqual     ComparisonOperator = "ne"
	Less         ComparisonOperator = "lt"
	LessEqual    ComparisonOperator = "le"
	Greater      ComparisonOperator = "gt"
	GreaterEqual ComparisonOperator = "ge"
)

func (o ComparisonOperator) validate(p string) error {
	return oneOf(string(o), p, "eq", "ne", "lt", "le", "gt", "ge")
}

type CriterionReducer string

const (
	All   CriterionReducer = "all"
	Any   CriterionReducer = "any"
	Count CriterionReducer = "count"
)

func (r CriterionReducer) validate(p string) error { return oneOf(string(r), p, "all", "any", "count") }

// CriterionExpression is U07's entire executable vocabulary. Count compares
// population cardinality; all/any compare each selected scalar with Target.
// EmptyResult is optional: without an authored meaning, empty is UNKNOWN.
type CriterionExpression struct {
	ResultSelector ArtifactRef        `json:"result_selector"`
	Unit           string             `json:"unit" semantic:"text"`
	Population     Population         `json:"population"`
	Operator       ComparisonOperator `json:"operator"`
	Target         Scalar             `json:"target"`
	Reducer        CriterionReducer   `json:"reducer"`
	EmptyResult    *bool              `json:"empty_result,omitempty"`
}
type Population struct {
	Identity    string      `json:"identity" semantic:"text"`
	Selector    ArtifactRef `json:"selector"`
	Denominator string      `json:"denominator" semantic:"text"`
}

func (c CriterionExpression) validate(p string) error {
	if c.Reducer == Count && c.Target.Type != "number" {
		return invalid(p+".target", "count compares a number")
	}
	if c.Target.Type != "number" && c.Operator != Equal && c.Operator != NotEqual {
		return invalid(p+".operator", "ordering requires a numeric target")
	}
	return nil
}

// EvaluationPolicy freezes family membership and retry handling for U07/U12.
// No option permits dropping an observation because a later attempt passed.
type EvaluationPolicy struct {
	Inclusion string `json:"inclusion"`
	Retry     string `json:"retry"`
}

func (r EvaluationPolicy) validate(p string) error {
	if err := oneOf(r.Inclusion, p+".inclusion", "entire-criterion-family"); err != nil {
		return err
	}
	return oneOf(r.Retry, p+".retry", "retain-all")
}

// InvocationEnvelope is generated by U10, consumed for observation/trust by
// U06/U07/U12, and displayed by U13. Each observed field carries its own availability.
// CriterionRef can honestly be unknown for measurements outside the executable vocabulary.
type InvocationEnvelope struct {
	InvocationID            ID                                            `json:"invocation_id"`
	AttemptID               ID                                            `json:"attempt_id"`
	InstrumentRef           RecordRef                                     `json:"instrument_ref"`
	CriterionRef            Availability[CriterionRef]                    `json:"criterion_ref"`
	ExecutionSourceIdentity ExecutionIdentity                             `json:"execution_source_identity"`
	Argv                    []string                                      `json:"argv"`
	InputRefs               []ArtifactRef                                 `json:"input_refs"`
	ConfigRequested         map[string]Scalar                             `json:"config_requested"`
	ConfigEffective         Availability[map[string]Availability[Scalar]] `json:"config_effective"`
	ConditionsDeclared      map[string]Scalar                             `json:"conditions_declared"`
	ConditionsObserved      Availability[map[string]Availability[Scalar]] `json:"conditions_observed"`
	Isolation               Availability[Isolation]                       `json:"isolation"`
	StartedAt               time.Time                                     `json:"started_at"`
	ObservedAt              Availability[time.Time]                       `json:"observed_at"`
	Outcome                 Availability[ProcessOutcome]                  `json:"outcome"`
	OutputRefs              Availability[[]ArtifactRef]                   `json:"output_refs"`
	Visual                  Availability[VisualObservation]               `json:"visual"`
}

type Isolation string

const IsolationClean Isolation = "clean"

func (i Isolation) validate(p string) error { return oneOf(string(i), p, "clean") }

// ProcessOutcome distinguishes a known exit, a signal and failure to spawn.
// Observer death is Availability unknown, never a fabricated exit code.
type ProcessOutcome struct {
	Kind       string  `json:"kind"`
	ExitCode   *int    `json:"exit_code,omitempty"`
	Signal     *string `json:"signal,omitempty"`
	Diagnostic *string `json:"diagnostic,omitempty"`
}

func (o ProcessOutcome) validate(p string) error {
	switch o.Kind {
	case "exit":
		if o.ExitCode == nil || *o.ExitCode < 0 || o.Signal != nil || o.Diagnostic != nil {
			return invalid(p, "exit requires only a nonnegative exit code")
		}
	case "signal":
		if o.Signal == nil || strings.TrimSpace(*o.Signal) == "" || o.ExitCode != nil || o.Diagnostic != nil {
			return invalid(p, "signal requires only an observed signal")
		}
	case "spawn-failed":
		if o.Diagnostic == nil || strings.TrimSpace(*o.Diagnostic) == "" || o.ExitCode != nil || o.Signal != nil {
			return invalid(p, "spawn failure requires only a diagnostic")
		}
	default:
		return invalid(p+".kind", "unknown process outcome")
	}
	return nil
}

// VisualObservation points at metadata bytes instead of copying transforms or
// frames into a record. U10 pins each adapter selector; U07/U12 resolve actual
// framing (bounds/camera/projection/viewport/DPR), state, appearance and limits.
type VisualObservation struct {
	Framing    Availability[ArtifactRef] `json:"framing"`
	Transforms Availability[ArtifactRef] `json:"transforms"`
	Clip       Availability[string]      `json:"clip"`
	Time       Availability[json.Number] `json:"time"`
	Seed       Availability[string]      `json:"seed"`
	Backend    Availability[string]      `json:"backend"`
	Appearance Availability[ArtifactRef] `json:"appearance"`
	Limits     Availability[ArtifactRef] `json:"limits"`
}

func (e InvocationEnvelope) validate(p string) error {
	if len(e.Argv) == 0 || strings.TrimSpace(e.Argv[0]) == "" {
		return invalid(p+".argv", "an executable argv array is required")
	}
	if e.ObservedAt.State == Known && e.ObservedAt.Value != nil && e.ObservedAt.Value.Before(e.StartedAt) {
		return invalid(p+".observed_at", "observation precedes start")
	}
	return nil
}

// ValidateInvocationConfig is used by U10/U12 once the exact instrument revision
// is available. Shape decoding cannot know another record's declared knob names.
func ValidateInvocationConfig(e InvocationEnvelope, s InstrumentSpec) error {
	if err := ValidateSchema(e); err != nil {
		return err
	}
	if err := ValidateSchema(s); err != nil {
		return err
	}
	names := map[string]bool{}
	for _, n := range s.ConfigSurface {
		names[n] = true
	}
	for n := range e.ConfigRequested {
		if !names[n] {
			return invalid("config_requested."+n, "undeclared configuration name")
		}
	}
	if e.ConfigEffective.State == Known && e.ConfigEffective.Value != nil {
		for n := range *e.ConfigEffective.Value {
			if !names[n] {
				return invalid("config_effective."+n, "undeclared configuration name")
			}
		}
		for n := range names {
			if _, ok := (*e.ConfigEffective.Value)[n]; !ok {
				return invalid("config_effective."+n, "declared knob needs a value or explicit unknown")
			}
		}
	}
	return nil
}

// ValidateSchema checks a programmatically constructed payload at the same
// boundary as DecodeEvent. It does not resolve references or admit facts.
func ValidateSchema(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return invalid("payload", err.Error())
	}
	tree, err := parseOrdered(b)
	if err != nil {
		return err
	}
	if err = checkJSONShape(tree, reflect.TypeOf(v), "payload"); err != nil {
		return err
	}
	return validateValue(reflect.ValueOf(v), "payload")
}

type schemaValidator interface{ validate(string) error }

// Reflection here enforces JSON presence/types, never reference discovery.
// encoding/json alone accepts case aliases, null zero-values and missing fields.
func checkJSONShape(tree any, t reflect.Type, p string) error {
	if t == nil || tree == nil {
		return invalid(p, "null is not a value; use explicit availability")
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeOf(time.Time{}) {
		if _, ok := tree.(string); !ok {
			return invalid(p, "expected time text")
		}
		return nil
	}
	if t == reflect.TypeOf(json.Number("")) {
		if _, ok := tree.(json.Number); !ok {
			return invalid(p, "expected JSON number")
		}
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		fields, ok := tree.([]member)
		if !ok {
			return invalid(p, "expected object")
		}
		known := map[string]reflect.StructField{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			if tag != "" && tag != "-" {
				known[tag] = f
			}
		}
		seen := map[string]bool{}
		for _, m := range fields {
			if m.key == "status" {
				return invalid(p+".status", "status is a projection, never writable")
			}
			f, ok := known[m.key]
			if !ok {
				return invalid(p+"."+m.key, "unknown field")
			}
			seen[m.key] = true
			if err := checkJSONShape(m.value, f.Type, p+"."+m.key); err != nil {
				return err
			}
		}
		for name, f := range known {
			if !seen[name] && !strings.Contains(f.Tag.Get("json"), ",omitempty") {
				return invalid(p+"."+name, "required field is missing")
			}
		}
	case reflect.Map:
		fields, ok := tree.([]member)
		if !ok {
			return invalid(p, "expected object")
		}
		for _, m := range fields {
			if strings.TrimSpace(m.key) == "" || m.key == "status" {
				return invalid(p+"."+m.key, "blank or reserved map key")
			}
			if err := checkJSONShape(m.value, t.Elem(), p+"."+m.key); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		items, ok := tree.([]any)
		if !ok {
			return invalid(p, "expected array")
		}
		for i, v := range items {
			if err := checkJSONShape(v, t.Elem(), fmt.Sprintf("%s[%d]", p, i)); err != nil {
				return err
			}
		}
	case reflect.String:
		if _, ok := tree.(string); !ok {
			return invalid(p, "expected string")
		}
	case reflect.Bool:
		if _, ok := tree.(bool); !ok {
			return invalid(p, "expected boolean")
		}
	case reflect.Int, reflect.Int64, reflect.Uint, reflect.Uint32, reflect.Uint64, reflect.Uint16:
		if _, ok := tree.(json.Number); !ok {
			return invalid(p, "expected number")
		}
	default:
		return invalid(p, "unsupported schema type")
	}
	return nil
}

func validateValue(v reflect.Value, p string) error {
	if !v.IsValid() {
		return invalid(p, "missing value")
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		return validateValue(v.Elem(), p)
	}
	x := v.Interface()
	if s, ok := x.(schemaValidator); ok {
		if err := s.validate(p); err != nil {
			return err
		}
	}
	switch s := x.(type) {
	case ID:
		if !ValidID(s) {
			return invalid(p, "not a ULID")
		}
	case ProjectID:
		if strings.TrimSpace(string(s)) == "" {
			return invalid(p, "empty project")
		}
	case Digest:
		if !ValidDigest(s) {
			return invalid(p, "not lowercase SHA-256")
		}
	case Revision:
		if s == 0 {
			return invalid(p, "revision starts at 1")
		}
	case Actor:
		if err := validActor(s, p); err != nil {
			return err
		}
		if strings.TrimSpace(s.ID) == "" && strings.TrimSpace(s.UnknownReason) == "" {
			return invalid(p, "actor identity or unknown reason is blank")
		}
	case ArtifactRef:
		if err := ValidateArtifactRef(s, p); err != nil {
			return err
		}
	case GitPin:
		if err := validateCommit(s.ObjectFormat, s.Commit, p); err != nil {
			return err
		}
		if err := relativePath(s.Path, p+".path"); err != nil {
			return err
		}
	case ContentPin:
		if strings.TrimSpace(s.MediaType) == "" {
			return invalid(p+".media_type", "blank media type")
		}
	case Locator:
		if err := relativePath(s.Path, p+".path"); err != nil {
			return err
		}
	case Selector:
		if err := oneOf(s.Kind, p+".kind", "whole", "json-pointer"); err != nil {
			return err
		}
		if s.Kind == "whole" && s.Pointer != "" {
			return invalid(p, "whole selector takes no pointer")
		}
		if s.Kind == "json-pointer" && s.Pointer != "" {
			if s.Pointer[0] != '/' {
				return invalid(p+".pointer", "JSON pointer must begin with slash")
			}
			for i := 0; i < len(s.Pointer); i++ {
				if s.Pointer[i] == '~' {
					if i+1 == len(s.Pointer) || (s.Pointer[i+1] != '0' && s.Pointer[i+1] != '1') {
						return invalid(p+".pointer", "invalid JSON pointer escape")
					}
					i++
				}
			}
		}
	case time.Time:
		if s.IsZero() {
			return invalid(p, "missing time")
		}
		return nil
	case json.Number:
		_, err := DecimalRat(s)
		return err
	}
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			if tag == "" || tag == "-" {
				continue
			}
			fv := v.Field(i)
			if strings.Contains(f.Tag.Get("json"), ",omitempty") && fv.IsZero() {
				continue
			}
			at := p + "." + tag
			if f.Tag.Get("semantic") == "text" && strings.TrimSpace(fv.String()) == "" {
				return invalid(at, "whitespace-only is empty")
			}
			if f.Tag.Get("semantic") == "texts" {
				for j := 0; j < fv.Len(); j++ {
					if strings.TrimSpace(fv.Index(j).String()) == "" {
						return invalid(fmt.Sprintf("%s[%d]", at, j), "whitespace-only is empty")
					}
				}
			}
			if err := validateValue(fv, at); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if err := validateValue(v.Index(i), fmt.Sprintf("%s[%d]", p, i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			if err := validateValue(iter.Value(), p+"."+iter.Key().String()); err != nil {
				return err
			}
		}
	}
	return nil
}
func invalid(p, detail string) error { return fault("invalid-field", p, detail) }
func oneOf(value, p string, values ...string) error {
	for _, v := range values {
		if value == v {
			return nil
		}
	}
	return invalid(p, "unknown enum member: "+value)
}
func relativePaths(paths []string, p string) error {
	for i, s := range paths {
		if err := relativePath(s, fmt.Sprintf("%s[%d]", p, i)); err != nil {
			return err
		}
	}
	return nil
}
func relativePath(s, p string) error {
	if strings.TrimSpace(s) == "" || strings.ContainsAny(s, "\\\x00") || path.IsAbs(s) || strings.Contains(s, ":") {
		return invalid(p, "expected a nonblank project-relative path")
	}
	for _, part := range strings.Split(s, "/") {
		if part == ".." {
			return invalid(p, "path escapes the project root")
		}
	}
	return nil
}
func validateCommit(format, commit, p string) error {
	n := 40
	switch format {
	case "sha1":
	case "sha256":
		n = 64
	default:
		return invalid(p+".object_format", "unknown Git object format")
	}
	if len(commit) != n {
		return invalid(p+".commit", "commit must be a full object name")
	}
	for _, c := range commit {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return invalid(p+".commit", "commit must be lowercase hex")
		}
	}
	return nil
}
