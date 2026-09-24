package main

// Tests for proof drafting: `whosaidso check admission --family` lists exactly the
// family the gate counts (an earlier-revision run, an exact run and a run only
// a rejection recorded), the proof skeleton it prints leaves every judgment
// to the author, and that skeleton, filled with judgment only, admits.
// template --example pins a criterion's example under its output name.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
)

var familyAck = regexp.MustCompile(`(?m)^run (\S+) exit \S+; start (\S+) seal (\S+);`)

// familyRun runs the fixture producer under the criterion revision given
// ("" leaves both revisions to run's defaults) and returns the invocation
// and its two packets.
func familyRun(t *testing.T, f boundFixture, criterionRevision string, admit bool) (inv, start, seal string) {
	t.Helper()
	args := []string{"run", "--attempt-id", string(f.attempt), "--instrument", string(f.instrument), "--claim", string(f.claim), "--criterion-id", string(f.criterion)}
	if criterionRevision != "" {
		args = append(args, "--claim-revision", "1", "--criterion-revision", criterionRevision)
	}
	if admit {
		args = append(args, "--admit", "--reason", "fixture run")
	}
	out, errs, code := cliRun(t, f.root, nil, "lane", append(args, "--", "/bin/sh", "tools/measure.sh")...)
	m := familyAck.FindStringSubmatch(out)
	if code != 0 || m == nil {
		t.Fatalf("run: %d %q %q", code, out, errs)
	}
	return m[1], m[2], m[3]
}

