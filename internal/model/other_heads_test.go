package model

import (
	"fmt"
	"strings"
	"testing"
)

// otherHeadsBundle is sequence 3 whose predecessor is 01K5V8Q2220000000000000002
// at sequence 2, with whatever other_heads member the case injects.
func otherHeadsBundle(sequence uint64, member string) string {
	pred := `,"predecessor":"01K5V8Q2220000000000000002"`
	if sequence == 1 {
		pred = ""
	}
	return fmt.Sprintf(`{"version":1,"project":"p","sequence":%d,"command_id":"01K5V8Q3330000000000000003"%s%s,
		"request_digest":%q,"admitter":{"id":"a"},"recorded_at":"2026-09-22T00:00:00Z",
		"packets":[],"events":[{"type":"task.create","data":{}}]}`, sequence, pred, member, HashBytes([]byte("request")))
}

func TestDecodeBundleOtherHeadsShape(t *testing.T) {
	head := func(seq int, id string) string { return fmt.Sprintf(`{"sequence":%d,"command_id":%q}`, seq, id) }
	a, b := "01K5V8Q2220000000000000001", "01K5V8Q2220000000000000005"
	cases := []struct {
		name     string
		sequence uint64
		member   string
		refused  string // "" for a good control
	}{
		{"absent", 3, "", ""},
		{"one head", 3, `,"other_heads":[` + head(2, a) + `]`, ""},
		{"heads in canonical order", 3, `,"other_heads":[` + head(1, b) + `,` + head(2, a) + `]`, ""},
		{"empty list", 3, `,"other_heads":[]`, "bundle.other_heads"},
		{"null", 3, `,"other_heads":null`, "bundle.other_heads"},
		{"on the genesis bundle", 1, `,"other_heads":[` + head(1, a) + `]`, "bundle.other_heads"},
		{"a head at the bundle's own sequence", 3, `,"other_heads":[` + head(3, a) + `]`, "bundle.other_heads[0]"},
		{"the predecessor again", 3, `,"other_heads":[` + head(2, "01K5V8Q2220000000000000002") + `]`, "bundle.other_heads[0]"},
		{"out of canonical order", 3, `,"other_heads":[` + head(2, a) + `,` + head(1, b) + `]`, "bundle.other_heads[1]"},
		{"a repeated head", 3, `,"other_heads":[` + head(2, a) + `,` + head(2, a) + `]`, "bundle.other_heads[1]"},
		{"a malformed id", 3, `,"other_heads":[` + head(2, "nope") + `]`, "bundle.other_heads[0]"},
		{"a case variant inside a head", 3, `,"other_heads":[{"Sequence":2,"command_id":"` + a + `"}]`, "other_heads"},
		{"an unknown key inside a head", 3, `,"other_heads":[{"sequence":2,"command_id":"` + a + `","x":1}]`, "other_heads"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeBundle([]byte(otherHeadsBundle(tc.sequence, tc.member)))
			if tc.refused == "" {
				if err != nil {
					t.Fatalf("good control refused: %v", err)
				}
				if tc.member == "" && got.OtherHeads != nil {
					t.Fatalf("an absent field decoded to %v", got.OtherHeads)
				}
				return
			}
			f, ok := err.(*Fault)
			if !ok || !strings.Contains(f.Path, tc.refused) {
				t.Fatalf("want a refusal at %s, got %v", tc.refused, err)
			}
		})
	}
}

func TestBundleRefCanonicalOrder(t *testing.T) {
	low, high := ID("01K5V8Q2220000000000000001"), ID("01K5V8Q2220000000000000002")
	ordered := []BundleRef{{1, high}, {2, low}, {2, high}, {3, low}}
	for i := range ordered {
		for j := range ordered {
			if got, want := ordered[i].Before(ordered[j]), i < j; got != want {
				t.Errorf("%v before %v = %v, want %v", ordered[i], ordered[j], got, want)
			}
		}
	}
}
