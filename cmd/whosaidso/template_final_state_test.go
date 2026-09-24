package main

// Tests that what a bound template computes is computed from the FINAL draft:
// a criterion's metadata from the selectors left after every --pin and --set
// (a replaced pin or a --set selector never keeps an earlier reading's
// values), and an appended list element as a fresh skeleton instance (its
// own minted id at revision 1, as element 0). Each rule opens with a control.

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"whosaidso/internal/model"
)

func TestCriterionMetadataComesFromTheFinalSelectors(t *testing.T) {
	f := boundWorld(t)
	proofWrite(t, f.root, "good.json", `{"a":{"value":1,"unit":"ms","population":"runs of X","denominator":"runs"},"c":{"value":2,"unit":"s","population":"runs of Y","denominator":"trials"}}`)
	proofWrite(t, f.root, "bad.txt", "not json at all\n")
	good := []string{"criterion.fix", "--claim", string(f.claim), "--example", "good=" + filepath.Join(f.root, "good.json"),
		"--pin", "expression.result_selector=good#/a"}
	draft := func(extra ...string) ([3]any, string) {
		t.Helper()
		args := append(append([]string{"template"}, good...), extra...)
		out, errs, code := cliRun(t, f.root, nil, "lane", args...)
		var tree []map[string]any
		if code != 0 || json.Unmarshal([]byte(out), &tree) != nil {
			t.Fatalf("%v: %d %s", args, code, errs)
		}
		d, _ := tree[0]["data"].(map[string]any)
		return [3]any{boundAt(d, "expression.unit"), boundAt(d, "expression.population.identity"), boundAt(d, "expression.population.denominator")}, errs
	}
	placeholders := func(name string, got [3]any, errs string) {
		t.Helper()
		for i, v := range got {
			if s, _ := v.(string); !isPlaceholder(s) {
				t.Errorf("%s: field %d is %q; the final selector states nothing, so it must stay a placeholder", name, i, v)
			}
		}
		if strings.Contains(errs, "stated alike") {
			t.Errorf("%s: no field may be noted as derived:\n%s", name, errs)
		}
	}
	// Control: the one good pin fills all three.
	if got, _ := draft(); got != [3]any{"ms", "runs of X", "runs"} {
		t.Fatalf("control: the final selector /a states all three: %v", got)
	}
	// Route 1: a later --pin replaces the selector with one that reads nothing.
	got, errs := draft("--example", "bad="+filepath.Join(f.root, "bad.txt"), "--pin", "expression.result_selector=bad#/not_json")
	placeholders("replaced by a pin that reads nothing", got, errs)
	got, errs = draft("--pin", "expression.result_selector=good#/missing")
	placeholders("replaced by a pin at a missing pointer", got, errs)
	// Route 2: --set changes the pinned selector's pointer, or the artifact.
	got, errs = draft("--set", "expression.result_selector.selector.pointer=/not_json")
	placeholders("--set pointer to nothing", got, errs)
	got, errs = draft("--set", "expression.result_selector.content.sha256="+string(model.HashBytes([]byte("other bytes"))))
	placeholders("--set artifact no pin read", got, errs)
	// Both routes derive from what the final selector does read.
	if got, _ := draft("--pin", "expression.result_selector=good#/c"); got != [3]any{"s", "runs of Y", "trials"} {
		t.Errorf("a replacing pin's own reading fills the fields: %v", got)
	}
	if got, _ := draft("--set", "expression.result_selector.selector.pointer=/c"); got != [3]any{"s", "runs of Y", "trials"} {
		t.Errorf("a --set pointer's reading fills the fields: %v", got)
	}
	// An authored value is never replaced, before or after the pin.
	if got, _ := draft("--set", "expression.unit=kg"); got != [3]any{"kg", "runs of X", "runs"} {
		t.Errorf("--set on the field itself is the author's: %v", got)
	}
}

func TestAppendedCriteriaAreFreshSkeletonInstances(t *testing.T) {
	f := boundWorld(t)
	args := []string{"task.create", "--set", "provenance.source_refs=[]", "--set", "spec.intent=measure", "--set", "spec.subject=pose sweep",
		"--set", "spec.scope.source_paths=[]", "--set", "spec.scope.context_refs=[]", "--set", "spec.scope.applies_when=this fixture",
		"--set", "spec.scope.limitations=none", "--set", `spec.non_goals=["production writes"]`, "--set", "spec.context_refs=[]",
		"--set", "spec.constraint_refs=[]", "--set", "spec.prerequisites=[]", "--set", "spec.next_actor.id=lane",
		"--set", "spec.accepter=null", "--set", "spec.progress=null", "--set", "spec.acceptance_criteria[0].criterion=first property",
		"--set", "spec.acceptance_criteria[1].criterion=second property", "--set", "spec.acceptance_criteria[2].criterion=third property"}
	data := boundPrint(t, f.root, args...)
	ids := map[any]bool{boundAt(data, "id"): true}
	for i := 0; i < 3; i++ {
		at := fmt.Sprintf("spec.acceptance_criteria[%d]", i)
		id, _ := boundAt(data, at+".id").(string)
		if !model.ValidID(model.ID(id)) || ids[id] {
			t.Errorf("%s.id = %q: want a fresh id distinct from every other", at, id)
		}
		ids[id] = true
		if boundAt(data, at+".revision") != float64(1) {
			t.Errorf("%s.revision = %v: a minted id starts at revision 1", at, boundAt(data, at+".revision"))
		}
	}
	out, errs, code := cliRun(t, f.root, nil, "lane", append([]string{"template"}, args...)...)
	if code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	if text, errs, code := cliRun(t, f.root, []byte(out), "lane", "check", "admission", "--events", "-"); code != 0 || !strings.Contains(text, "\nresult: would-admit\n") {
		t.Fatalf("the printed draft must pass check admission: %d %s %s", code, text, errs)
	}
}

