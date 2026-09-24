package main

// Tests that a template placeholder is refused at every capture entrance, not
// only template --capture (the decoder owns the rule: model.IsPlaceholder),
// that authored text which merely resembles one is accepted, and that
// revising from a template never carries an old judgment across: --from
// leaves an instrument's validation a placeholder to judge again.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
)

const textPlaceholder = "<text: authored words, not blank>"

// boundValidation is a KNOWN validation citing the fixture's validation file,
// as an author re-judging the revised instrument would write it.
func boundValidation(t *testing.T) string {
	t.Helper()
	v := e2eKnown(model.InstrumentValidation{Ref: e2ePin(`{"validated":"against a known pose sweep"}`, "validation/measure.json", "application/json"), Version: "v2"})
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// jsonString is s as a JSON string with "<" and ">" left as written.
func jsonString(s string) string {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

func intakeCount(t *testing.T, root string) int {
	t.Helper()
	project, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	packets, err := store.ReadVerifiedIntake(project, nil)
	if err != nil {
		t.Fatal(err)
	}
	return len(packets)
}

// A blocker.hold whose criterion is the given string, written to a file.
func holdWithCriterion(t *testing.T, f boundFixture, criterion string) string {
	t.Helper()
	printed, errs, code := cliRun(t, f.root, nil, "agent", "template", "blocker.hold", "--task", string(f.task), "--set", "reason=resume", "--set", `actor={"id":"agent"}`)
	if code != 0 {
		t.Fatalf("template: %d %s", code, errs)
	}
	quoted, replacement := jsonString(textPlaceholder), jsonString(criterion)
	if strings.Count(printed, quoted) != 1 {
		t.Fatalf("the fixture must leave exactly the criterion placeholder: %s", printed)
	}
	path := filepath.Join(t.TempDir(), "hold.json")
	if err := os.WriteFile(path, []byte(strings.Replace(printed, quoted, replacement, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPlaceholderRefusedAtEveryCaptureEntrance(t *testing.T) {
	f := boundWorld(t)
	// Controls first: authored text, including text that holds angle brackets
	// or a placeholder's near miss, captures and would admit.
	for _, authored := range []string{
		"the fixture output exists",
		"<text:no space after the colon>",
		"<Text: the kind is case-sensitive>",
		"<note: not a kind the template writes>",
		"the <text: hint> sits inside a sentence",
		"<text: hint> with words after it",
		" <text: leading space>",
		"a < b and c > d",
		"<>",
	} {
		file := holdWithCriterion(t, f, authored)
		if out, errs, code := cliRun(t, f.root, nil, "agent", "check", "admission", "--events", file); code != 0 || !strings.Contains(out, "\nresult: would-admit\n") {
			t.Errorf("authored %q must would-admit: %d %s %s", authored, code, out, errs)
		}
		if out, errs, code := cliRun(t, f.root, nil, "agent", "capture", "--events", file); code != 0 {
			t.Errorf("authored %q must capture: %d %s %s", authored, code, out, errs)
		}
	}
	before := intakeCount(t, f.root)
	for _, placeholder := range []string{textPlaceholder, "<text>", "<path: project-relative, forward slashes>"} {
		file := holdWithCriterion(t, f, placeholder)
		out, errs, code := cliRun(t, f.root, nil, "agent", "capture", "--events", file)
		if code != 1 || out != "" || !strings.Contains(errs, "event.data.criterion") || !strings.Contains(errs, "placeholder") {
			t.Errorf("plain capture must refuse the placeholder %q at criterion: %d %q %q", placeholder, code, out, errs)
		}
		out, errs, code = cliRun(t, f.root, nil, "agent", "check", "admission", "--events", file)
		if code != 1 || strings.Contains(out, "would-admit") || !strings.Contains(errs, "event.data.criterion") {
			t.Errorf("check admission must report the placeholder %q refused, not would-admit: %d %q %q", placeholder, code, out, errs)
		}
	}
	// A handback builds its receipt in process (internal/write): the same
	// decoder refuses a placeholder given as its reason.
	out, errs, code := cliRun(t, f.root, nil, "agent", "handback", "--attempt-id", string(f.attempt), "--outcome", "stopped", "--reason", textPlaceholder, "--next-action", "fill the reason")
	if code != 1 || !strings.Contains(errs, "placeholder") {
		t.Errorf("handback must refuse a placeholder reason: %d %q %q", code, out, errs)
	}
	if after := intakeCount(t, f.root); after != before {
		t.Fatalf("a refused placeholder reached intake: %d -> %d packets", before, after)
	}
}

// --from copies the author's description and leaves the old validation, a
// verdict on the replaced implementation, as placeholders; capture refuses
// until the author judges it again.
func TestReviseFromLeavesValidationToJudgeAgain(t *testing.T) {
	f := boundWorld(t)
	args := []string{"template", "instrument.revise", "--from", string(f.instrument), "--set", "provenance.source_refs=[]"}
	out, errs, code := cliRun(t, f.root, nil, "agent", args...)
	if code != 0 {
		t.Fatalf("template: %d %s", code, errs)
	}
	var tree []map[string]any
	if err := json.Unmarshal([]byte(out), &tree); err != nil {
		t.Fatal(err)
	}
	data := tree[0]["data"].(map[string]any)
	// Control: the author's own wording and the pinned implementation are copied.
	if boundAt(data, "replacement.blind_to") != "unmeasured poses" || boundAt(data, "replacement.question_answered") != "pose penetration depth" ||
		boundAt(data, "replacement.implementation_ref.content.locators[0].path") != "tools/measure.sh" {
		t.Fatalf("--from must copy the author's description and implementation: %v", data["replacement"])
	}
	for _, path := range []string{"replacement.validation.state", "replacement.validation.value.version", "replacement.validation.value.ref.kind"} {
		if v, ok := boundAt(data, path).(string); !ok || !isPlaceholder(v) {
			t.Errorf("--from carried the old validation into %s: %v", path, boundAt(data, path))
		}
	}
	if !strings.Contains(errs, "replacement.validation: NOT copied") {
		t.Errorf("the notes must say validation was not copied: %s", errs)
	}
	before := intakeCount(t, f.root)
	if _, errs, code := cliRun(t, f.root, nil, "agent", append(args, "--capture")...); code != 1 || !strings.Contains(errs, "replacement.validation.state") {
		t.Fatalf("capture must refuse the unjudged validation: %d %s", code, errs)
	}
	if after := intakeCount(t, f.root); after != before {
		t.Fatalf("a refused capture wrote intake: %d -> %d", before, after)
	}
	boundCapture(t, f.root, append(args[1:], "--set", "replacement.validation="+boundValidation(t))...)
	rec, _ := boundSnapshot(t, f.root).Current(reduce.Ident{Project: "test/cli", ID: f.instrument})
	if rec.Key.Revision != 2 || rec.Instrument.Validation.Value == nil || rec.Instrument.Validation.Value.Version != "v2" || rec.Instrument.BlindTo != "unmeasured poses" {
		t.Fatalf("the re-judged revision must admit with the author's new validation: %+v", rec.Instrument)
	}
}
