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
		ExecutionSourceIdentity: testExecution(),
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

// testMachine is the one machine every fixture run executes on, so families
// compare unless a test says otherwise.
const testMachine model.ID = "01ARZ3NDEKTSV4RRFFQ69G5FZZ"

func testExecution() model.ExecutionIdentity {
	machine := testMachine
	return model.ExecutionIdentity{Project: projectID, SourceRefs: []model.ArtifactRef{},
		MachineID: model.Availability[model.ID]{State: model.Known, Value: &machine},
		Head:      model.Availability[model.GitHead]{State: model.Unknown, Reason: "fixture"},
		Dirty:     model.Availability[bool]{State: model.Unknown, Reason: "fixture"}}
}

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
		Instrument:         model.RecordRef{Project: projectID, RecordID: instrumentI, Revision: 1},
		Execution:          testExecution(),
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

func TestCounterexampleStillNeedsSharedEvidence(t *testing.T) {
	for _, alter := range []func(*Observation){
		func(o *Observation) { o.Result.Unit = unknown("unit unavailable") },
		func(o *Observation) { o.Result.Population = unknown("population unavailable") },
		func(o *Observation) { o.Population.Denominator = known("other") },
		func(o *Observation) {
			o.Population.MemberMetadata = []map[string]model.Availability[string]{{"population": unknown("unidentified")}}
		},
		func(o *Observation) { o.Population = sizedPopulation(1) },
		func(o *Observation) { o.Population = sizedPopulation(3) },
		func(o *Observation) {
			o.Outcome = model.Availability[model.ProcessOutcome]{State: model.Unknown, Reason: "not completed"}
		},
		func(o *Observation) { o.Unavailable = "artifact unavailable" },
	} {
		c := testCriterion(t)
		o := observation(invocationA, selectResult(t, "0.9,null"), sizedPopulation(2))
		wantVerdict(t, evaluate(t, c, o), False, "")
		alter(&o)
		wantVerdict(t, evaluate(t, c, o), Unknown, "")
		// An independent observation can still fail the family in either order.
		other := observation(invocationB, numbersRead("mm", "pose sweep", "poses", "0.9"), sizedPopulation(1))
		wantVerdict(t, evaluate(t, c, o, other), False, "")
		wantVerdict(t, evaluate(t, c, other, o), False, "")
	}
}
