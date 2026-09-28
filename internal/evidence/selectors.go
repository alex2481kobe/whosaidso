package evidence

// Artifact readings, scalar selection, and declared measurement metadata live here.
// Byte retrieval, JSON parsing, and criterion evaluation do not.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// WholeUnit and WholePopulation are what a whole-artifact selector reports.
// A whole selector reads identity, not a measured quantity, so its reading is
// the artifact's raw digest and it says so. A criterion that declares any other
// unit against a whole selector is asking a question these bytes cannot answer.
const (
	WholeUnit       = "sha256"
	WholePopulation = "artifact"
)

// ---- selectors -----------------------------------------------------------

// ReadingKind says what the selector found. Absent is a first-class answer and
// carries the reason, because an absent reading is information we do not have
// rather than a value to fill in.
type ReadingKind string

const (
	ReadingScalar ReadingKind = "scalar"
	ReadingSet    ReadingKind = "set"
	ReadingAbsent ReadingKind = "absent"
)

// Reading is what a selector actually read out of pinned bytes, with the facts
// the artifact stated about it. Unit, Population and Denominator come from the
// artifact, never from the record: a unit retyped next to a number is a second
// copy that can drift from the measurement it labels.
// Metadata's zero State means omitted; Unknown means declared unavailable.
type Reading struct {
	Artifact    model.Digest
	Selector    model.Selector
	Kind        ReadingKind
	Scalar      model.Scalar
	Values      []model.Scalar
	Unit        model.Availability[string]
	Population  model.Availability[string]
	Denominator model.Availability[string]
	Reason      string
	// MemberMetadata parallels Values; absent keys were not declared by the member.
	MemberMetadata []map[string]model.Availability[string]
	// MemberReasons parallels Values. A nonblank entry marks an unreadable slot;
	// its zero Scalar is never a measurement. Positions and coverage are retained.
	MemberReasons []string
	// MemberNames parallels Values: the member's own identifying string field
	// (memberNameKeys), or blank when it states none. It only names a member
	// in a reason; it is the artifact's claim and is never compared.
	MemberNames []string
}

// memberNameKeys are the fields that identify a set member, in preference
// order: readings commonly carry "path".
var memberNameKeys = []string{"path", "id", "name"}

// Scalars returns a complete readable selection, never unreadable placeholders.
func (r Reading) Scalars() ([]model.Scalar, bool) {
	for _, reason := range r.MemberReasons {
		if reason != "" {
			return nil, false
		}
	}
	return r.selectedValues()
}

// selectedValues includes partial sets; callers must consult MemberReasons.
func (r Reading) selectedValues() ([]model.Scalar, bool) {
	switch r.Kind {
	case ReadingScalar:
		return []model.Scalar{r.Scalar}, true
	case ReadingSet:
		return r.Values, true
	}
	return nil, false
}

// Size is the cardinality a denominator is measured against.
func (r Reading) Size() (int, bool) {
	v, ok := r.Scalars()
	return len(v), ok
}

// Select applies a selector to resolved bytes.
//
// Numbers keep their exact decimal text the whole way through: 0.30 stays
// "0.30" and 9007199254740993 stays itself. Passing a threshold through float64
// would make a comparison mean something slightly different from what the
// criterion says, which is the one thing a threshold cannot afford.
func Select(a ResolvedArtifact, sel model.Selector) (Reading, error) {
	out := Reading{Artifact: a.SHA256, Selector: sel}
	switch sel.Kind {
	case "whole":
		// The reading of a whole artifact is its identity. That is a real,
		// checkable value, and it is honestly labelled as a digest rather than
		// dressed up as a measurement of the thing inside.
		out.Kind = ReadingScalar
		out.Scalar = stringScalar(string(a.SHA256))
		out.Unit = known(WholeUnit)
		out.Population = known(WholePopulation)
		out.Denominator = known(WholePopulation)
		return out, nil
	case "json-pointer":
	default:
		return Reading{}, fault("invalid-field", "selector.kind", "unknown selector kind: "+sel.Kind)
	}

	root, err := decodeJSON(a.Bytes)
	if err != nil {
		return Reading{}, err
	}
	value, parent, ok, err := pointerValue(root, sel.Pointer)
	if err != nil {
		return Reading{}, err
	}
	if !ok {
		out.Kind = ReadingAbsent
		out.Reason = "the artifact has no value at pointer " + quote(sel.Pointer)
		return out, nil
	}

	obj, _ := value.(map[string]any)
	out.Unit, out.Population, out.Denominator = metaFrom(obj, parent)

	switch v := value.(type) {
	case map[string]any:
		inner, found := v["value"]
		if !found {
			if vals, haveList := v["values"]; haveList {
				inner = vals
				found = true
			}
		}
		if !found {
			out.Kind = ReadingAbsent
			out.Reason = "the selected object states no value or values member"
			return out, nil
		}
		return finish(out, inner)
	default:
		return finish(out, v)
	}
}

