package main

// Tests for what a capture reports around its packet (capture_report.go) and
// for --set null on a required key: every template placeholder is named in one
// refusal rather than one per attempt, a placeholder inside an optional key
// says the key can be deleted, the ids a packet creates are in the --json
// answers, and a null on a required list points at [].

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// A packet holding many placeholders is refused with every one named at once,
// the ones inside an optional key marked deletable, and nothing reaches intake.
func TestCaptureNamesEveryPlaceholderAtOnce(t *testing.T) {
	f := boundWorld(t)
	if out, errs, code := cliRun(t, f.root, nil, "agent", "capture", "--events", holdWithCriterion(t, f, "the owner answers")); code != 0 {
		t.Fatalf("control: an authored hold must capture: %d %s %s", code, out, errs)
	}
	printed, _, code := cliRun(t, f.root, nil, "agent", "template", "task.create")
	var events []model.Event
	if code != 0 || json.Unmarshal([]byte(printed), &events) != nil || len(events) != 1 {
		t.Fatalf("control: task.create must print one event: %d %s", code, printed)
	}
	want := model.Placeholders(events[0].Data)
	if len(want) < 2 {
		t.Fatalf("control: the printed template must hold several placeholders, got %v", want)
	}
	file := filepath.Join(t.TempDir(), "task.json")
	if err := os.WriteFile(file, []byte(printed), 0o644); err != nil {
		t.Fatal(err)
	}
	before := intakeCount(t, f.root)
	out, errs, code := cliRun(t, f.root, nil, "agent", "capture", "--events", file)
	if code != 1 || out != "" || !strings.Contains(errs, fmt.Sprintf("capture refused: %d template placeholder(s) still unfilled:\n", len(want))) {
		t.Fatalf("capture must refuse naming all %d placeholders: %d %q %s", len(want), code, out, errs)
	}
	for _, path := range want {
		if !strings.Contains(errs, "\n  "+path) {
			t.Errorf("the refusal must name %s", path)
		}
	}
	if !strings.Contains(errs, "\n  event.data.spec.accepter.id  (inside optional spec.accepter: delete it to leave it out)\n") {
		t.Errorf("a placeholder inside the optional accepter must say the key can be deleted: %s", errs)
	}
	// Nested optional keys name the outermost: deleting it leaves the whole block out.
	if !strings.Contains(errs, "\n  event.data.spec.progress.summary  (inside optional spec.progress: delete it to leave it out)\n") {
		t.Errorf("a placeholder under nested optional keys must name the outermost, spec.progress: %s", errs)
	}
	if strings.Contains(errs, "event.data.spec.intent  (inside optional") {
		t.Errorf("a required field must not be marked optional: %s", errs)
	}
	if got := intakeCount(t, f.root); got != before {
		t.Fatalf("a refused capture must write nothing to intake: %d packets, want %d", got, before)
	}
}

// capture --json carries the ids the packet creates, with and without
// --admit, and prints no notes beside the one answer.
func TestCaptureJSONListsTheIDsItCreates(t *testing.T) {
	wantCreated := []createdRecord{
		{Event: 0, Type: "task.create", Path: "id", ID: string(cliID(1))},
		{Event: 0, Type: "task.create", Path: "spec.acceptance_criteria[0].id", ID: string(cliID(2))},
	}
	root, data := cliFixture(t)
	out, errs, code := cliRun(t, root, data, "agent", "capture", "--json")
	var plain captureAnswer
	if code != 0 || errs != "" || json.Unmarshal([]byte(out), &plain) != nil {
		t.Fatalf("capture --json must print one JSON answer and no notes: %d %q %q", code, out, errs)
	}
	if fmt.Sprint(plain.Created) != fmt.Sprint(wantCreated) || plain.CommandID == "" {
		t.Fatalf("capture --json created = %+v, want %+v", plain.Created, wantCreated)
	}
	root, data = cliFixture(t)
	out, errs, code = cliRun(t, root, data, "agent", "capture", "--json", "--admit", "--reason", "checked")
	var admitted struct {
		Capture struct {
			Created []createdRecord `json:"created"`
		} `json:"capture"`
	}
	if code != 0 || errs != "" || json.Unmarshal([]byte(out), &admitted) != nil {
		t.Fatalf("capture --json --admit must print one JSON answer and no notes: %d %q %q", code, out, errs)
	}
	if fmt.Sprint(admitted.Capture.Created) != fmt.Sprint(wantCreated) {
		t.Fatalf("capture --json --admit created = %+v, want %+v", admitted.Capture.Created, wantCreated)
	}
}

// template --capture --json carries the minted ids in its answer; --json
// without --capture is refused, since a printed template is already JSON.
func TestTemplateCaptureJSONListsTheIDsItCreates(t *testing.T) {
	f := boundWorld(t)
	args := []string{"template", "task.create", "--set", "provenance.source_refs=[]", "--set", "spec.intent=measure", "--set", "spec.subject=pose sweep",
		"--set", "spec.scope.source_paths=[]", "--set", "spec.scope.context_refs=[]", "--set", "spec.scope.applies_when=this fixture",
		"--set", "spec.scope.limitations=none", "--set", `spec.non_goals=["production writes"]`, "--set", "spec.context_refs=[]",
		"--set", "spec.constraint_refs=[]", "--set", "spec.prerequisites=[]", "--set", "spec.next_actor.id=agent",
		"--set", "spec.acceptance_criteria[0].criterion=first property"}
	if _, errs, code := cliRun(t, f.root, nil, "agent", append(args, "--json")...); code != 2 || !strings.Contains(errs, "--json needs --capture") {
		t.Fatalf("--json without --capture must be refused: %d %s", code, errs)
	}
	out, errs, code := cliRun(t, f.root, nil, "agent", append(args, "--capture", "--admit", "--reason", "checked", "--json")...)
	var answer struct {
		Capture struct {
			Created []createdRecord `json:"created"`
		} `json:"capture"`
	}
	if code != 0 || strings.Contains(errs, "minted   ") || json.Unmarshal([]byte(out), &answer) != nil {
		t.Fatalf("template --capture --json must answer in JSON without minted notes: %d %q %q", code, out, errs)
	}
	got := answer.Capture.Created
	if len(got) != 2 || got[0].Path != "id" || got[0].Type != "task.create" || !model.ValidID(model.ID(got[0].ID)) ||
		got[1].Path != "spec.acceptance_criteria[0].id" || !model.ValidID(model.ID(got[1].ID)) {
		t.Fatalf("the answer must list the minted task and criterion ids: %+v", got)
	}
}

// --set PATH=null on a required key is refused with what to do instead: a list
// is emptied with [], quoted for zsh; an object's fields are filled; text is
// filled. An optional key is still omitted.
func TestSetNullOnARequiredKeySaysWhatToDoInstead(t *testing.T) {
	f := boundWorld(t)
	data := boundPrint(t, f.root, "task.create", "--set", "spec.accepter=null")
	if _, ok := data["spec"].(map[string]any)["accepter"]; ok {
		t.Fatalf("control: null on the optional accepter must omit it: %v", data["spec"])
	}
	for path, want := range map[string]string{
		"spec.context_refs": "--set 'spec.context_refs=[]'",
		"spec.scope":        "fill its fields",
		"spec.intent":       "is required: fill it",
	} {
		_, errs, code := cliRun(t, f.root, nil, "agent", "template", "task.create", "--set", path+"=null")
		if code != 2 || !strings.Contains(errs, "omits only an optional key") || !strings.Contains(errs, want) {
			t.Errorf("--set %s=null must be refused saying %q: %d %s", path, want, code, errs)
		}
	}
}
