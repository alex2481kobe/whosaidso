package evidence

// Resolver tests for selectors: exact JSON numbers and member metadata. Git and
// content pins and observations belong elsewhere.

import (
	"strings"
	"testing"

	"datum/internal/model"
)

// ---- selectors -----------------------------------------------------------

const selectorArtifact = `{
  "results": {
    "unit": "mm",
    "population": "twelve-pose sweep",
    "denominator": "poses",
    "values": [{"pose": "p1", "value": 0.30}, {"pose": "p2", "value": 0.00}]
  },
  "a/b": 1,
  "m~n": 2,
  "big": 9007199254740993,
  "nothing": null
}`

func resolved(t *testing.T, body string) ResolvedArtifact {
	t.Helper()
	return ResolvedArtifact{
		Bytes:  []byte(body),
		SHA256: model.HashBytes([]byte(body)),
		Length: uint64(len(body)),
	}
}

func TestSelectorsReadExactJSONNumbers(t *testing.T) {
	a := resolved(t, selectorArtifact)

	// Control: the pointer finds the set and the facts the artifact states about it.
	got, err := Select(a, model.Selector{Kind: "json-pointer", Pointer: "/results"})
	if err != nil {
		t.Fatalf("control: %v", err)
	}
	if got.Kind != ReadingSet || len(got.Values) != 2 {
		t.Fatalf("control: %+v", got)
	}
	if got.Unit.State != model.Known || *got.Unit.Value != "mm" {
		t.Fatalf("control: unit %+v", got.Unit)
	}
	if got.Denominator.State != model.Known || *got.Denominator.Value != "poses" {
		t.Fatalf("control: denominator %+v", got.Denominator)
	}

	t.Run("decimal text survives", func(t *testing.T) {
		got, err := Select(a, model.Selector{Kind: "json-pointer", Pointer: "/results/values/0/value"})
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != ReadingScalar || string(*got.Scalar.Number) != "0.30" {
			// 0.30 through float64 comes back as 0.3, a different spelling of a
			// number the criterion may well be comparing exactly.
			t.Fatalf("got %+v, want the exact token 0.30", got.Scalar.Number)
		}
	})

	t.Run("integers beyond float64 survive", func(t *testing.T) {
		got, err := Select(a, model.Selector{Kind: "json-pointer", Pointer: "/big"})
		if err != nil {
			t.Fatal(err)
		}
		if string(*got.Scalar.Number) != "9007199254740993" {
			t.Fatalf("got %v, want 9007199254740993", got.Scalar.Number)
		}
	})

	t.Run("escaped pointer tokens", func(t *testing.T) {
		for pointer, want := range map[string]string{"/a~1b": "1", "/m~0n": "2"} {
			got, err := Select(a, model.Selector{Kind: "json-pointer", Pointer: pointer})
			if err != nil {
				t.Fatalf("%s: %v", pointer, err)
			}
			if got.Kind != ReadingScalar || string(*got.Scalar.Number) != want {
				t.Fatalf("%s: got %+v, want %s", pointer, got, want)
			}
		}
	})

	t.Run("the whole artifact reads as its own identity", func(t *testing.T) {
		got, err := Select(a, model.Selector{Kind: "whole"})
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != ReadingScalar || *got.Scalar.String != string(a.SHA256) {
			t.Fatalf("got %+v, want the digest", got)
		}
		if *got.Unit.Value != WholeUnit {
			t.Fatalf("a whole selector must say what its reading is: %+v", got.Unit)
		}
	})

	t.Run("absent pointers are absent, not zero", func(t *testing.T) {
		for _, pointer := range []string{"/nope", "/results/values/9/value", "/results/values/0/missing"} {
			got, err := Select(a, model.Selector{Kind: "json-pointer", Pointer: pointer})
			if err != nil {
				t.Fatalf("%s: %v", pointer, err)
			}
			if got.Kind != ReadingAbsent || strings.TrimSpace(got.Reason) == "" {
				t.Fatalf("%s: expected an absent reading with a reason, got %+v", pointer, got)
			}
		}
	})

	t.Run("null is not an observation", func(t *testing.T) {
		got, err := Select(a, model.Selector{Kind: "json-pointer", Pointer: "/nothing"})
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != ReadingAbsent {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		cases := []struct {
			name string
			body string
			sel  model.Selector
			code string
		}{
			{"duplicate key gives one pointer two values", `{"unit":"mm","unit":"cm"}`,
				model.Selector{Kind: "json-pointer", Pointer: "/unit"}, "invalid-json"},
			{"artifact is not JSON", "\x89PNG\r\n\x1a\n",
				model.Selector{Kind: "json-pointer", Pointer: ""}, "invalid-json"},
			{"trailing content", `{"a":1} {"a":2}`,
				model.Selector{Kind: "json-pointer", Pointer: "/a"}, "invalid-json"},
			{"pointer without a leading slash", `{"a":1}`,
				model.Selector{Kind: "json-pointer", Pointer: "a"}, "invalid-field"},
			{"invalid escape", `{"a":1}`,
				model.Selector{Kind: "json-pointer", Pointer: "/a~2"}, "invalid-field"},
			{"unknown selector kind", `{"a":1}`,
				model.Selector{Kind: "xpath"}, "invalid-field"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := Select(resolved(t, tc.body), tc.sel)
				wantFault(t, err, tc.code)
			})
		}
	})
}

