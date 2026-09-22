package evidence

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"datum/internal/model"
)

const (
	projectID   = model.ProjectID("datum")
	claimID     = model.ID("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	criterionID = model.ID("01ARZ3NDEKTSV4RRFFQ69G5FAW")
	attemptID   = model.ID("01ARZ3NDEKTSV4RRFFQ69G5FBV")
	instrumentI = model.ID("01ARZ3NDEKTSV4RRFFQ69G5FBW")
	invocationA = model.ID("01ARZ3NDEKTSV4RRFFQ69G5FAX")
	invocationB = model.ID("01ARZ3NDEKTSV4RRFFQ69G5FAY")
)

func criterionIdentity() model.CriterionRef {
	return model.CriterionRef{
		Claim:       model.RecordRef{Project: projectID, RecordID: claimID, Revision: 1},
		CriterionID: criterionID,
		Revision:    1,
	}
}

// criterionExample is what the criterion pins: the instrument's output contract,
// frozen before any run. It is deliberately NOT the bytes any run produced, so a
// resolver that read the criterion's own pin instead of the run's output would
// be reading numbers no instrument measured here.
const criterionExample = `{"results": {"unit": "mm", "population": "pose sweep", "denominator": "poses", "values": []}}`

// testCriterion is the executable form of "every pose in the sweep penetrates
// less than 0.05mm". It is validated here so a later refusal cannot be an
// accident of a malformed fixture.
func testCriterion(t *testing.T) model.CriterionFix {
	t.Helper()
	target := numberScalar(json.Number("0.05"))
	c := model.CriterionFix{
		Claim:       model.RecordRef{Project: projectID, RecordID: claimID, Revision: 1},
		CriterionID: criterionID,
		Revision:    1,
		Expression: model.CriterionExpression{
			ResultSelector: contentRef(criterionExample, "application/json", []string{"out/result.json"}, "json-pointer", "/results"),
			Unit:           "mm",
			Population: model.Population{
				Identity:    "pose sweep",
				Selector:    contentRef(criterionExample, "application/json", []string{"out/result.json"}, "json-pointer", "/results/values"),
				Denominator: "poses",
			},
			Operator: model.Less,
			Target:   target,
			Reducer:  model.All,
		},
		Policy:     model.EvaluationPolicy{Inclusion: "entire-criterion-family", Retry: "retain-all"},
		Author:     model.Actor{ID: "lane-d"},
		SourceRefs: []model.ArtifactRef{},
	}
	if err := model.ValidateSchema(c); err != nil {
		t.Fatalf("fixture criterion is not valid: %v", err)
	}
	return c
}

func testEnvelope(t *testing.T, id model.ID) model.InvocationEnvelope {
	t.Helper()
	ref := criterionIdentity()
	exit := 0
	outcome := model.ProcessOutcome{Kind: "exit", ExitCode: &exit}
	return model.InvocationEnvelope{
		InvocationID:            id,
		AttemptID:               attemptID,
		InstrumentRef:           model.RecordRef{Project: projectID, RecordID: instrumentI, Revision: 1},
		CriterionRef:            model.Availability[model.CriterionRef]{State: model.Known, Value: &ref},
		ExecutionSourceIdentity: model.ExecutionIdentity{Project: projectID},
		Argv:                    []string{"./tools/penetration-check"},
		ConfigRequested:         map[string]model.Scalar{},
		ConfigEffective:         config("camera_pos", "12"),
		ConditionsDeclared:      map[string]model.Scalar{},
		ConditionsObserved:      config("backend", "1"),
		Outcome:                 model.Availability[model.ProcessOutcome]{State: model.Known, Value: &outcome},
		Visual:                  model.Availability[model.VisualObservation]{State: model.Unknown, Reason: "this instrument reports numbers, not frames"},
		StartedAt:               time.Unix(1_700_000_000, 0).UTC(),
	}
}

// ---- observation builders ------------------------------------------------

func config(pairs ...string) model.Availability[map[string]model.Availability[model.Scalar]] {
	m := map[string]model.Availability[model.Scalar]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		s := numberScalar(json.Number(pairs[i+1]))
		m[pairs[i]] = model.Availability[model.Scalar]{State: model.Known, Value: &s}
	}
	return model.Availability[map[string]model.Availability[model.Scalar]]{State: model.Known, Value: &m}
}

