package query

// Tests for the concise brief. Agreement between the brief and --json is
// defined here: every value the brief shows is a leaf of the same answer's
// JSON export at the path it was read from, and equal to it, or for a string
// shown truncated, a prefix of it marked with "…"; every count is the length
// of the array at its path; nothing is shown as absent. The brief is not
// lossless: --json is.

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
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

func viewAnswerOf(t *testing.T, p store.Project, r ViewRequest) ViewAnswer {
	t.Helper()
	a, err := ReadView(p, r)
	if err != nil {
		t.Fatalf("control %s view must answer: %v", r.View, err)
	}
	return a
}

// The brief is the views' default text; these ran over the old presets
// and now read the views that absorbed them (now and intake pending -> todo).
func TestBriefNamesStatusReasonAndNextActorPerRecord(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	appendEvents(t, p, 101, testTask(2), &model.BlockerHold{Task: testRef(2, 1), BlockerID: testID(80), Reason: model.BlockerResume,
		Actor: model.Actor{ID: "owner"}, Criterion: "resume is authorized"})
	todo := assertViewHonest(t, viewAnswerOf(t, p, ViewRequest{View: "todo"}))
	for _, want := range []string{
		"blocked: 1\n  TASK " + string(testID(2)) + " rev 1 BLOCKED next acceptance-owner\n    Build the first usable read slice of WhoSaidSo",
		"    blocked: resume waits on owner hold " + string(testID(80)) + " - resume is authorized\n",
		"ready: 1\n  TASK " + string(testID(1)) + " rev 1 READY next acceptance-owner\n",
		"attention: 1\n  task-blocked-owed " + string(testID(2)) + " rev 1 waits on owner\n",
	} {
		if !strings.Contains(todo, want) {
			t.Fatalf("todo brief must carry %q, got\n%s", want, todo)
		}
	}
	if strings.Contains(todo, `"Key"`) || strings.Contains(todo, "null") {
		t.Fatalf("the brief must not dump structure, got\n%s", todo)
	}
}

// DOGFOOD item 72: todo shows what intake owes. An unreviewed packet awaits
// review; a correction-requested one is listed with its review, and whether a
// corrected packet answered it is not recorded; a rejected one owes nothing, so it is only
// counted, and history still lists its review.
func TestBriefShowsReviewedIntakeWithItsDisposition(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	empty := assertViewHonest(t, viewAnswerOf(t, p, ViewRequest{View: "todo"}))
	if !strings.Contains(empty, "\nintake unreviewed: none\n") || !strings.Contains(empty, "\ncorrection requested (whether a corrected packet answered it is not recorded): none\n") || strings.Contains(empty, "rejected intake") {
		t.Fatalf("an empty owed section must say none, not vanish, and nothing rejected says nothing, got\n%s", empty)
	}
	rejected := capturePacket(t, p, 2)
	reviewPacket(t, p, 101, rejected, "rejected")
	reviewPacket(t, p, 102, capturePacket(t, p, 4), "correction-requested")
	capturePacket(t, p, 3)
	text := assertViewHonest(t, viewAnswerOf(t, p, ViewRequest{View: "todo"}))
	for _, want := range []string{"intake unreviewed 1 correction requested 1 rejected 1\n",
		"\nintake unreviewed: 1\n  packet " + string(testID(1003)) + " pending\n    author author events 1 task.create\n",
		"\ncorrection requested (whether a corrected packet answered it is not recorded): 1\n  packet " + string(testID(1004)) + " correction-requested\n",
		"    reviewed correction-requested by reviewer\n",
		"\nrejected intake: 1 (reviewed, nothing owed; whosaidso history lists the reviews)\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("intake brief must carry %q, got\n%s", want, text)
		}
	}
	if strings.Contains(text, string(testID(1002))) {
		t.Fatalf("a rejected packet owes nothing and must not be listed as owed:\n%s", text)
	}
	a := viewAnswerOf(t, p, ViewRequest{View: "todo"}).(*TodoAnswer)
	if a.Totals.IntakeUnreviewed != 1 || a.Totals.IntakeCorrectionRequested != 1 || a.Totals.IntakeRejected != 1 {
		t.Fatalf("totals must split intake by what the review says is owed: %+v", a.Totals)
	}
	history := viewAnswerOf(t, p, ViewRequest{View: "history"}).(*HistoryAnswer)
	found := false
	for _, r := range history.Reviews {
		found = found || r.Packet.CommandID == rejected.CommandID && r.Outcome == "rejected"
	}
	if !found {
		t.Fatalf("history must still list the rejected packet's review: %+v", history.Reviews)
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
	text := assertViewHonest(t, viewAnswerOf(t, p, ViewRequest{View: "show"}))
	if strings.Contains(text, "\x1b") || strings.Contains(text, "0009 rev 1") || !strings.Contains(text, "    fine…\n") ||
		!strings.Contains(text, `"clear\u001b[2J screen \"quoted\""`) || !strings.Contains(text, "    "+strings.Repeat("λ", briefWidth-1)+"…\n") || !strings.Contains(text, "    \"done…\"\n") {
		t.Fatalf("authored text must be cut, quoted and marked, got\n%s", text)
	}
}