func TestSelectPreservesMemberMetadataDeclarations(t *testing.T) {
	for _, field := range []string{"unit", "population", "denominator"} {
		for _, raw := range []string{`"different"`, `""`, `null`, `{"state":"unknown","reason":"not observed"}`} {
			t.Run(field+"/"+raw, func(t *testing.T) {
				body := `{"unit":"mm","population":"pose sweep","denominator":"poses","values":[0.0200,{"value":0.0100,"` + field + `":` + raw + `},{"value":9007199254740993}]}`
				got, err := Select(resolved(t, body), model.Selector{Kind: "json-pointer"})
				if err != nil {
					t.Fatal(err)
				}
				values, ok := got.Scalars()
				if !ok || len(values) != 3 || string(*values[1].Number) != "0.0100" || string(*values[2].Number) != "9007199254740993" {
					t.Fatalf("resolution must retain exact values despite metadata disagreement: %+v", got)
				}
				if *got.Unit.Value != "mm" || *got.Population.Value != "pose sweep" || *got.Denominator.Value != "poses" {
					t.Fatalf("member declarations overwrote set metadata: %+v", got)
				}
				if len(got.MemberMetadata) != 3 || len(got.MemberMetadata[0]) != 0 || len(got.MemberMetadata[2]) != 0 || len(got.MemberMetadata[1]) != 1 {
					t.Fatalf("member declarations must retain their positions and omitted fields: %+v", got.MemberMetadata)
				}
				declared, present := got.MemberMetadata[1][field]
				if !present {
					t.Fatal("explicit declaration was lost")
				}
				if raw == `"different"` {
					if declared.State != model.Known || declared.Value == nil || *declared.Value != "different" {
						t.Fatalf("declared value lost: %+v", declared)
					}
				} else if declared.State != model.Unknown || declared.Value != nil || !strings.Contains(declared.Reason, field) {
					t.Fatalf("explicit unavailability must remain present with a reason: %+v", declared)
				}
			})
		}
	}
}

func TestSelectMetadataOmissionAndUnavailabilityStayDistinct(t *testing.T) {
	for _, field := range []string{"unit", "population", "denominator"} {
		for _, tc := range []struct {
			name, declaration string
			parent            bool
			state             model.AvailabilityState
		}{
			{"omitted", "", false, ""},
			{"inherited", "", true, model.Known},
			{"known", `"child"`, true, model.Known},
			{"null blocks inheritance", `null`, true, model.Unknown},
			{"unknown blocks inheritance", `{"state":"unknown","reason":"not measured"}`, true, model.Unknown},
			{"blank blocks inheritance", `""`, true, model.Unknown},
		} {
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				declaration, parent := "", ""
				if tc.declaration != "" {
					declaration = `,"` + field + `":` + tc.declaration
				}
				if tc.parent {
					parent = `"` + field + `":"parent",`
				}
				body := `{` + parent + `"reading":{"value":1` + declaration + `}}`
				read, err := Select(resolved(t, body), model.Selector{Kind: "json-pointer", Pointer: "/reading"})
				if err != nil {
					t.Fatal(err)
				}
				got := map[string]model.Availability[string]{"unit": read.Unit, "population": read.Population, "denominator": read.Denominator}[field]
				if got.State != tc.state {
					t.Fatalf("metadata state: %+v, want %q", got, tc.state)
				}
				if tc.state == model.Known {
					want := "parent"
					if tc.declaration != "" {
						want = "child"
					}
					if got.Value == nil || *got.Value != want {
						t.Fatalf("metadata value: %+v, want %q", got, want)
					}
				} else if got.Value != nil || !strings.Contains(got.Reason, field) {
					t.Fatalf("unobserved metadata lost its reason: %+v", got)
				}
			})
		}
	}
}

func TestSelectSiblingReadingIsNotInheritedMetadata(t *testing.T) {
	for _, field := range []string{"unit", "population", "denominator"} {
		for _, member := range []string{"value", "values"} {
			for _, explicit := range []string{"", `,"` + field + `":null`, `,"` + field + `":{"state":"unknown","reason":"not measured"}`} {
				body := `{"` + field + `":{"` + member + `":[]},"reading":{"value":1` + explicit + `}}`
				read, err := Select(resolved(t, body), model.Selector{Kind: "json-pointer", Pointer: "/reading"})
				if err != nil {
					t.Fatal(err)
				}
				got := map[string]model.Availability[string]{"unit": read.Unit, "population": read.Population, "denominator": read.Denominator}[field]
				want := model.AvailabilityState("")
				if explicit != "" {
					want = model.Unknown
				}
				if got.State != want {
					t.Fatalf("%s: got %+v, want %q", body, got, want)
				}
			}
		}
	}
}