func unknownConfig(reason string) model.Availability[map[string]model.Availability[model.Scalar]] {
	return model.Availability[map[string]model.Availability[model.Scalar]]{State: model.Unknown, Reason: reason}
}

// numbersRead is what a resolved artifact looks like after Select: values plus
// the unit and population the artifact itself stated.
func numbersRead(unit, population, denominator string, ns ...string) Reading {
	r := Reading{
		Kind:        ReadingSet,
		Unit:        known(unit),
		Population:  known(population),
		Denominator: known(denominator),
		Artifact:    model.HashBytes([]byte(resultArtifact)),
		Selector:    model.Selector{Kind: "json-pointer", Pointer: "/results"},
	}
	for _, n := range ns {
		r.Values = append(r.Values, numberScalar(json.Number(n)))
	}
	return r
}

func sizedPopulation(n int) Reading {
	r := Reading{Kind: ReadingSet, Selector: model.Selector{Kind: "json-pointer", Pointer: "/results/values"}}
	for i := 0; i < n; i++ {
		r.Values = append(r.Values, numberScalar(json.Number("0")))
	}
	return r
}

func observation(id model.ID, result Reading, population Reading) Observation {
	ref := criterionIdentity()
	exit := 0
	outcome := model.ProcessOutcome{Kind: "exit", ExitCode: &exit}
	return Observation{
		InvocationRef:      model.InvocationRef{Project: projectID, InvocationID: id},
		CriterionRef:       model.Availability[model.CriterionRef]{State: model.Known, Value: &ref},
		Outcome:            model.Availability[model.ProcessOutcome]{State: model.Known, Value: &outcome},
		ConfigEffective:    config("camera_pos", "12"),
		ConditionsObserved: config("backend", "1"),
		Visual:             model.Availability[model.VisualObservation]{State: model.Unknown, Reason: "no frame was captured"},
		Result:             result,
		Population:         population,
	}
}

// passing is one clean run of the fixture criterion.
func passing(id model.ID) Observation {
	return observation(id, numbersRead("mm", "pose sweep", "poses", "0.01", "0.02"), sizedPopulation(2))
}

func evaluate(t *testing.T, c model.CriterionFix, obs ...Observation) Evaluation {
	t.Helper()
	ev, err := Evaluate(c, obs)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if ev.Verdict == Unknown && strings.TrimSpace(ev.Reason) == "" {
		t.Fatal("UNKNOWN without a reason is the answer this package exists to refuse")
	}
	return ev
}

func wantVerdict(t *testing.T, ev Evaluation, verdict Verdict, contains string) {
	t.Helper()
	if ev.Verdict != verdict {
		t.Fatalf("got %s (%s), want %s", ev.Verdict, ev.Reason, verdict)
	}
	if contains != "" && !strings.Contains(ev.Reason, contains) {
		t.Fatalf("expected the reason to mention %q, got %q", contains, ev.Reason)
	}
}

// ---- the control ---------------------------------------------------------

func TestEvaluateControl(t *testing.T) {
	c := testCriterion(t)
	ev := evaluate(t, c, passing(invocationA))
	wantVerdict(t, ev, True, "")
	if len(ev.Members) != 1 || ev.Members[0].Unit != "mm" || ev.Members[0].Compared != 2 {
		t.Fatalf("members: %+v", ev.Members)
	}
	if ev.Inclusion != "entire-criterion-family" || ev.Retry != "retain-all" {
		t.Fatalf("the frozen policy must travel with the result: %+v", ev)
	}
}

