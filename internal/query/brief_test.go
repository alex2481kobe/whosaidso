package query

// Tests for the concise brief. Agreement between the brief and --json is
// defined here: every value the brief shows is a leaf of the same answer's
// JSON export at the path it was read from, and equal to it, or for a string
// shown truncated, a prefix of it marked with "…"; every count is the length
// of the array at its path; nothing is shown as absent. The brief is not
// lossless: --json and the default outline are.

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"datum/internal/model"
)

func jsonAt(t *testing.T, root any, path []string) (any, bool) {
	t.Helper()
	v := root
	for _, step := range path {
		if strings.HasPrefix(step, "[") {
			i, err := strconv.Atoi(strings.Trim(step, "[]"))
			xs, ok := v.([]any)
			if err != nil || !ok || i >= len(xs) {
				return nil, false
			}
			v = xs[i]
			continue
		}
		var key string
		if err := json.Unmarshal([]byte(step), &key); err != nil {
			t.Fatalf("brief path step %q is not a quoted key", step)
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = m[key]; !ok {
			return nil, false
		}
	}
	return v, true
}

// assertBriefAgrees checks the agreement rule above and returns the text.
func assertBriefAgrees(t *testing.T, a Answer) string {
	t.Helper()
	var exported, text, again bytes.Buffer
	if err := RenderJSON(&exported, a); err != nil {
		t.Fatal(err)
	}
	if err := RenderBrief(&text, a); err != nil {
		t.Fatal(err)
	}
	if err := RenderBrief(&again, a); err != nil || again.String() != text.String() {
		t.Fatalf("%s brief must render deterministically", a.Command)
	}
	b, err := brief(a)
	if err != nil {
		t.Fatal(err)
	}
	checkBriefFacts(t, a.Command, exported.Bytes(), text.String(), b.facts, a.Watermark.Sequence)
	return text.String()
}

// checkBriefFacts is the agreement rule above over one export and its brief.
func checkBriefFacts(t *testing.T, name string, exported []byte, text string, facts []BriefFact, sequence uint64) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(exported))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		t.Fatal(err)
	}
	if len(facts) == 0 || strings.Contains(text, "UNKNOWN(absent)") {
		t.Fatalf("%s brief shows no facts or reads a path the JSON lacks:\n%s", name, text)
	}
	for _, f := range facts {
		where := strings.Join(f.Path, "/")
		v, ok := jsonAt(t, root, f.Path)
		if !ok {
			t.Fatalf("%s brief shows %s from %s, which the JSON does not have", name, f.Display, where)
		}
		if f.Count {
			if xs, isList := v.([]any); !isList || strconv.Itoa(len(xs)) != f.Display {
				t.Fatalf("%s brief counts %s at %s, JSON has %v", name, f.Display, where, v)
			}
			continue
		}
		leaf, _ := json.Marshal(v)
		s, isString := v.(string)
		shown := f.Display
		if isString && strings.HasPrefix(shown, `"`) {
			unquoted := strings.TrimSuffix(shown, "…")
			if err := json.Unmarshal([]byte(unquoted), &shown); err != nil {
				t.Fatalf("%s brief quoted %q at %s undecodably", name, f.Display, where)
			}
			if f.Prefix {
				shown += "…"
			}
		}
		switch {
		case f.Prefix && (!isString || !strings.HasSuffix(shown, "…") || !strings.HasPrefix(s, strings.TrimSuffix(shown, "…")) || s == strings.TrimSuffix(shown, "…")):
			t.Fatalf("%s brief shows %q at %s as a cut of %s, but it is not a strict prefix", name, f.Display, where, leaf)
		case !f.Prefix && isString && shown != s, !f.Prefix && !isString && shown != string(leaf):
			t.Fatalf("%s brief shows %q at %s, JSON has %s", name, f.Display, where, leaf)
		}
		if !strings.Contains(text, f.Display) {
			t.Fatalf("%s brief recorded %q but did not print it", name, f.Display)
		}
	}
	if !strings.Contains(strings.SplitN(text, "\n", 2)[0], "watermark sequence "+strconv.FormatUint(sequence, 10)) {
		t.Fatalf("%s brief must open with its watermark, got %q", name, text)
	}
}

