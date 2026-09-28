package evidence

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestMemberMetadataMustBeComparableBeforeReduction(t *testing.T) {
	for _, reducer := range []model.CriterionReducer{model.All, model.Any, model.Count} {
		for _, field := range []struct{ name, matching string }{
			{"unit", `"mm"`}, {"population", `"pose sweep"`}, {"denominator", `"poses"`},
		} {
			for _, tc := range []struct {
				name, raw string
				want      Verdict
			}{
				{"omitted", "", True}, {"matching", field.matching, True},
				{"conflicting", `"different"`, Unknown}, {"blank", `""`, Unknown},
				{"null", `null`, Unknown}, {"explicit unknown", `{"state":"unknown","reason":"not observed"}`, Unknown},
			} {
				t.Run(string(reducer)+"/"+field.name+"/"+tc.name, func(t *testing.T) {
					c := testCriterion(t)
					c.Expression.Reducer = reducer
					if reducer == model.Count {
						c.Expression.Operator = model.Equal
						c.Expression.Target = numberScalar(json.Number("2"))
					}
					declaration := ""
					if tc.raw != "" {
						declaration = `,"` + field.name + `":` + tc.raw
					}
					body := `{"unit":"mm","population":"pose sweep","denominator":"poses","values":[0.0100,{"value":0.0200` + declaration + `}]}`
					read, err := Select(resolved(t, body), model.Selector{Kind: "json-pointer"})
					if err != nil {
						t.Fatal(err)
					}
					o := observation(invocationA, read, sizedPopulation(2))
					ev := evaluate(t, c, o)
					reason := ""
					if tc.want == Unknown {
						reason = "member 1 " + field.name
					}
					wantVerdict(t, ev, tc.want, reason)
					compared := 0
					if reducer == model.All {
						compared = 1
					} // only the usable sibling
					if tc.want == Unknown && ev.Members[0].Compared != compared {
						t.Fatalf("incomparable member was compared: %+v", ev.Members[0])
					}
				})
			}
		}
	}
}