// review P's attack: one run finds 0.2mm, a later run finds zero on every pose.
// Proposing only the later run makes a false claim true. The family answers over
// both, and the earlier result is not a retry to be dropped.
func TestCounterexampleSurvivesARetry(t *testing.T) {
	c := testCriterion(t)

	// Control: the clean run alone satisfies the criterion.
	wantVerdict(t, evaluate(t, c, passing(invocationB)), True, "")

	first := observation(invocationA, numbersRead("mm", "pose sweep", "poses", "0.2", "0.0"), sizedPopulation(2))
	ev := evaluate(t, c, first, passing(invocationB))
	wantVerdict(t, ev, False, string(invocationA))
	if len(ev.Members) != 2 {
		t.Fatalf("every member of the family is evaluated and reported: %+v", ev.Members)
	}
}

// ---- units, populations, denominators ------------------------------------

func TestUnitMustMatchTheCriterion(t *testing.T) {
	c := testCriterion(t)
	wantVerdict(t, evaluate(t, c, passing(invocationA)), True, "")

	t.Run("a different unit refuses rather than compares", func(t *testing.T) {
		// 0.01cm is 0.1mm, which fails the same threshold. Comparing across
		// units would silently answer a different question.
		o := observation(invocationA, numbersRead("cm", "pose sweep", "poses", "0.01", "0.02"), sizedPopulation(2))
		wantVerdict(t, evaluate(t, c, o), Unknown, "unit mismatch")
	})

	t.Run("an artifact that states no unit", func(t *testing.T) {
		read := numbersRead("mm", "pose sweep", "poses", "0.01", "0.02")
		read.Unit = unknown("the artifact does not state the unit of this reading")
		o := observation(invocationA, read, sizedPopulation(2))
		wantVerdict(t, evaluate(t, c, o), Unknown, "unit")
	})
}

func TestDenominatorAndPopulationMustMatch(t *testing.T) {
	c := testCriterion(t)
	wantVerdict(t, evaluate(t, c, passing(invocationA)), True, "")

	t.Run("the artifact counted something else", func(t *testing.T) {
		o := observation(invocationA, numbersRead("mm", "pose sweep", "samples", "0.01", "0.02"), sizedPopulation(2))
		wantVerdict(t, evaluate(t, c, o), Unknown, "denominator mismatch")
	})

	t.Run("the artifact covered a different population", func(t *testing.T) {
		o := observation(invocationA, numbersRead("mm", "grip sweep", "poses", "0.01", "0.02"), sizedPopulation(2))
		wantVerdict(t, evaluate(t, c, o), Unknown, "population mismatch")
	})

	t.Run("the result covers part of the declared population", func(t *testing.T) {
		// Five readings against twelve declared poses is not "all poses pass".
		o := observation(invocationA, numbersRead("mm", "pose sweep", "poses", "0.01", "0.02", "0.01", "0.02", "0.01"), sizedPopulation(12))
		wantVerdict(t, evaluate(t, c, o), Unknown, "5 of the 12")
	})
}

func TestSelectedPopulationMetadataMustMatch(t *testing.T) {
	for _, reducer := range []model.CriterionReducer{model.All, model.Any, model.Count} {
		for _, field := range []string{"population", "denominator"} {
			for _, scenario := range []string{"passing", "failing", "empty true", "empty false"} {
				t.Run(string(reducer)+"/"+field+"/"+scenario, func(t *testing.T) {
					c := testCriterion(t)
					c.Expression.Reducer = reducer
					if reducer == model.Count {
						c.Expression.Operator = model.Equal
						c.Expression.Target = numberScalar(json.Number("2"))
					}
					o := passing(invocationA)
					want := True
					switch scenario {
					case "failing":
						o.Result.Values = []model.Scalar{numberScalar(json.Number("0.9"))}
						o.Population = sizedPopulation(1)
						want = False
					case "empty true", "empty false":
						o.Result.Values = nil
						o.Population = sizedPopulation(0)
						empty := scenario == "empty true"
						c.Expression.EmptyResult = &empty
						want = verdictOf(empty)
					}
					o.Population.Population = known("pose sweep")
					o.Population.Denominator = known("poses")
					wantVerdict(t, evaluate(t, c, o), want, "")
					if field == "population" {
						o.Population.Population = known("other sweep")
					} else {
						o.Population.Denominator = known("frames")
					}
					ev := evaluate(t, c, o)
					wantVerdict(t, ev, Unknown, field+" mismatch")
					if ev.Members[0].Verdict != Unknown || ev.Members[0].Compared != 0 {
						t.Fatalf("incompatible population must be refused before comparison: %+v", ev.Members[0])
					}
				})
			}
		}
	}
}

