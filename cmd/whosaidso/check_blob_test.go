package main

// Fresh-process test for `whosaidso check admission --events --blob` (DOGFOOD
// item 64): the blob is capture's --blob, and the dry run gives the verdict
// admission gives for the same events and blob, writing nothing.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whosaidso/internal/model"
)

func TestCheckAdmissionTakesCapturesBlob(t *testing.T) {
	root, criterion, _, _ := e2eWorld(t)
	// A second criterion whose example exists only in the file handed as --blob.
	example := strings.Replace(e2ePass, "0.0200", "0.0300", 1)
	result, population := e2ePin(example, "example/stdout.json", "application/json"), e2ePin(example, "example/stdout.json", "application/json")
	result.Selector, population.Selector = model.Selector{Kind: "json-pointer", Pointer: "/results"}, model.Selector{Kind: "json-pointer", Pointer: "/population"}
	target := json.Number("0.05")
	fix := &model.CriterionFix{Claim: criterion.Claim, CriterionID: cliID(900), Revision: 1, Author: model.Actor{ID: "lane"}, SourceRefs: []model.ArtifactRef{},
		Expression: model.CriterionExpression{ResultSelector: result, Unit: "mm", Population: model.Population{Identity: "pose sweep", Selector: population, Denominator: "poses"},
			Operator: model.Less, Target: model.Scalar{Type: "number", Number: &target}, Reducer: model.All},
		Policy: model.EvaluationPolicy{Inclusion: "entire-criterion-family", Retry: "retain-all"}}
	blob := filepath.Join(t.TempDir(), "stdout.json")
	wrong := filepath.Join(t.TempDir(), "other.json")
	os.WriteFile(blob, []byte(example), 0600)
	os.WriteFile(wrong, []byte(e2ePass), 0600)
	before := cliTree(t, root)
	for _, tc := range []struct {
		blob, result string
	}{
		{"", "would-refuse"},
		{wrong, "would-refuse"},
		{blob, "would-admit"},
	} {
		args := []string{"check", "admission", "--events", "-"}
		if tc.blob != "" {
			args = append(args, "--blob", tc.blob)
		}
		out, err := e2eInvoke(t, root, []model.TypedEvent{fix}, args...)
		if !strings.Contains(string(out), "\nresult: "+tc.result+"\n") || (err == nil) != (tc.result == "would-admit") {
			t.Fatalf("--blob %q: want %s, got %v\n%s", tc.blob, tc.result, err, out)
		}
		if tc.result == "would-refuse" && !strings.Contains(string(out), "no locator holds the pinned bytes") {
			t.Fatalf("--blob %q: the refusal must name the unresolved example:\n%s", tc.blob, out)
		}
	}
	if after := cliTree(t, root); after != before {
		t.Fatalf("check admission --blob wrote:\n%s\n%s", before, after)
	}
	if _, err := e2eInvoke(t, root, nil, "check", "admission", "--packet", string(cliID(1)), "--blob", blob); err == nil || !strings.Contains(err.Error(), "--blob belongs to --events") {
		t.Fatalf("--blob without --events must be refused as usage: %v", err)
	}
	// Admission agrees: the same events captured with the same --blob admit.
	if _, err := e2eInvoke(t, root, []model.TypedEvent{fix}, "capture", "--command-id", string(cliID(901)), "--blob", blob); err != nil {
		t.Fatal(err)
	}
	if out, err := e2eInvoke(t, root, nil, "check", "admission", "--packet", string(cliID(901))); err != nil || !strings.Contains(string(out), "\nresult: would-admit\n") {
		t.Fatalf("a captured packet's own blob must resolve in the dry run: %v\n%s", err, out)
	}
	if _, err := e2eInvoke(t, root, nil, "admit", "--command-id", string(cliID(902)), "--actor", "coordinator", "--outcome", "accepted", "--reason", "example blob", string(cliID(901))); err != nil {
		t.Fatalf("admission refused what the check admitted: %v", err)
	}
}