// A proof's code change between two commits the author chose: the object
// formats and the changed paths under the claim's scope are what the gate's
// own git observation (evidence.ScopeChanges) reports, filled only when both
// commits resolve; the commits stay the author's, and so does a value the
// author wrote.
func TestProofCodeChangeIsFilledFromGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	f := boundWorld(t)
	scoped := cliID(390)
	scope := model.Scope{SourcePaths: []string{"src"}, ContextRefs: []model.RecordRef{}, AppliesWhen: "this fixture", Limitations: "not a real ledger"}
	boundAdmit(t, f.root, &model.ClaimAssert{ID: scoped, Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}},
		Spec: model.ClaimSpec{Assertion: "the step is exact", Falsifier: "a step drifts", Scope: scope, ExternalRefs: []model.ExternalReference{}}})
	homeGit(t, f.root, "init", "-q")
	proofWrite(t, f.root, "src/step.go", "package step\n")
	homeGit(t, f.root, "add", "src/step.go")
	homeGit(t, f.root, "commit", "-q", "-m", "from")
	from := homeGit(t, f.root, "rev-parse", "HEAD")
	proofWrite(t, f.root, "src/step.go", "package step // fixed\n")
	proofWrite(t, f.root, "notes.txt", "outside the scope\n")
	homeGit(t, f.root, "add", "src/step.go", "notes.txt")
	homeGit(t, f.root, "commit", "-q", "-m", "to")
	to := homeGit(t, f.root, "rev-parse", "HEAD")
	proofWrite(t, f.root, "notes.txt", "outside the scope, again\n")
	homeGit(t, f.root, "commit", "-q", "-am", "outside only")
	outside := homeGit(t, f.root, "rev-parse", "HEAD")

	draft := func(fromCommit, toCommit string, extra ...string) map[string]any {
		t.Helper()
		args := append([]string{"proof.admit", "--set", `claim={"project":"test/cli","record_id":"` + string(scoped) + `","revision":1}`,
			"--set", "evidence[0].code_change.from.commit=" + fromCommit, "--set", "evidence[0].code_change.to.commit=" + toCommit}, extra...)
		return boundAt(boundPrint(t, f.root, args...), "evidence[0].code_change").(map[string]any)
	}
	placeholder := func(v any) bool {
		s, ok := v.(string)
		if list, isList := v.([]any); isList && len(list) == 1 {
			s, ok = list[0].(string)
		}
		return ok && isPlaceholder(s)
	}
	// Control: both commits resolve, git lists the one scoped path.
	c := draft(from, to)
	if boundAt(c, "from.object_format") != "sha1" || boundAt(c, "to.object_format") != "sha1" || fmt.Sprint(c["changed_paths"]) != "[src/step.go]" ||
		boundAt(c, "from.commit") != from || boundAt(c, "to.commit") != to {
		t.Fatalf("formats and scoped changed paths must be git's, the commits the author's: %v", c)
	}
	// A commit that does not resolve fills nothing.
	for name, pair := range map[string][2]string{"unknown to": {from, strings.Repeat("0", 40)}, "unknown from": {strings.Repeat("0", 40), to}, "short": {from[:12], to},
		// Digits and one e: a fixed abbreviation that JSON would read as a number.
		"digits and e": {"79461799e564", to}} {
		c := draft(pair[0], pair[1])
		if !placeholder(boundAt(c, "from.object_format")) || !placeholder(boundAt(c, "to.object_format")) || !placeholder(c["changed_paths"]) {
			t.Errorf("%s: an unresolved commit must leave every computed field a placeholder: %v", name, c)
		}
	}
	// No scoped change: the formats are git's, but no changed path is invented.
	if c := draft(to, outside); boundAt(c, "from.object_format") != "sha1" || !placeholder(c["changed_paths"]) {
		t.Errorf("no file under the scope changed; changed_paths must stay a placeholder: %v", c)
	}
	// A claim with no scope paths asks git nothing, so no commit was resolved.
	c = boundAt(boundPrint(t, f.root, "proof.admit", "--set", `claim={"project":"test/cli","record_id":"`+string(f.claim)+`","revision":1}`,
		"--set", "evidence[0].code_change.from.commit="+strings.Repeat("0", 40), "--set", "evidence[0].code_change.to.commit="+to), "evidence[0].code_change").(map[string]any)
	if !placeholder(boundAt(c, "from.object_format")) || !placeholder(c["changed_paths"]) {
		t.Errorf("an empty scope resolves no commit and fills nothing: %v", c)
	}
	// The author's own value is never replaced.
	if c := draft(from, to, "--set", `evidence[0].code_change.changed_paths=["src/other.go"]`); fmt.Sprint(c["changed_paths"]) != "[src/other.go]" {
		t.Errorf("an authored changed_paths must stay the author's: %v", c)
	}
	// Commits left to the author are left alone.
	c = boundAt(boundPrint(t, f.root, "proof.admit"), "evidence[0].code_change").(map[string]any)
	if !placeholder(boundAt(c, "from.object_format")) || !placeholder(c["changed_paths"]) {
		t.Errorf("with no commits chosen nothing is computed: %v", c)
	}
}