func TestEmptyPopulation(t *testing.T) {
	c := testCriterion(t)
	wantVerdict(t, evaluate(t, c, passing(invocationA)), True, "")

	empty := observation(invocationA, numbersRead("mm", "pose sweep", "poses"), sizedPopulation(0))

	t.Run("empty is unknown by default", func(t *testing.T) {
		// An empty result file and a clean sweep look identical from here.
		wantVerdict(t, evaluate(t, c, empty), Unknown, "empty population")
	})

	t.Run("empty means what the frozen criterion says it means", func(t *testing.T) {
		for _, authored := range []bool{true, false} {
			frozen := c
			expr := c.Expression
			value := authored
			expr.EmptyResult = &value
			frozen.Expression = expr
			want := False
			if authored {
				want = True
			}
			if got := evaluate(t, frozen, empty); got.Verdict != want {
				t.Fatalf("authored %v: got %s (%s)", authored, got.Verdict, got.Reason)
			}
		}
	})
}

// ---- absent information --------------------------------------------------

func TestExternalCitationIsNotAnObservation(t *testing.T) {
	c := testCriterion(t)
	wantVerdict(t, evaluate(t, c, passing(invocationA)), True, "")
	// A claim can cite a vendor's benchmark, another project or an agent's
	// report. None of them is a local observation, so the family is empty.
	wantVerdict(t, evaluate(t, c), Unknown, "context, not evidence")
}

func TestAnUnobservedRunKeepsTheFamilyUnknown(t *testing.T) {
	c := testCriterion(t)
	wantVerdict(t, evaluate(t, c, passing(invocationA)), True, "")

	killed := passing(invocationB)
	killed.Outcome = model.Availability[model.ProcessOutcome]{State: model.Unknown, Reason: "the observer was killed"}
	ev := evaluate(t, c, passing(invocationA), killed)
	wantVerdict(t, ev, Unknown, "the observer was killed")
	if len(ev.Members) != 2 {
		t.Fatalf("the unobserved run stays in the family: %+v", ev.Members)
	}
}

// Nothing here may supply a visual state nobody saw. A missing frame is a
// missing frame, not a rest pose and not a default camera.
func TestUnobservedVisualStateIsNeverInvented(t *testing.T) {
	c := testCriterion(t)
	wantVerdict(t, evaluate(t, c, passing(invocationA)), True, "")

	blind := passing(invocationA)
	blind.Result = Reading{Kind: ReadingAbsent, Reason: "the run recorded no joint transforms"}
	blind.Unavailable = "result selector: the transforms artifact was not captured"
	ev := evaluate(t, c, blind)
	wantVerdict(t, ev, Unknown, "transforms artifact was not captured")
	for _, m := range ev.Members {
		if m.Verdict != Unknown {
			t.Fatalf("an unobserved state produced a verdict: %+v", m)
		}
	}
}

func TestUncomparableValueIsUnknownNotFalse(t *testing.T) {
	c := testCriterion(t)
	wantVerdict(t, evaluate(t, c, passing(invocationA)), True, "")

	read := numbersRead("mm", "pose sweep", "poses")
	read.Values = []model.Scalar{stringScalar("clear")}
	o := observation(invocationA, read, sizedPopulation(1))
	wantVerdict(t, evaluate(t, c, o), Unknown, "not comparable")
}

// ---- conditions ----------------------------------------------------------

