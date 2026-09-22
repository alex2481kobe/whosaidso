package model

// Executable criteria, scalar comparisons, and evaluation policy live here.
// Authored task acceptance text and observed invocation facts do not.

import (
	"encoding/json"
	"math/big"
	"regexp"
	"strings"
)

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