func TestFamilyListsWhatTheGateCountsAndItsProofAdmits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture producer is a POSIX shell script")
	}
	f := boundWorld(t)
	earlier, _, _ := familyRun(t, f, "1", true)
	boundCapture(t, f.root, "criterion.fix", "--criterion", string(f.criterion), "--set", "source_refs=[]")
	exact, _, _ := familyRun(t, f, "2", true)
	rejected, start, seal := familyRun(t, f, "2", false)
	if _, errs, code := cliRun(t, f.root, nil, "coordinator", "admit", "--outcome", "rejected", "--reason", "a rehearsal run", start, seal); code != 0 {
		t.Fatalf("rejecting the run: %s", errs)
	}

	out, errs, code := cliRun(t, f.root, nil, "lane", "check", "admission", "--family", string(f.claim), "--json")
	var answer struct {
		Result    string             `json:"result"`
		Criterion model.CriterionRef `json:"criterion"`
		Members   []map[string]any   `json:"members"`
		Proof     []struct {
			Data map[string]any `json:"data"`
		} `json:"proof"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &answer) != nil {
		t.Fatalf("check admission --family: %d %s %s", code, out, errs)
	}
	classes := map[string]string{}
	for _, m := range answer.Members {
		classes[m["invocation"].(map[string]any)["invocation_id"].(string)] = m["class"].(string)
		if _, offered := m["disposition"]; offered {
			t.Errorf("a family row offers a disposition: %v", m)
		}
	}
	want := map[string]string{earlier: "earlier", exact: "exact", rejected: "rejected"}
	if answer.Result != "listed" || answer.Criterion.Revision != 2 || len(classes) != 3 || classes[earlier] != want[earlier] || classes[exact] != want[exact] || classes[rejected] != want[rejected] {
		t.Fatalf("the family must be the gate's three members at the current criterion revision 2: %s %+v %v", answer.Result, answer.Criterion, classes)
	}
	proof := answer.Proof[0].Data
	var listed []string
	for i := range boundAt(proof, "evidence").([]any) {
		e := boundAt(proof, "evidence").([]any)[i].(map[string]any)
		listed = append(listed, e["invocation_ref"].(map[string]any)["invocation_id"].(string))
		if !isPlaceholder(e["disposition"].(string)) || !isPlaceholder(e["reason"].(string)) {
			t.Errorf("the skeleton chose a disposition or reason: %v", e)
		}
	}
	for _, path := range []string{"judgment.reason", "verdict"} {
		if !isPlaceholder(boundAt(proof, path).(string)) {
			t.Errorf("the skeleton filled %s", path)
		}
	}
	sort.Strings(listed)
	all := []string{earlier, exact, rejected}
	sort.Strings(all)
	if strings.Join(listed, " ") != strings.Join(all, " ") {
		t.Fatalf("the skeleton's evidence must be exactly the family: %v", listed)
	}
	// The confirmation is the gate's: a list missing a member is a mismatch
	// naming it, whichever member is missing.
	project, err := store.Open(f.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, missing := range all {
		var short []model.InvocationRef
		for _, id := range all {
			if id != missing {
				short = append(short, model.InvocationRef{Project: "test/cli", InvocationID: model.ID(id)})
			}
		}
		mismatch, _, err := familyConfirm(context.Background(), project, answer.Criterion, short)
		if err != nil || !strings.Contains(strings.Join(mismatch, "\n"), missing) {
			t.Errorf("a family list missing %s must be a mismatch naming it: %v, %v", missing, mismatch, err)
		}
	}
	if text, _, code := cliRun(t, f.root, nil, "lane", "check", "admission", "--family", string(f.claim)); code != 0 || !strings.HasPrefix(text, "proof family at watermark") || !strings.Contains(text, "3 member(s)") {
		t.Fatalf("the text answer opens with its scope: %d %s", code, text)
	}

	// Control: a proof missing one member is refused by the gate, so the
	// family listed above is not more than the gate needs.
	index := map[string]int{}
	for i, id := range listedOrder(proof) {
		index[id] = i
	}
	disposition := func(id, d string) []string {
		i := index[id]
		return []string{"--set", "evidence[" + strconv.Itoa(i) + "].disposition=" + d, "--set", "evidence[" + strconv.Itoa(i) + "].reason=judged for this test"}
	}
	args := []string{"template", "proof.admit", "--claim", string(f.claim), "--set", "judgment.reason=the current revision's only run passes", "--set", "verdict=supports"}
	args = append(args, disposition(earlier, "inapplicable")...)
	args = append(args, disposition(exact, "supports")...)
	args = append(args, disposition(rejected, "inapplicable")...)
	for _, dropped := range all {
		printed, _, _ := cliRun(t, f.root, nil, "lane", args...)
		var tree []map[string]any
		if err := json.Unmarshal([]byte(printed), &tree); err != nil {
			t.Fatal(err)
		}
		data := tree[0]["data"].(map[string]any)
		var kept []any
		for i, e := range data["evidence"].([]any) {
			if listedOrder(data)[i] != dropped {
				delete(e.(map[string]any), "code_change") // the optional key --capture omits
				kept = append(kept, e)
			}
		}
		data["evidence"] = kept
		short, _ := json.Marshal(tree)
		file := filepath.Join(t.TempDir(), "proof.json")
		if err := os.WriteFile(file, short, 0600); err != nil {
			t.Fatal(err)
		}
		if out, errs, code := cliRun(t, f.root, nil, "lane", "check", "admission", "--events", file); code != 1 || !strings.Contains(out, string(dropped)) {
			t.Errorf("a proof omitting %s must be refused by the gate, naming it: %d %s %s", dropped, code, out, errs)
		}
	}
	if out, errs, code := cliRun(t, f.root, nil, "lane", append(args, "--capture", "--admit", "--reason", "family drafted by whosaidso")...); code != 0 {
		t.Fatalf("the drafted proof, judged, must admit: %d %s %s", code, out, errs)
	}
	claim := model.RecordRef{Project: "test/cli", RecordID: f.claim, Revision: 1}
	if p, _ := boundSnapshot(t, f.root).ClaimAt(claim); p.Status != reduce.StatusProven {
		t.Fatalf("the drafted proof left the claim %s", p.Status)
	}
}

func listedOrder(proof map[string]any) []string {
	var out []string
	for _, e := range boundAt(proof, "evidence").([]any) {
		out = append(out, e.(map[string]any)["invocation_ref"].(map[string]any)["invocation_id"].(string))
	}
	return out
}

// A criterion's example is pinned under its run output name from a local
// file, the file travels with the capture, and the criterion admits.
func TestTemplateExamplePinsACriterion(t *testing.T) {
	f := boundWorld(t)
	example := filepath.Join(t.TempDir(), "example.json")
	if err := os.WriteFile(example, []byte(e2ePass), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"template", "criterion.fix", "--claim", string(f.claim), "--example", "out/run.json=" + example,
		"--pin", "expression.result_selector=out/run.json#/results", "--pin", "expression.population.selector=out/run.json#/population"}
	printed, errs, code := cliRun(t, f.root, nil, "lane", args...)
	if code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	var tree []map[string]any
	if err := json.Unmarshal([]byte(printed), &tree); err != nil {
		t.Fatal(err)
	}
	result := pinOf(t, tree[0]["data"].(map[string]any), "expression.result_selector")
	if result.Content.SHA256 != model.HashBytes([]byte(e2ePass)) || result.Content.Locators[0].Path != "out/run.json" || result.Selector.Pointer != "/results" ||
		!strings.Contains(errs, "an example, never an observation") || !strings.Contains(errs, "blob     capture it with --blob "+example) {
		t.Fatalf("the example pin must carry the file's bytes under the output name: %+v\n%s", result.Content, errs)
	}
	for _, bad := range [][]string{{"--example", "out/other.json=" + example}, {"--pin", "expression.result_selector=out/run.json@HEAD"}} {
		if _, _, code := cliRun(t, f.root, nil, "lane", append(append([]string{}, args...), bad...)...); code != 2 {
			t.Errorf("%v must be a usage error, got %d", bad, code)
		}
	}
	judged := append(args, "--set", "expression.unit=mm", "--set", "expression.population.identity=pose sweep", "--set", "expression.population.denominator=poses",
		"--set", "expression.operator=lt", "--set", `expression.target={"type":"number","number":0.05}`, "--set", "expression.reducer=all", "--set", "source_refs=[]",
		"--capture", "--admit", "--reason", "criterion pinned from its example")
	if out, errs, code := cliRun(t, f.root, nil, "lane", judged...); code != 0 {
		t.Fatalf("the example-pinned criterion must admit: %d %s %s", code, out, errs)
	}
}