func TestEffectiveConfigurationDecidesComparability(t *testing.T) {
	c := testCriterion(t)

	// Control: two runs under the same effective configuration compare.
	wantVerdict(t, evaluate(t, c, passing(invocationA), passing(invocationB)), True, "")

	t.Run("a knob spelled two ways", func(t *testing.T) {
		drifted := passing(invocationB)
		drifted.ConfigEffective = config("cameraPosition", "12")
		wantVerdict(t, evaluate(t, c, passing(invocationA), drifted), Unknown, "cameraPosition")
	})

	t.Run("the same knob at a different value", func(t *testing.T) {
		other := passing(invocationB)
		other.ConfigEffective = config("camera_pos", "34")
		wantVerdict(t, evaluate(t, c, passing(invocationA), other), Unknown, "camera_pos")
	})

	t.Run("observed conditions that differ", func(t *testing.T) {
		other := passing(invocationB)
		other.ConditionsObserved = config("backend", "2")
		wantVerdict(t, evaluate(t, c, passing(invocationA), other), Unknown, "backend")
	})

	t.Run("configuration nobody observed", func(t *testing.T) {
		a, b := passing(invocationA), passing(invocationB)
		a.ConfigEffective = unknownConfig("the adapter reported no effective configuration")
		b.ConfigEffective = unknownConfig("the adapter reported no effective configuration")
		wantVerdict(t, evaluate(t, c, a, b), Unknown, "neither run observed")
	})

	t.Run("a single run is not compared with anything", func(t *testing.T) {
		lone := passing(invocationA)
		lone.ConfigEffective = unknownConfig("the adapter reported no effective configuration")
		wantVerdict(t, evaluate(t, c, lone), True, "")
	})
}

func TestUnknownSettingValuesDoNotEstablishComparability(t *testing.T) {
	for _, label := range []string{"effective configuration", "observed conditions"} {
		for _, tc := range []struct {
			name        string
			left, right string // a nonempty reason makes this setting UNKNOWN
		}{
			{"both unknown same reason", "not observed", "not observed"},
			{"both unknown different reasons", "first runner missed it", "second runner missed it"},
			{"left unknown", "not observed", ""},
			{"right unknown", "", "not observed"},
		} {
			t.Run(label+"/"+tc.name, func(t *testing.T) {
				c := testCriterion(t)
				a, b := passing(invocationA), passing(invocationB)
				left, right := config("sample_count", "12"), config("sample_count", "1.200e1")
				if label == "effective configuration" {
					a.ConfigEffective, b.ConfigEffective = left, right
				} else {
					a.ConditionsObserved, b.ConditionsObserved = left, right
				}
				wantVerdict(t, evaluate(t, c, a, b), True, "")
				if tc.left != "" {
					(*left.Value)["sample_count"] = model.Availability[model.Scalar]{State: model.Unknown, Reason: tc.left}
				}
				if tc.right != "" {
					(*right.Value)["sample_count"] = model.Availability[model.Scalar]{State: model.Unknown, Reason: tc.right}
				}
				forward := evaluate(t, c, a, b)
				wantVerdict(t, forward, Unknown, label)
				wantVerdict(t, forward, Unknown, "sample_count")
				if backward := evaluate(t, c, b, a); !reflect.DeepEqual(forward, backward) {
					t.Fatalf("order changed the refusal: %+v vs %+v", forward, backward)
				}
			})
		}
	}
}

// ---- arithmetic ----------------------------------------------------------

// A threshold has to mean exactly what it says. These two integers are one apart
// and identical as float64, so a comparison that went through floating point
// would answer the wrong question while looking perfectly reasonable.
func TestComparisonIsExactNotFloatingPoint(t *testing.T) {
	if float64(9007199254740993) != float64(9007199254740992) {
		t.Skip("this platform distinguishes these as float64, so the trap does not apply")
	}
	c := testCriterion(t)
	expr := c.Expression
	expr.Unit = "counts"
	expr.Operator = model.Greater
	expr.Target = numberScalar(json.Number("9007199254740992"))
	c.Expression = expr

	read := numbersRead("counts", "pose sweep", "poses", "9007199254740993")
	o := observation(invocationA, read, sizedPopulation(1))
	wantVerdict(t, evaluate(t, c, o), True, "")

	equal := numbersRead("counts", "pose sweep", "poses", "9007199254740992")
	wantVerdict(t, evaluate(t, c, observation(invocationA, equal, sizedPopulation(1))), False, "")
}

