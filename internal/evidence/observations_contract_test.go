package evidence

// Observation tests for where a criterion's contract path resolves (R8.3/U12).
// Comparability and verdict tests stay in observations_test.go.

import (
	"testing"

	"whosaidso/internal/model"
)

// R8.3 as decided for U12, R9: the criterion's contract path resolves inside the
// observing run's own directory, never at the bare project path and never in
// another run's files.
func TestContractPathResolvesInThisRunsDirectoryOnly(t *testing.T) {
	a, b := RunDir(invocationA), RunDir(invocationB)
	contract := func(p string) model.ArtifactRef {
		return contentRef(criterionExample, "application/json", []string{p}, "json-pointer", "/results")
	}
	output := func(p string) model.ArtifactRef {
		return contentRef(resultArtifact, "application/json", []string{p}, "whole", "")
	}
	for _, tc := range []struct {
		name     string
		contract string
		outputs  []string
		want     string // matched path, or "" for no match
	}{
		{"own-run-directory", "out/result.json", []string{a + "/out/result.json"}, a + "/out/result.json"},
		{"bare-contract-path-names-nothing", "out/result.json", []string{"out/result.json"}, ""},
		{"another-runs-directory", "out/result.json", []string{b + "/out/result.json"}, ""},
		{"escape-by-dot-dot", "../" + string(invocationB) + "/out/result.json", []string{b + "/out/result.json"}, ""},
		{"named-other-run-directly", b + "/out/result.json", []string{b + "/out/result.json"}, ""},
		{"beside-a-bare-output-only-the-run-dir-counts", "out/result.json", []string{"out/result.json", a + "/out/result.json"}, a + "/out/result.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outs := []model.ArtifactRef{}
			for _, p := range tc.outputs {
				outs = append(outs, output(p))
			}
			got, _, why := matchOutput(outs, a, contract(tc.contract))
			if tc.want == "" {
				if why == "" {
					t.Fatalf("matched %v; a run must read only its own output", declaredPaths(got))
				}
				return
			}
			if why != "" || declaredPaths(got)[0] != tc.want {
				t.Fatalf("want %s, got %v (%s)", tc.want, declaredPaths(got), why)
			}
		})
	}
}
