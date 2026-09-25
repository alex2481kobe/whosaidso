package main

// Tests for what a bound template computes from its pins and final tree: a
// source.intake's digest and length from its pin, a criterion's metadata from
// the bytes its selectors read (only when uniquely stated), an omitted
// optional key added at a schema path, a pin outside the project carried as
// a blob, a pin appending one list element, and notes read from the final
// tree. Each rule opens with a control that must pass.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
)

func TestSourceIntakePinFillsDigestAndLength(t *testing.T) {
	f := boundWorld(t)
	note := "# old note\n\nmaybe the step ignores dt\n"
	proofWrite(t, f.root, "notes/old.md", note)
	args := []string{"source.intake", "--pin", "source_ref=notes/old.md", "--set", "speaker.unknown_reason=the note names no author",
		"--set", "order=0", "--set", `referents=[{"project":"test/cli","record_id":"` + string(f.task) + `","revision":1}]`}
	data := boundPrint(t, f.root, args...)
	if boundAt(data, "original_digest") != string(model.HashBytes([]byte(note))) || boundAt(data, "length") != float64(len(note)) {
		t.Fatalf("original_digest and length must be the pinned bytes' own: %v %v", boundAt(data, "original_digest"), boundAt(data, "length"))
	}
	// Control: nothing else is asked for, and the gate admits it.
	boundCapture(t, f.root, args...)
}

func TestCriterionMetadataIsFilledOnlyWhenTheBytesStateIt(t *testing.T) {
	f := boundWorld(t)
	draft := func(example, result, population string, extra ...string) map[string]any {
		t.Helper()
		proofWrite(t, f.root, "ex.json", example)
		args := append([]string{"criterion.fix", "--claim", string(f.claim), "--example", "stdout=" + filepath.Join(f.root, "ex.json"),
			"--pin", "expression.result_selector=stdout#" + result, "--pin", "expression.population.selector=stdout#" + population}, extra...)
		return boundPrint(t, f.root, args...)
	}
	fields := func(d map[string]any) [3]any {
		return [3]any{boundAt(d, "expression.unit"), boundAt(d, "expression.population.identity"), boundAt(d, "expression.population.denominator")}
	}
	stated := `{"a":{"value":1,"unit":"ms","population":"runs of X","denominator":"runs"},`
	// Control: every field stated alike is filled, and no judgment is.
	d := draft(stated+`"b":{"values":[1],"population":"runs of X","denominator":"runs","unit":"s"}}`, "/a", "/b")
	if got := fields(d); got != [3]any{"ms", "runs of X", "runs"} {
		t.Fatalf("uniquely stated metadata must be filled (the population reading's unit is not asked): %v", got)
	}
	for _, judgment := range []string{"expression.operator", "expression.target.type", "expression.reducer", "author.id"} {
		if s, _ := boundAt(d, judgment).(string); judgment != "author.id" && !isPlaceholder(s) {
			t.Errorf("%s is judgment and must stay a placeholder: %v", judgment, boundAt(d, judgment))
		}
	}
	for name, tc := range map[string]struct {
		example, result, population string
		want                        [3]bool // filled: unit, population, denominator
	}{
		"absent":                        {`{"a":{"value":1}}`, "/a", "/a", [3]bool{}},
		"blank is UNKNOWN":              {`{"a":{"value":1,"unit":" ","population":"p","denominator":"d"}}`, "/a", "/a", [3]bool{false, true, true}},
		"UNKNOWN beside a stated one":   {`{"a":{"unit":"ms","values":[{"value":1,"unit":" "}],"population":"p","denominator":"d"}}`, "/a", "/a", [3]bool{false, true, true}},
		"selectors disagree":            {stated + `"b":{"values":[1],"population":"other","denominator":"runs"}}`, "/a", "/b", [3]bool{true, false, true}},
		"members disagree":              {`{"a":{"unit":"ms","values":[{"value":1,"unit":"ms"},{"value":2,"unit":"s"}],"population":"p","denominator":"d"}}`, "/a", "/a", [3]bool{false, true, true}},
		"unit only from the population": {`{"a":{"value":1},"b":{"values":[1],"unit":"ms"}}`, "/a", "/b", [3]bool{}},
	} {
		got := fields(draft(tc.example, tc.result, tc.population))
		for i, filled := range tc.want {
			s, _ := got[i].(string)
			if isPlaceholder(s) == filled {
				t.Errorf("%s: field %d is %q, want filled %v", name, i, s, filled)
			}
		}
	}
	// A value the author already has (a --criterion copy) is never replaced.
	if got := fields(draft(stated+`"b":{"values":[1]}}`, "/a", "/a", "--criterion", string(f.criterion))); got != [3]any{"mm", "pose sweep", "poses"} {
		t.Errorf("a copied criterion's metadata must stay the author's: %v", got)
	}
}