func finish(out Reading, v any) (Reading, error) {
	switch t := v.(type) {
	case []any:
		out.Kind = ReadingSet
		out.Values = make([]model.Scalar, len(t))
		out.MemberReasons = make([]string, len(t))
		readable := 0
		out.MemberMetadata = make([]map[string]model.Availability[string], len(t))
		out.MemberNames = make([]string, len(t))
		for i, e := range t {
			if obj, ok := e.(map[string]any); ok {
				for _, key := range memberNameKeys {
					if name, isString := obj[key].(string); isString && strings.TrimSpace(name) != "" && out.MemberNames[i] == "" {
						out.MemberNames[i] = name
					}
				}
				unit, population, denominator := metaFrom(obj, nil)
				declared := map[string]model.Availability[string]{"unit": unit, "population": population, "denominator": denominator}
				for key := range declared {
					if _, present := obj[key]; !present {
						delete(declared, key)
					}
				}
				out.MemberMetadata[i] = declared
				inner, found := obj["value"]
				if !found {
					out.MemberReasons[i] = fmt.Sprintf("member %d of the selected set states no value", i)
				}
				e = inner
			}
			s, ok := scalarOf(e)
			if !ok {
				if out.MemberReasons[i] == "" {
					out.MemberReasons[i] = fmt.Sprintf("member %d of the selected set is not a comparable value", i)
				}
				if out.Reason == "" {
					out.Reason = out.MemberReasons[i]
				}
				continue
			}
			out.Values[i] = s
			readable++
		}
		if len(t) > 0 && readable == 0 {
			out.Kind = ReadingAbsent
		}
		return out, nil
	default:
		s, ok := scalarOf(t)
		if !ok {
			out.Kind = ReadingAbsent
			out.Reason = "the selected value is not a comparable value"
			return out, nil
		}
		out.Kind, out.Scalar = ReadingScalar, s
		return out, nil
	}
}

func scalarOf(v any) (model.Scalar, bool) {
	switch t := v.(type) {
	case json.Number:
		n := t
		return model.Scalar{Type: "number", Number: &n}, true
	case string:
		s := t
		return model.Scalar{Type: "string", String: &s}, true
	case bool:
		b := t
		return model.Scalar{Type: "bool", Bool: &b}, true
	}
	return model.Scalar{}, false
}

func stringScalar(s string) model.Scalar { return model.Scalar{Type: "string", String: &s} }

func numberScalar(n json.Number) model.Scalar { return model.Scalar{Type: "number", Number: &n} }

func known(s string) model.Availability[string] {
	v := s
	return model.Availability[string]{State: model.Known, Value: &v}
}

func unknown(reason string) model.Availability[string] {
	return model.Availability[string]{State: model.Unknown, Reason: reason}
}

// metaFrom reads what the artifact says about its own numbers, looking at the
// object holding them and then at its parent. Nothing is defaulted: an artifact
// that does not state its unit leaves the unit unknown, and a criterion cannot
// be evaluated against a number whose unit nobody recorded.
func metaFrom(selected, parent map[string]any) (unit, population, denominator model.Availability[string]) {
	pick := func(key, missing string) model.Availability[string] {
		for i, o := range []map[string]any{selected, parent} {
			raw, ok := o[key]
			if !ok {
				continue
			}
			if obj, ok := raw.(map[string]any); ok && i > 0 {
				// A parent's sibling reading is not an inherited label.
				_, value := obj["value"]
				_, values := obj["values"]
				_, state := obj["state"]
				if !state && (value || values) {
					continue
				}
			}
			s, isText := raw.(string)
			if !isText || strings.TrimSpace(s) == "" {
				return unknown("the artifact states a blank " + key)
			}
			return known(s)
		}
		return model.Availability[string]{Reason: missing}
	}
	return pick("unit", "the artifact does not state the unit of this reading"),
		pick("population", "the artifact does not state which population this reading covers"),
		pick("denominator", "the artifact does not state the denominator of this reading")
}

func quote(s string) string { return strconv.Quote(s) }
