package main

// --set values are typed by the event's skeleton: a text field takes VALUE as
// text (decoded only as a JSON string literal), so a commit such as
// 79461799e564 or a numeric-looking name never becomes a JSON number; number,
// boolean, object and array fields take VALUE as JSON. What the gate or git
// then makes of the value is tested elsewhere.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSetValueIsTypedByTheSkeleton(t *testing.T) {
	f := boundWorld(t)
	data := boundPrint(t, f.root, "attempt.terminal",
		"--set", "delivery_refs[0].git.commit=79461799e564", // all digits and one e: a JSON number if parsed blindly
		"--set", "delivery_refs[0].git.path=1234", // a numeric-looking text field
		"--set", "reason=true",
		"--set", `next_action={"not":"an object"}`,
		"--set", `delivery_refs[0].content.media_type="12"`, // a JSON string literal is decoded
		"--set", "task.revision=2", // a number field
		"--set", "delivery_refs[0].content.length=17",
		"--set", "commits_denied=true", // a boolean field
		"--set", `delivery_refs[0].content.locators=[{"path":"a.json"}]`, // an array field
	)
	for path, want := range map[string]any{
		"delivery_refs[0].git.commit":               "79461799e564",
		"delivery_refs[0].git.path":                 "1234",
		"reason":                                    "true",
		"next_action":                               `{"not":"an object"}`,
		"delivery_refs[0].content.media_type":       "12",
		"task.revision":                             float64(2),
		"delivery_refs[0].content.length":           float64(17),
		"commits_denied":                            true,
		"delivery_refs[0].content.locators[0].path": "a.json",
	} {
		if got := boundAt(data, path); got != want {
			t.Errorf("%s: got %#v (%T), want %#v (%T)", path, got, got, want, want)
		}
	}
}

// A map key the skeleton cannot name is typed by the draft's node there.
func TestSetValueUnderAMapKeyIsTypedByTheDraft(t *testing.T) {
	f := boundWorld(t)
	data := boundPrint(t, f.root, "invocation.start",
		"--set", `envelope.config_requested={"label":{"type":"string","string":"x"},"threads":{"type":"number","number":1}}`,
		"--set", "envelope.config_requested.label.string=8e1",
		"--set", "envelope.config_requested.threads.number=8e1",
	)
	if got := boundAt(data, "envelope.config_requested.label.string"); got != "8e1" {
		t.Errorf("a text value under a map key: got %#v (%T)", got, got)
	}
	if got := boundAt(data, "envelope.config_requested.threads.number"); got != float64(80) {
		t.Errorf("a number under a map key: got %#v (%T)", got, got)
	}
}

// Number, boolean and integer leaves the skeleton holds as placeholders take
// VALUE as JSON; a text leaf beside them stays text.
func TestSetValueFillsTypedPlaceholders(t *testing.T) {
	f := boundWorld(t)
	fix := boundPrint(t, f.root, "criterion.fix", "--set", "expression.target.number=5e-2", "--set", "expression.empty_result=true", "--set", "expression.unit=1e3")
	seal := boundPrint(t, f.root, "invocation.seal", "--set", "envelope.outcome.value.exit_code=3")
	for path, c := range map[string]struct {
		data map[string]any
		want any
	}{
		"expression.target.number":         {fix, 0.05},
		"expression.empty_result":          {fix, true},
		"expression.unit":                  {fix, "1e3"},
		"envelope.outcome.value.exit_code": {seal, float64(3)},
	} {
		if got := boundAt(c.data, path); got != c.want {
			t.Errorf("%s: got %#v (%T), want %#v (%T)", path, got, got, c.want, c.want)
		}
	}
}

// --set on an artifact reference with anything but a whole JSON object is
// refused before capture, naming --pin, the only flag that builds one. The
// control: a whole reference object is still taken as JSON.
func TestSetOnAReferenceNamesPin(t *testing.T) {
	f := boundWorld(t)
	boundCapture(t, f.root, "blocker.hold", "--task", string(f.task), "--set", "reason=resume", "--set", `actor={"id":"lane"}`, "--set", "criterion=the fixture output exists")
	hold := string(openHold(t, boundSnapshot(t, f.root), f.task))
	pinned := boundPrint(t, f.root, "blocker.clear", "--hold", hold, "--pin", "resolving_witness=out/result.json")
	whole, err := json.Marshal(pinned["resolving_witness"])
	if err != nil {
		t.Fatal(err)
	}
	if got := boundPrint(t, f.root, "blocker.clear", "--hold", hold, "--set", "resolving_witness="+string(whole)); !reflect.DeepEqual(got["resolving_witness"], pinned["resolving_witness"]) {
		t.Errorf("a whole reference object given to --set must be kept: %#v", got["resolving_witness"])
	}
	for _, c := range []struct{ event, flag, path string }{
		{"blocker.clear", "--hold", "resolving_witness"},
		{"task.close", "--task", "delivery_witness_refs[0]"},
	} {
		id := hold
		if c.event == "task.close" {
			id = string(f.task)
		}
		for _, value := range []string{"out/result.json", `"out/result.json"`, "[]"} {
			_, errs, code := cliRun(t, f.root, nil, "lane", "template", c.event, c.flag, id, "--set", c.path+"="+value)
			if want := c.path + " is a reference: use --pin " + c.path + "=PATH"; code != 2 || !strings.Contains(errs, want) {
				t.Errorf("template %s --set %s=%s: want exit 2 saying %q, got %d %s", c.event, c.path, value, want, code, errs)
			}
		}
	}
}