func TestAmendFromAddsAnOmittedOptionalKey(t *testing.T) {
	f := boundWorld(t)
	base := []string{"task.amend", "--from", string(f.task), "--set", "provenance.source_refs=[]"}
	for _, bad := range []string{"replacement.bogus=1", "replacement.progress.bogus=x", "bogus.progress=x"} {
		if _, errs, code := cliRun(t, f.root, nil, "agent", append(append([]string{"template"}, base...), "--set", bad)...); code != 2 || !strings.Contains(errs, "has no field") {
			t.Errorf("--set %s is no schema path and must be refused: %d %s", bad, code, errs)
		}
	}
	// Control: the omitted progress is added by --set and --pin, and admits.
	args := append(base, "--set", "replacement.progress.summary=reproduced, then fixed",
		"--pin", "replacement.progress.witness_refs[0]=out/result.json")
	data := boundPrint(t, f.root, args...)
	if boundAt(data, "replacement.progress.summary") != "reproduced, then fixed" || boundAt(data, "replacement.progress.witness_refs[0].kind") != "content" {
		t.Fatalf("progress must be added at its schema path: %v", boundAt(data, "replacement.progress"))
	}
	boundCapture(t, f.root, args...)
	if spec, _ := boundSnapshot(t, f.root).Current(reduce.Ident{Project: "test/cli", ID: f.task}); spec.Task.Progress == nil || spec.Task.Progress.Summary != "reproduced, then fixed" || spec.Task.Progress.NextAction != "" {
		t.Fatalf("the admitted revision must carry the added progress, next_action omitted: %+v", spec.Task)
	}
}

func TestPinOutsideTheProjectTravelsAsABlob(t *testing.T) {
	f := boundWorld(t)
	boundCapture(t, f.root, "blocker.hold", "--task", string(f.task), "--set", "reason=resume", "--set", `actor={"id":"agent"}`, "--set", "criterion=the review exists")
	hold := string(openHold(t, boundSnapshot(t, f.root), f.task))
	outside := filepath.Join(filepath.Dir(f.root), "review.txt")
	if err := os.WriteFile(outside, []byte("reviewed outside the repo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../review.txt", outside} {
		ref := pinOf(t, boundPrint(t, f.root, "blocker.clear", "--hold", hold, "--pin", "resolving_witness="+path), "resolving_witness")
		if ref.Content == nil || ref.Content.SHA256 != model.HashBytes([]byte("reviewed outside the repo\n")) || ref.Content.Locators == nil || len(ref.Content.Locators) != 0 {
			t.Fatalf("%s: an outside pin stores content alone, no locator: %+v", path, ref.Content)
		}
	}
	// Control: the gate resolves it from the blob capture carried.
	boundCapture(t, f.root, "blocker.clear", "--hold", hold, "--pin", "resolving_witness=../review.txt")
	for _, b := range boundSnapshot(t, f.root).Blockers(reduce.Ident{Project: "test/cli", ID: f.task}) {
		if b.Open() {
			t.Fatal("the hold must be cleared by the outside witness")
		}
	}
}

func TestPinAppendsOneListElement(t *testing.T) {
	f := boundWorld(t)
	d := boundPrint(t, f.root, "task.close", "--task", string(f.task), "--pin", "acceptance_witness_refs[0].witness_ref=out/result.json",
		"--pin", "acceptance_witness_refs[1].witness_ref=validation/measure.json")
	if w := boundAt(d, "acceptance_witness_refs").([]any); len(w) != 2 || boundAt(d, "acceptance_witness_refs[1].witness_ref.kind") != "content" ||
		!isPlaceholder(boundAt(d, "acceptance_witness_refs[1].criterion_id").(string)) || boundAt(d, "acceptance_witness_refs[0].criterion_id") != string(f.ac) {
		t.Fatalf("[1] must append one skeleton element holding the pin, [0] unchanged: %v", w)
	}
	_, errs, code := cliRun(t, f.root, nil, "agent", "template", "task.close", "--task", string(f.task), "--pin", "acceptance_witness_refs[2].witness_ref=out/result.json")
	if code != 2 || !strings.Contains(errs, "acceptance_witness_refs has 1 element(s); a path can add only acceptance_witness_refs[1]") {
		t.Fatalf("a pin two past the end must be refused clearly: %d %s", code, errs)
	}
}

func TestNotesAreReadFromTheFinalTree(t *testing.T) {
	f := boundWorld(t)
	notes := func(args ...string) string {
		t.Helper()
		_, errs, code := cliRun(t, f.root, nil, "agent", append([]string{"template"}, args...)...)
		if code != 0 {
			t.Fatalf("%v: %d %s", args, code, errs)
		}
		return errs
	}
	closeNotes := notes("task.close", "--task", string(f.task), "--pin", "delivery_witness_refs[0]=out/result.json")
	if strings.Contains(closeNotes, "choose   delivery_witness_refs[0]") || !strings.Contains(closeNotes, "choose   acceptance_witness_refs[0].witness_ref.kind") {
		t.Errorf("a resolved choice is not noted; an open one is (control):\n%s", closeNotes)
	}
	fix := notes("criterion.fix", "--claim", string(f.claim))
	if n := strings.Count(fix, "filled   revision:"); n != 1 {
		t.Errorf("each filled path is noted once, got %d:\n%s", n, fix)
	}
	if revised := notes("criterion.fix", "--criterion", string(f.criterion)); strings.Contains(revised, "minted   criterion_id") {
		t.Errorf("an id a bind flag replaced is not minted:\n%s", revised)
	}
	if amend := notes("task.amend", "--from", string(f.task)); !strings.Contains(amend, "optional replacement.progress: absent; --set or --pin replacement.progress adds it") ||
		strings.Contains(amend, "optional replacement.accepter: delete") {
		t.Errorf("an absent optional key is noted as addable, and no delete note for a key not there:\n%s", amend)
	}
	for _, event := range templateEvents {
		out, _, _ := cliRun(t, f.root, nil, "agent", "template", string(event.EventType()))
		if m := regexp.MustCompile(`existing (0|[^;]*_)[;>]`).FindString(out); m != "" {
			t.Errorf("%s names an id's noun as %q", event.EventType(), m)
		}
	}
}
