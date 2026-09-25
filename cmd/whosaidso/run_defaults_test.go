package main

// Tests for run's defaults (step 5): omitted claim and criterion revisions
// resolve to the CURRENT admitted ones, a revised criterion is picked up, the
// highest admitted revision wins even when revisions skip, a named revision
// is kept, and an omission with no single answer is refused before launch.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
)

// runCriterion is the criterion reference the admitted run recorded.
func runCriterion(t *testing.T, root, invocation string) model.CriterionRef {
	t.Helper()
	inv, ok := boundSnapshot(t, root).Invocation(reduce.InvocationKey{Project: "test/cli", InvocationID: model.ID(invocation)})
	if !ok || inv.Start.CriterionRef.Value == nil {
		t.Fatalf("run %s is not admitted with a criterion", invocation)
	}
	return *inv.Start.CriterionRef.Value
}

func TestRunDefaultsPickTheCurrentRevisions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture producer is a POSIX shell script")
	}
	f := boundWorld(t)
	base := []string{"run", "--attempt-id", string(f.attempt), "--instrument", string(f.instrument), "--admit", "--reason", "defaults"}
	run := func(extra ...string) (string, string, int) {
		out, errs, code := cliRun(t, f.root, nil, "agent", append(append(append([]string{}, base...), extra...), "--", "/bin/sh", "tools/measure.sh")...)
		if m := familyAck.FindStringSubmatch(out); m != nil {
			return m[1], errs, code
		}
		return "", errs, code
	}
	inv, errs, code := run("--claim", string(f.claim), "--criterion-id", string(f.criterion))
	if code != 0 || runCriterion(t, f.root, inv).Revision != 1 || !strings.Contains(errs, "criterion "+string(f.criterion)+" revision 1") {
		t.Fatalf("control: the only revision is 1: %d %s", code, errs)
	}
	// A revised criterion is picked up, whichever part names it.
	boundCapture(t, f.root, "criterion.fix", "--criterion", string(f.criterion), "--set", "source_refs=[]")
	for _, named := range [][]string{{"--claim", string(f.claim), "--criterion-id", string(f.criterion)}, {"--claim", string(f.claim)}, {"--criterion-id", string(f.criterion)}} {
		inv, errs, code := run(named...)
		if got := runCriterion(t, f.root, inv); code != 0 || got.Revision != 2 || got.Claim.Revision != 1 || got.CriterionID != f.criterion || got.Claim.RecordID != f.claim {
			t.Fatalf("%v must record the current criterion revision 2: %d %+v %s", named, code, got, errs)
		}
	}
	// The highest admitted revision wins, not a count: revisions may skip.
	boundCapture(t, f.root, "criterion.fix", "--criterion", string(f.criterion), "--set", "source_refs=[]", "--set", "revision=5")
	if inv, errs, code := run("--claim", string(f.claim)); code != 0 || runCriterion(t, f.root, inv).Revision != 5 {
		t.Fatalf("the current revision is 5: %d %s", code, errs)
	}
	// A named revision is checked and kept, never replaced by the current one.
	if inv, errs, code := run("--claim", string(f.claim), "--criterion-revision", "2"); code != 0 || runCriterion(t, f.root, inv).Revision != 2 {
		t.Fatalf("a named revision 2 must be recorded as 2: %d %s", code, errs)
	}
	if inv, _, _ := run("--claim", string(f.claim), "--criterion-revision", "3"); inv != "" {
		t.Fatal("a named revision nobody admitted must be refused")
	}
	// The claim moves to revision 2, where no criterion is fixed: the default
	// refuses, naming the revision it is fixed on, and launches nothing.
	boundCapture(t, f.root, "claim.revise", "--from", string(f.claim), "--set", "provenance.source_refs=[]", "--set", "replacement.scope.limitations=revised")
	marker := filepath.Join(f.root, "launched")
	out, errs, code := cliRun(t, f.root, nil, "agent", append(append([]string{}, base...), "--claim", string(f.claim), "--criterion-id", string(f.criterion), "--", "/usr/bin/touch", marker)...)
	if code != 1 || out != "" || !strings.Contains(errs, "no admitted revision on claim "+string(f.claim)+" revision 2 (claim revisions it is fixed on: 1)") {
		t.Fatalf("a criterion absent from the current claim revision must be refused: %d %q %s", code, out, errs)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("the refused run launched")
	}
	// A second criterion on the claim makes "the claim's criterion" ambiguous.
	boundCapture(t, f.root, "criterion.fix", "--claim", string(f.claim), "--set", "source_refs=[]", "--pin", "expression.result_selector=out/result.json#/results",
		"--pin", "expression.population.selector=out/result.json#/population", "--set", "expression.unit=mm", "--set", "expression.population.identity=pose sweep",
		"--set", "expression.population.denominator=poses", "--set", "expression.operator=lt", "--set", `expression.target={"type":"number","number":0.05}`, "--set", "expression.reducer=all")
	boundCapture(t, f.root, "criterion.fix", "--claim", string(f.claim), "--set", "source_refs=[]", "--pin", "expression.result_selector=out/result.json#/results",
		"--pin", "expression.population.selector=out/result.json#/population", "--set", "expression.unit=mm", "--set", "expression.population.identity=pose sweep",
		"--set", "expression.population.denominator=poses", "--set", "expression.operator=lt", "--set", `expression.target={"type":"number","number":0.04}`, "--set", "expression.reducer=all")
	if _, errs, code := cliRun(t, f.root, nil, "agent", append(append([]string{}, base...), "--claim", string(f.claim), "--", "/usr/bin/touch", marker)...); code != 1 || !strings.Contains(errs, "has 2 admitted criteria") {
		t.Fatalf("two criteria on the claim must be refused as ambiguous: %d %s", code, errs)
	}
}