func TestMemberMetadataUnknownCannotEraseIndependentCounterexample(t *testing.T) {
	c := testCriterion(t)
	body := `{"unit":"mm","population":"pose sweep","denominator":"poses","values":[{"value":0.0100,"unit":"cm"}]}`
	read, err := Select(resolved(t, body), model.Selector{Kind: "json-pointer"})
	if err != nil {
		t.Fatal(err)
	}
	uncertain := observation(invocationA, read, sizedPopulation(1))
	wantVerdict(t, evaluate(t, c, uncertain), Unknown, "unit")
	counterexample := observation(invocationB, numbersRead("mm", "pose sweep", "poses", "0.9"), sizedPopulation(1))
	counterexample.ConfigEffective = config("camera_pos", "34")
	forward := evaluate(t, c, uncertain, counterexample)
	wantVerdict(t, forward, False, "does not satisfy")
	if backward := evaluate(t, c, counterexample, uncertain); !reflect.DeepEqual(forward, backward) {
		t.Fatal("family order changed the independent counterexample")
	}
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

func TestMetadataAgreementAcrossSelections(t *testing.T) {
	for _, reducer := range []model.CriterionReducer{model.All, model.Any, model.Count} {
		for _, location := range []string{"result", "result member", "population", "population member"} {
			for _, field := range []struct{ name, matching string }{
				{"population", "pose sweep"}, {"denominator", "poses"},
			} {
				for _, tc := range []struct {
					name  string
					value any
					want  Verdict
				}{
					{"omitted", nil, True}, {"matching", field.matching, True},
					{"conflicting", "other", Unknown}, {"blank", "", Unknown},
					{"null", nil, Unknown},
					{"explicit unknown", map[string]any{"state": "unknown", "reason": "not measured"}, Unknown},
				} {
					t.Run(string(reducer)+"/"+location+"/"+field.name+"/"+tc.name, func(t *testing.T) {
						c := testCriterion(t)
						c.Expression.Reducer = reducer
						if reducer == model.Count {
							c.Expression.Operator = model.Equal
							c.Expression.Target = numberScalar(json.Number("2"))
						}
						member := map[string]any{"value": json.Number("0.01")}
						document := map[string]any{
							"unit": "mm", "population": "pose sweep", "denominator": "poses",
							"values": []any{json.Number("0.02"), member},
						}
						declaration := document
						if strings.HasSuffix(location, " member") {
							declaration = member
						}
						delete(declaration, field.name)
						if tc.name != "omitted" {
							declaration[field.name] = tc.value
						}
						body, err := json.Marshal(document)
						if err != nil {
							t.Fatal(err)
						}
						read, err := Select(resolved(t, string(body)), model.Selector{Kind: "json-pointer"})
						if err != nil {
							t.Fatal(err)
						}
						o := passing(invocationA)
						if strings.HasPrefix(location, "result") {
							o.Result = read
						} else {
							o.Population = read
						}
						reason := ""
						if tc.want == Unknown {
							reason = field.name
						}
						ev := evaluate(t, c, o)
						wantVerdict(t, ev, tc.want, reason)
						if tc.want == Unknown {
							compared := 0
							if reducer == model.All && location == "result member" {
								compared = 1
							}
							if ev.Members[0].Compared != compared {
								t.Fatalf("incomparable evidence was compared: %+v", ev.Members[0])
							}
							counterexample := observation(invocationB, numbersRead("mm", "pose sweep", "poses", "0.9"), sizedPopulation(1))
							wantVerdict(t, evaluate(t, c, o, counterexample), False, "")
							wantVerdict(t, evaluate(t, c, counterexample, o), False, "")
						}
					})
				}
			}
		}
	}
}

func TestSelectedCounterexampleUsesOnlyComparableMembers(t *testing.T) {
	for _, field := range []string{"unit", "population", "denominator"} {
		for _, bad := range []string{`null`, `"other"`, `""`, `{"state":"unknown","reason":"missing"}`} {
			for _, ownValue := range []string{"0.01", "0.9"} {
				for _, reverse := range []bool{false, true} {
					c := testCriterion(t)
					good := ownValue
					invalid := `{"value":0.9,"` + field + `":` + bad + `}`
					if reverse {
						good, invalid = invalid, good
					}
					r := selectResult(t, good+","+invalid)
					ev := evaluate(t, c, observation(invocationA, r, sizedPopulation(2)))
					want := Unknown
					if ownValue == "0.9" {
						want = False
					}
					wantVerdict(t, ev, want, "")
					if ev.Members[0].Compared != 1 {
						t.Fatalf("must compare only the usable member: %+v", ev)
					}
				}
			}
		}
	}
}

func selectResult(t *testing.T, values string) Reading {
	t.Helper()
	r, err := Select(resolved(t, `{"unit":"mm","population":"pose sweep","denominator":"poses","values":[`+values+`]}`), model.Selector{Kind: "json-pointer"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPartialSelectionRetainsPositionsAndRefusesCompleteScalars(t *testing.T) {
	for _, missing := range []string{`null`, `{}`, `{"value":null}`, `[]`, `{"value":{}}`} {
		for _, reverse := range []bool{false, true} {
			values, badIndex := "0.9,"+missing, 1
			if reverse {
				values, badIndex = missing+",0.9", 0
			}
			r := selectResult(t, values)
			if r.Kind != ReadingSet || len(r.Values) != 2 || len(r.MemberReasons) != 2 || r.MemberReasons[badIndex] == "" || r.MemberReasons[1-badIndex] != "" {
				t.Fatalf("lost member positions or availability: %+v", r)
			}
			if r.Values[badIndex].Number != nil || r.Values[1-badIndex].Number == nil {
				t.Fatalf("invented or discarded scalar: %+v", r)
			}
			if _, ok := r.Scalars(); ok {
				t.Fatal("partial set reported complete scalars")
			}
			if _, ok := r.Size(); ok {
				t.Fatal("partial set reported a known population")
			}
			for _, reducer := range []model.CriterionReducer{model.All, model.Any, model.Count} {
				c := testCriterion(t)
				c.Expression.Reducer = reducer
				want := Unknown
				if reducer == model.All {
					want = False
				}
				wantVerdict(t, evaluate(t, c, observation(invocationA, r, sizedPopulation(2))), want, "")
				wantVerdict(t, evaluate(t, c, observation(invocationA, r, r)), Unknown, "population")
			}
		}
	}
}

func TestMetadataRolesDoNotDependOnValueShape(t *testing.T) {
	for _, tc := range []struct {
		body  string
		state model.AvailabilityState
		value string
	}{
		{`{"population":{"values":["p"]},"results":[0.01]}`, "", ""},
		{`{"population":{"values":["p"]},"results":{"value":[0.01]}}`, "", ""},
		{`{"population":"pose sweep","results":[0.01]}`, model.Known, "pose sweep"},
		{`{"population":null,"results":[0.01]}`, model.Unknown, ""},
		{`{"population":{"state":"unknown","values":[]},"results":[0.01]}`, model.Unknown, ""},
		{`{"population":"pose sweep","results":{"population":null,"value":0.01}}`, model.Unknown, ""},
		{`{"population":"other","results":{"population":"pose sweep","value":0.01}}`, model.Known, "pose sweep"},
		{`{"results":{"population":{"values":[]},"value":0.01}}`, model.Unknown, ""},
	} {
		r, err := Select(resolved(t, tc.body), model.Selector{Kind: "json-pointer", Pointer: "/results"})
		if err != nil {
			t.Fatal(err)
		}
		if r.Population.State != tc.state || (tc.state == model.Known && (r.Population.Value == nil || *r.Population.Value != tc.value)) {
			t.Fatalf("%s: population %+v, want %s %s", tc.body, r.Population, tc.state, tc.value)
		}
	}
}

// A FALSE reason names the failing member by its own identifying field when it
// states one (for example "path"), and by index when it does not.
func TestFalseReasonNamesTheFailingMember(t *testing.T) {
	c := testCriterion(t)
	read := func(members string) Reading {
		t.Helper()
		got, err := Select(resolved(t, `{"results":{"unit":"mm","population":"pose sweep","denominator":"poses","values":[`+members+`]}}`),
			model.Selector{Kind: "json-pointer", Pointer: "/results"})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	// Control: a passing set with paths is TRUE.
	wantVerdict(t, evaluate(t, c, observation(invocationA, read(`{"path":"a.go","value":0.01},{"path":"b.go","value":0.02}`), sizedPopulation(2))), True, "")
	for _, tc := range []struct{ name, members, want string }{
		{"path", `{"path":"a.go","value":0.01},{"path":"internal/reduce/task.go","value":0.90}`, "internal/reduce/task.go: 0.90 does not satisfy lt 0.05"},
		{"id when no path", `{"id":"pose-7","value":0.01},{"id":"pose-8","value":0.90}`, "pose-8: 0.90 does not satisfy lt 0.05"},
		{"index without an identifier", `{"value":0.01},{"value":0.90}`, "member 1: 0.90 does not satisfy lt 0.05"},
		{"bare numbers keep the index", `0.01,0.90`, "member 1: 0.90 does not satisfy lt 0.05"},
		{"a blank path is no identifier", `{"path":"a.go","value":0.01},{"path":" ","value":0.90}`, "member 1: 0.90 does not satisfy lt 0.05"},
		{"a name that could fake the reason is quoted", `{"path":"a.go","value":0.01},{"path":"x: 0 satisfies","value":0.90}`, `"x: 0 satisfies": 0.90 does not satisfy lt 0.05`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantVerdict(t, evaluate(t, c, observation(invocationA, read(tc.members), sizedPopulation(2))), False, tc.want)
		})
	}
}