func TestBriefAgreesWithJSONOnEveryPreset(t *testing.T) {
	p := testProject(t)
	presetWorld(t, p)
	requests := []Request{{Command: "show"}, {Command: "history"}, {Command: "now"}, {Command: "todo"}, {Command: "state"},
		{Command: "instruments"}, {Command: "context"}, {Command: "context", ID: testID(20)}, {Command: "show", ID: testID(999)}}
	at := time.Now().UTC()
	requests = append(requests, Request{Command: "continue", ID: testID(1), Observed: &Observation{
		ObservedAt: model.Availability[time.Time]{State: model.Known, Value: &at},
		Head:       notKnown[model.GitHead]("not a git checkout"), Dirty: notKnown[bool]("not a git checkout")}})
	for _, r := range requests {
		a, err := Read(p, r)
		if err != nil {
			t.Fatalf("control %s read must succeed: %v", r.Command, err)
		}
		text := assertBriefAgrees(t, a)
		t.Logf("%s", text)
		if a.Result == "UNKNOWN" && !strings.Contains(text, "\nreason: no admitted record "+string(testID(999))) {
			t.Fatalf("an UNKNOWN answer's brief must say why, got\n%s", text)
		}
	}
}

func TestBriefNamesStatusReasonAndNextActorPerRecord(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	appendEvents(t, p, 101, testTask(2), &model.BlockerHold{Task: testRef(2, 1), BlockerID: testID(80), Reason: model.BlockerResume,
		Actor: model.Actor{ID: "owner"}, Criterion: "resume is authorized"})
	todo := assertBriefAgrees(t, presetAnswer(t, p, Request{Command: "todo"}))
	for _, want := range []string{
		"blocked: 1\n  TASK " + string(testID(2)) + " rev 1 BLOCKED next acceptance-owner\n    Build U09 of Datum",
		"    blocked: resume waits on owner hold " + string(testID(80)) + " - resume is authorized\n",
		"ready: 1\n  TASK " + string(testID(1)) + " rev 1 READY next acceptance-owner\n",
	} {
		if !strings.Contains(todo, want) {
			t.Fatalf("todo brief must carry %q, got\n%s", want, todo)
		}
	}
	now := assertBriefAgrees(t, presetAnswer(t, p, Request{Command: "now"}))
	if !strings.Contains(now, "attention: 1\n  task-blocked-owed "+string(testID(2))+" rev 1 waits on owner\n") {
		t.Fatalf("now brief must name the owed hold and who it waits on, got\n%s", now)
	}
	if strings.Contains(todo+now, `"Key"`) || strings.Contains(todo+now, "null") {
		t.Fatalf("the brief must not dump structure, got\n%s%s", todo, now)
	}
}

func TestBriefShowsReviewedIntakeWithItsDisposition(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	if empty := assertBriefAgrees(t, readAnswer(t, p, "intake pending", "")); !strings.Contains(empty, "\nintake: none\n") {
		t.Fatalf("the list a command is about must say none, not vanish, got\n%s", empty)
	}
	reviewPacket(t, p, 101, capturePacket(t, p, 2), "rejected")
	capturePacket(t, p, 3)
	text := assertBriefAgrees(t, readAnswer(t, p, "intake pending", ""))
	for _, want := range []string{"packet " + string(testID(1002)) + " rejected\n", "    reviewed rejected by reviewer\n",
		"packet " + string(testID(1003)) + " pending\n    author author events 1 task.create\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("intake brief must carry %q, got\n%s", want, text)
		}
	}
}

// Authored text is cut at its first line and quoted when it holds anything a
// terminal could act on, so it can never print a line of its own. An authored
// … is quoted too, so a bare trailing … always means the brief cut the text.
func TestBriefCannotBeForgedByAuthoredText(t *testing.T) {
	p := testProject(t)
	forged := testTask(1)
	forged.Spec.Intent = "fine\n  TASK 00000000000000000000000009 rev 1 READY next owner"
	controls := testTask(2)
	controls.Spec.Intent = "clear\x1b[2J screen \"quoted\""
	long := testTask(3)
	long.Spec.Intent = strings.Repeat("λ", briefWidth-1) + " and more"
	ellipsis := testTask(4)
	ellipsis.Spec.Intent = "done…"
	appendEvents(t, p, 100, forged, controls, long, ellipsis)
	text := assertBriefAgrees(t, readAnswer(t, p, "show", ""))
	if strings.Contains(text, "\x1b") || strings.Contains(text, "0009 rev 1") || !strings.Contains(text, "    fine…\n") ||
		!strings.Contains(text, `"clear\u001b[2J screen \"quoted\""`) || !strings.Contains(text, "    "+strings.Repeat("λ", briefWidth-1)+"…\n") || !strings.Contains(text, "    \"done…\"\n") {
		t.Fatalf("authored text must be cut, quoted and marked, got\n%s", text)
	}
}
