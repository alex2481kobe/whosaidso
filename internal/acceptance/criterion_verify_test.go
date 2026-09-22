package acceptance_test

import (
	"fmt"
	"strings"
	"testing"

	"datum/internal/evidence"
	"datum/internal/model"
)

func TestCriterionVerifyBareValuesDoNotInheritSiblingReadingsAsUnavailableMetadata(t *testing.T) {
	for _, field := range []string{"population", "denominator"} {
		for _, shape := range []struct{ name, values, population string }{
			{"array", `[0.0100,0.0200]`, `["pose-a","pose-b"]`},
			{"scalar", `0.0100`, `["pose-a"]`},
		} {
			t.Run(field+"/"+shape.name, func(t *testing.T) {
				c := laneEEvidenceCriterion()
				c.Expression.Population.Selector.Selector.Pointer = "/" + field
				root := t.TempDir()
				body := func(result string) string {
					return fmt.Sprintf(`{"unit":"mm","results":%s,"%s":{"population":"pose sweep","denominator":"poses","values":%s}}`, result, field, shape.population)
				}
				control := laneEEvidenceObserve(t, root, c, body(`{"value":`+shape.values+`}`), 10)
				laneEEvidenceVerdict(t, c, []evidence.Observation{control}, evidence.True, "")

				// Both selector forms are supported. Removing a value wrapper
				// does not turn the sibling population reading into a label.
				bare := laneEEvidenceObserve(t, root, c, body(shape.values), 11)
				metadata := bare.Result.Population
				if field == "denominator" {
					metadata = bare.Result.Denominator
				}
				got, err := evidence.Evaluate(c, []evidence.Observation{bare})
				if err != nil {
					t.Fatal(err)
				}
				if metadata.State != "" || got.Verdict != evidence.True {
					t.Errorf("bare %s: want absent %s metadata and TRUE, as with the passing wrapper; got metadata state %q and %s (%q). The sibling is a separately selected reading, not a declaration of unavailable metadata; selector shape must not collapse ABSENT into UNKNOWN", shape.name, field, metadata.State, got.Verdict, got.Reason)
				}
			})
		}
	}
}

func TestCriterionVerifyIndependentCounterexampleSurvivesSiblingMetadataUnknown(t *testing.T) {
	c := laneEEvidenceCriterion()
	root := t.TempDir()
	control := laneEEvidenceObserve(t, root, c, laneEEvidenceBody, 10)
	laneEEvidenceVerdict(t, c, []evidence.Observation{control}, evidence.True, "")
	// This member identifies its own measurement completely. Its 0.9mm
	// counterexample does not depend on any declaration made by its sibling.
	counterexample := `{"value":0.9,"unit":"mm","population":"pose sweep","denominator":"poses"}`
	for _, field := range []string{"unit", "population", "denominator"} {
		for _, order := range []string{"counterexample_first", "counterexample_last"} {
			t.Run(field+"/"+order, func(t *testing.T) {
				makeObservation := func(declaration string) evidence.Observation {
					sibling := `{"value":0.0100` + declaration + `}`
					values := "[" + counterexample + "," + sibling + "]"
					if order == "counterexample_last" {
						values = "[" + sibling + "," + counterexample + "]"
					}
					body := strings.Replace(laneEEvidenceBody, "[0.0100,0.0200]", values, 1)
					return laneEEvidenceObserve(t, root, c, body, 11)
				}
				omitted := makeObservation("")
				laneEEvidenceVerdict(t, c, []evidence.Observation{omitted}, evidence.False, "does not satisfy")
				unknown := makeObservation(`,"` + field + `":null`)
				got, err := evidence.Evaluate(c, []evidence.Observation{unknown})
				if err != nil {
					t.Fatal(err)
				}
				if got.Verdict != evidence.False {
					t.Errorf("want FALSE from the independently identified 0.9mm member, got %s (%q) after only its sibling's %s became unavailable. A member's UNKNOWN must not erase another member's established counterexample to ALL; the family-level guard must also hold inside a selected set", got.Verdict, got.Reason, field)
				}
			})
		}
	}
}

