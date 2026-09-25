package acceptance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/evidence"
	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestVerifyExplicitUnavailableMetadataCannotBecomeAgreement(t *testing.T) {
	c := evidenceCriterion()
	root := t.TempDir()
	control := evidenceObserve(t, root, c, evidenceBody, 10)
	evidenceVerdict(t, c, []evidence.Observation{control}, evidence.True, "")

	for _, location := range []string{"results", "population"} {
		for _, field := range []string{"population", "denominator"} {
			t.Run(location+"/"+field, func(t *testing.T) {
				// Omission is allowed by the current vocabulary. An explicit
				// statement of unavailability is a different fact, however.
				var document map[string]map[string]any
				if err := json.Unmarshal([]byte(evidenceBody), &document); err != nil {
					t.Fatal(err)
				}
				delete(document[location], field)
				observe := func() evidence.Observation {
					t.Helper()
					body, err := json.Marshal(document)
					if err != nil {
						t.Fatal(err)
					}
					return evidenceObserve(t, root, c, string(body), 11)
				}
				omitted := observe()
				evidenceVerdict(t, c, []evidence.Observation{omitted}, evidence.True, "")
				for _, unavailable := range []struct {
					name  string
					value any
				}{
					{"null", nil},
					{"explicit_unknown", map[string]any{"state": "unknown", "reason": "instrument could not identify " + field}},
				} {
					t.Run(unavailable.name, func(t *testing.T) {
						document[location][field] = unavailable.value
						o := observe()
						reading := o.Result
						if location == "population" {
							reading = o.Population
						}
						metadata := reading.Population
						if field == "denominator" {
							metadata = reading.Denominator
						}
						if metadata.State != model.Unknown {
							t.Fatalf("fixture must reach evaluation as unavailable %s.%s, got %+v", location, field, metadata)
						}
						got, err := evidence.Evaluate(c, []evidence.Observation{o})
						if err != nil {
							t.Fatal(err)
						}
						if got.Verdict != evidence.Unknown || !strings.Contains(got.Reason, field) {
							t.Errorf("explicitly unavailable %s.%s produced %s (%q); want UNKNOWN naming %s. Accepting omitted metadata must not turn an artifact's explicit inability to identify its population or denominator into agreement with the criterion", location, field, got.Verdict, got.Reason, field)
						}
					})
				}
			})
		}
	}
}

func TestVerifySelectedPopulationMembersMustBelongToTheDeclaredPopulation(t *testing.T) {
	c := evidenceCriterion()
	root := t.TempDir()
	controlBody := strings.Replace(evidenceBody, `["pose-a","pose-b"]`, `[{"value":"pose-a","population":"pose sweep","denominator":"poses"},"pose-b"]`, 1)
	control := evidenceObserve(t, root, c, controlBody, 10)
	evidenceVerdict(t, c, []evidence.Observation{control}, evidence.True, "")

	for _, tc := range []struct{ field, raw string }{
		{"population", `"other sweep"`},
		{"denominator", `"frames"`},
		{"population", `null`},
		{"denominator", `null`},
	} {
		t.Run(tc.field+"/"+tc.raw, func(t *testing.T) {
			members := `[{"value":"pose-a","` + tc.field + `":` + tc.raw + `},"pose-b"]`
			body := strings.Replace(evidenceBody, `["pose-a","pose-b"]`, members, 1)
			o := evidenceObserve(t, root, c, body, 11)
			if len(o.Population.MemberMetadata) != 2 {
				t.Fatalf("selector must preserve both population member positions: %+v", o.Population)
			}
			if _, present := o.Population.MemberMetadata[0][tc.field]; !present {
				t.Fatalf("selector lost the population member's %s declaration", tc.field)
			}
			got, err := evidence.Evaluate(c, []evidence.Observation{o})
			if err != nil {
				t.Fatal(err)
			}
			if got.Verdict != evidence.Unknown || !strings.Contains(got.Reason, tc.field) {
				t.Errorf("selected population member 0 declares %s=%s, but evaluation returned %s (%q); want UNKNOWN naming the member's %s. A matching set label and cardinality cannot make a foreign or unidentified member part of the declared population", tc.field, tc.raw, got.Verdict, got.Reason, tc.field)
			}
		})
	}
}

func TestVerifyLFSPointerRecognitionRequiresTheActualVersionLine(t *testing.T) {
	root := t.TempDir()
	r := evidence.NewResolver(root)
	resolveText := func(body string) (evidence.ResolvedArtifact, error) {
		t.Helper()
		evidenceWrite(t, root, "out/result.json", body)
		ref := evidenceContent(body)
		ref.Content.MediaType = "text/plain"
		return r.Resolve(context.Background(), ref)
	}
	digest := evidenceDigest("payload")
	controlBody := fmt.Sprintf("LFS format notes\noid sha256:%s\nsize 7\n", digest)
	control, err := resolveText(controlBody)
	if err != nil || string(control.Bytes) != controlBody {
		t.Fatalf("control: ordinary pinned UTF-8 text must resolve unchanged: %+v, %v", control, err)
	}

	// A real stand-in is refused, so accepting the examples below must not
	// be achieved by removing pointer detection altogether.
	pointer := fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize 7\n", digest)
	if _, err := resolveText(pointer); err == nil || !strings.Contains(err.Error(), "LFS pointer") {
		t.Fatalf("control: an actual LFS pointer must be refused as a pointer, got %v", err)
	}
	for _, suffix := range []string{"-example", "0"} {
		t.Run(suffix, func(t *testing.T) {
			body := strings.Replace(pointer, "/spec/v1\n", "/spec/v1"+suffix+"\n", 1)
			got, err := resolveText(body)
			if err != nil || string(got.Bytes) != body || got.LFSPointer {
				t.Errorf("ordinary text naming a different version URL was treated as a payload stand-in: %v; want the exact pinned text. A prefix of the LFS version line is not the actual version line, and the new content-path refusal must not make non-pointer documents unreadable", err)
			}
		})
	}
}