func TestReducers(t *testing.T) {
	base := testCriterion(t)

	t.Run("any", func(t *testing.T) {
		c := base
		expr := base.Expression
		expr.Reducer = model.Any
		c.Expression = expr
		hit := observation(invocationA, numbersRead("mm", "pose sweep", "poses", "0.9", "0.01"), sizedPopulation(2))
		wantVerdict(t, evaluate(t, c, hit), True, "")
		miss := observation(invocationA, numbersRead("mm", "pose sweep", "poses", "0.9", "0.8"), sizedPopulation(2))
		wantVerdict(t, evaluate(t, c, miss), False, "")
	})

	t.Run("count compares cardinality, not each value", func(t *testing.T) {
		c := base
		expr := base.Expression
		expr.Reducer = model.Count
		expr.Operator = model.Equal
		expr.Target = numberScalar(json.Number("2"))
		c.Expression = expr
		// Two failing poses out of twelve: count is about the subset, so the
		// cardinality rule that all and any need does not apply here.
		o := observation(invocationA, numbersRead("mm", "pose sweep", "poses", "0.9", "0.8"), sizedPopulation(12))
		wantVerdict(t, evaluate(t, c, o), True, "")

		expr.Target = numberScalar(json.Number("0"))
		c.Expression = expr
		wantVerdict(t, evaluate(t, c, o), False, "")
	})
}

// ---- family construction -------------------------------------------------

func TestFamilyConstructionIsRefusedWhenWrong(t *testing.T) {
	c := testCriterion(t)
	// Control: a well-formed family evaluates.
	if _, err := Evaluate(c, []Observation{passing(invocationA)}); err != nil {
		t.Fatalf("control: %v", err)
	}

	t.Run("an observation from a different criterion", func(t *testing.T) {
		foreign := passing(invocationB)
		other := criterionIdentity()
		other.Revision = 2
		foreign.CriterionRef = model.Availability[model.CriterionRef]{State: model.Known, Value: &other}
		_, err := Evaluate(c, []Observation{passing(invocationA), foreign})
		wantFault(t, err, "criterion")
	})

	t.Run("the same invocation counted twice", func(t *testing.T) {
		_, err := Evaluate(c, []Observation{passing(invocationA), passing(invocationA)})
		wantFault(t, err, "criterion")
	})

	t.Run("a run that names no criterion", func(t *testing.T) {
		loose := passing(invocationB)
		loose.CriterionRef = model.Availability[model.CriterionRef]{State: model.Unknown, Reason: "run outside the executable vocabulary"}
		wantVerdict(t, evaluate(t, c, loose), Unknown, "does not name the criterion")
	})

	t.Run("a malformed criterion is an error, not a verdict", func(t *testing.T) {
		bad := c
		expr := c.Expression
		expr.Reducer = "most"
		bad.Expression = expr
		_, err := Evaluate(bad, []Observation{passing(invocationA)})
		wantFault(t, err, "invalid-field")
	})
}

// The order observations arrive in is not a fact about the measurement, so it
// must not change the answer or the report.
func TestFamilyOrderDoesNotChangeTheResult(t *testing.T) {
	c := testCriterion(t)
	first := observation(invocationA, numbersRead("mm", "pose sweep", "poses", "0.2", "0.0"), sizedPopulation(2))
	forward := evaluate(t, c, first, passing(invocationB))
	backward := evaluate(t, c, passing(invocationB), first)
	if forward.Verdict != backward.Verdict || forward.Reason != backward.Reason {
		t.Fatalf("order changed the result: %+v vs %+v", forward, backward)
	}
	if forward.Members[0].InvocationRef != backward.Members[0].InvocationRef {
		t.Fatal("member order must be stable")
	}
}

func TestDescribeStatesTheReason(t *testing.T) {
	c := testCriterion(t)
	ev := evaluate(t, c)
	if !strings.Contains(ev.Describe(), "UNKNOWN") || !strings.Contains(ev.Describe(), ev.Reason) {
		t.Fatalf("describe: %q", ev.Describe())
	}
}