func TestCriterionVerifyUnreadableValueDoesNotDiscardIndependentCounterexample(t *testing.T) {
	c := laneEEvidenceCriterion()
	root := t.TempDir()
	control := laneEEvidenceObserve(t, root, c, laneEEvidenceBody, 10)
	laneEEvidenceVerdict(t, c, []evidence.Observation{control}, evidence.True, "")
	for _, unreadable := range []struct{ name, raw string }{
		{"null", `null`},
		{"missing_value", `{"unit":"mm","population":"pose sweep","denominator":"poses"}`},
	} {
		for _, order := range []string{"counterexample_first", "counterexample_last"} {
			t.Run(unreadable.name+"/"+order, func(t *testing.T) {
				observe := func(sibling string) evidence.Observation {
					values := "[0.9," + sibling + "]"
					if order == "counterexample_last" {
						values = "[" + sibling + ",0.9]"
					}
					return laneEEvidenceObserve(t, root, c, strings.Replace(laneEEvidenceBody, "[0.0100,0.0200]", values, 1), 11)
				}
				// The reducer already preserves a counterexample when a scalar
				// exists but cannot be compared. The selector must preserve it too.
				incomparable := observe(`"not measured"`)
				laneEEvidenceVerdict(t, c, []evidence.Observation{incomparable}, evidence.False, "does not satisfy")
				missing := observe(unreadable.raw)
				got, err := evidence.Evaluate(c, []evidence.Observation{missing})
				if err != nil {
					t.Fatal(err)
				}
				if got.Verdict != evidence.False {
					t.Errorf("want FALSE from the still-present 0.9mm measurement, got %s (%q), reading kind %q with %d retained scalars. An unreadable sibling must not cause Select to discard the entire set and erase a counterexample that the reducer preserves for other incomparable values", got.Verdict, got.Reason, missing.Result.Kind, len(missing.Result.Values))
				}
			})
		}
	}
}

func TestCriterionVerifyEmptyPopulationPolicyCannotBypassCoverage(t *testing.T) {
	for _, reducer := range []model.CriterionReducer{model.All, model.Any, model.Count} {
		for _, emptyResult := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/empty_%t", reducer, emptyResult), func(t *testing.T) {
				c := laneEEvidenceCriterion()
				c.Expression.Reducer = reducer
				c.Expression.EmptyResult = &emptyResult
				if reducer == model.Count {
					c.Expression.Operator = model.Equal
					c.Expression.Target = laneEEvidenceNumber("2")
				}
				root := t.TempDir()
				control := laneEEvidenceObserve(t, root, c, laneEEvidenceBody, 10)
				laneEEvidenceVerdict(t, c, []evidence.Observation{control}, evidence.True, "")
				emptyBody := strings.ReplaceAll(laneEEvidenceBody, `["pose-a","pose-b"]`, `[]`)
				consistent := laneEEvidenceObserve(t, root, c, strings.Replace(emptyBody, "[0.0100,0.0200]", "[]", 1), 11)
				wantEmpty := evidence.False
				if emptyResult {
					wantEmpty = evidence.True
				}
				laneEEvidenceVerdict(t, c, []evidence.Observation{consistent}, wantEmpty, "")

				inconsistent := laneEEvidenceObserve(t, root, c, emptyBody, 12)
				got, err := evidence.Evaluate(c, []evidence.Observation{inconsistent})
				if err != nil {
					t.Fatal(err)
				}
				if got.Verdict != evidence.Unknown || !strings.Contains(got.Reason, "population") {
					t.Errorf("want UNKNOWN naming population coverage for two results over zero declared members, got %s (%q). empty_result=%t defines a genuinely empty measurement; it cannot authorize incompatible nonempty results or manufacture a definite verdict before coverage is checked", got.Verdict, got.Reason, emptyResult)
				}
			})
		}
	}
}

func TestCriterionVerifyCountSubsetCannotExceedDeclaredPopulation(t *testing.T) {
	c := laneEEvidenceCriterion()
	c.Expression.Reducer = model.Count
	c.Expression.Operator = model.Equal
	c.Expression.Target = laneEEvidenceNumber("2")
	root := t.TempDir()
	control := laneEEvidenceObserve(t, root, c, laneEEvidenceBody, 10)
	laneEEvidenceVerdict(t, c, []evidence.Observation{control}, evidence.True, "")
	// Count may select fewer members than the population, unlike ALL/ANY.
	larger := strings.Replace(laneEEvidenceBody, `["pose-a","pose-b"]`, `["pose-a","pose-b","pose-c"]`, 1)
	subset := laneEEvidenceObserve(t, root, c, larger, 11)
	laneEEvidenceVerdict(t, c, []evidence.Observation{subset}, evidence.True, "")

	smaller := strings.Replace(laneEEvidenceBody, `["pose-a","pose-b"]`, `["pose-a"]`, 1)
	impossible := laneEEvidenceObserve(t, root, c, smaller, 12)
	got, err := evidence.Evaluate(c, []evidence.Observation{impossible})
	if err != nil {
		t.Fatal(err)
	}
	if got.Verdict != evidence.Unknown || !strings.Contains(got.Reason, "population") {
		t.Errorf("want UNKNOWN naming population coverage for a counted subset of two inside a population of one, got %s (%q). Exempting COUNT from exact coverage does not permit a subset larger than its declared denominator", got.Verdict, got.Reason)
	}
}
