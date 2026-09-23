package query

// Brief polish found by dogfooding: continue prints each blocked reason once,
// labels each plan item and says when a READY item's latest attempt did not
// succeed; show says "none" for an empty summary kind. Every value printed
// still agrees with the JSON at its path.

import (
	"strings"
	"testing"
)

const polishWatermark = `"watermark": {"sequence": 7, "bundles": 7, "events": 9, "head": {"command_id": "H", "recorded_at": "T"}}`

func polishTask(id, status, intent, attempts, reasons string) string {
	return `{"ref": {"record_id": "` + id + `", "revision": 1}, "label": "` + intent + `",
	 "fact": {"kind": "TASK", "key": {"id": "` + id + `", "revision": 1}, "task": {"intent": "` + intent + `"}},
	 "author": {"actor": {"id": "a"}}, "admitted_by": {"id": "a"}, "self_admitted": true,
	 "task": {"status": "` + status + `", "expected_next_actor": {"id": "a"}, "attempts": ` + attempts + `, "reasons": ` + reasons + `}}`
}

const polishReasons = `[{"kind": "prerequisite", "detail": "prerequisite 0 (task-success) is FALSE: open", "actor": {"id": "a"}},
 {"kind": "prerequisite", "detail": "prerequisite 1 (task-success) is FALSE: open", "actor": {"id": "a"}}]`

func polishItem(id string) string {
	return `{"index": 0, "kind": "task-success", "target": {"record_id": "` + id + `", "revision": 1}, "satisfied": "FALSE", "waived": false, "status": "READY"}`
}

func polishContinue() string {
	stopped := `[{"terminal": {"outcome": "success"}}, {"terminal": {"outcome": "stopped"}}]`
	done := `[{"terminal": {"outcome": "stopped"}}, {"terminal": {"outcome": "success"}}]`
	live := `[{"terminal": {"outcome": "stopped"}}, {"terminal": null}]`
	return `{"view": "continue", "project": "p", "result": "KNOWN", ` + polishWatermark + `,
	 "record": {"record_id": "ROOT", "revision": 1},
	 "records": {"ROOT@1": ` + polishTask("ROOT", "BLOCKED", "the plan", "[]", polishReasons) + `,
	  "STOP@1": ` + polishTask("STOP", "READY", "stopped step", stopped, "[]") + `,
	  "LIVE@1": ` + polishTask("LIVE", "READY", "live step", live, "[]") + `,
	  "NONE@1": ` + polishTask("NONE", "READY", "fresh step", "[]", "[]") + `,
	  "DONE@1": ` + polishTask("DONE", "READY", "done step", done, "[]") + `},
	 "owed": {"status": "BLOCKED", "next_actor": {"id": "a"},
	  "reasons": [{"kind": "prerequisite", "detail": "prerequisite 0 (task-success) is FALSE: open", "waiting_actor": {"id": "a"}},
	   {"kind": "prerequisite", "detail": "prerequisite 1 (task-success) is FALSE: open", "waiting_actor": {"id": "a"}},
	   {"kind": "resume", "detail": "owed only here", "waiting_actor": {"id": "a"}}],
	  "items": [` + polishItem("STOP") + `, ` + polishItem("LIVE") + `, ` + polishItem("NONE") + `, ` + polishItem("DONE") + `]},
	 "closure": {"mandatory": [], "cycles": []}, "context": {"refs": [], "limit": {"offered": 0, "omitted": 0}},
	 "observed": {"head": {"state": "UNKNOWN", "reason": "r"}, "dirty": {"state": "UNKNOWN", "reason": "r"}, "observed_at": {"state": "UNKNOWN", "reason": "r"}},
	 "attention": []}`
}

func polishBrief(t *testing.T, exported string) string {
	t.Helper()
	text, facts, err := ViewBriefOf([]byte(exported))
	if err != nil {
		t.Fatal(err)
	}
	checkBriefFacts(t, "polish", []byte(exported), text, facts, 7)
	return text
}

func lineWith(text, needle string) string {
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, needle) {
			return l
		}
	}
	return ""
}

func TestContinueBriefPrintsEachBlockedReasonOnce(t *testing.T) {
	text := polishBrief(t, polishContinue())
	for _, detail := range []string{"prerequisite 0 (task-success) is FALSE: open", "prerequisite 1 (task-success) is FALSE: open"} {
		if n := strings.Count(text, detail); n != 1 {
			t.Fatalf("reason %q printed %d times; the record's blocked: line is its one place:\n%s", detail, n, text)
		}
	}
	// Control: an owed reason the record does not already print still shows.
	if l := lineWith(text, "owed only here"); !strings.Contains(l, "waits: resume") {
		t.Fatalf("an owed reason not listed under the record must still print:\n%s", text)
	}
}

func TestContinueBriefLabelsItemsAndNamesAStoppedLastAttempt(t *testing.T) {
	text := polishBrief(t, polishContinue())
	for _, label := range []string{"stopped step", "live step", "fresh step", "done step"} {
		if strings.Count(text, label) != 1 {
			t.Fatalf("item label %q must print once beside its item:\n%s", label, text)
		}
	}
	if l := lineWith(text, "item STOP"); !strings.Contains(l, "status READY - last attempt stopped") {
		t.Fatalf("a READY item whose latest attempt stopped must say so, got %q", l)
	}
	for _, id := range []string{"item LIVE", "item NONE", "item DONE"} {
		if l := lineWith(text, id); l == "" || strings.Contains(l, "last attempt") {
			t.Fatalf("%s has no latest attempt that ended other than success and must not be annotated, got %q", id, l)
		}
	}
}

func TestShowBriefSaysNoneForAnEmptySummaryKind(t *testing.T) {
	exported := `{"view": "show", "project": "p", "result": "KNOWN", ` + polishWatermark + `,
	 "summary": {"kind": "all", "records": 1, "attention": 0, "tasks": {"READY": 1}, "decisions": {}},
	 "attention": [], "records": []}`
	text := polishBrief(t, exported)
	if !strings.Contains(text, "  decisions: none\n") || strings.Contains(text, "decisions:\n") {
		t.Fatalf("an empty decisions summary must read \"decisions: none\":\n%s", text)
	}
	if !strings.Contains(text, "tasks: READY 1") {
		t.Fatalf("control: a non-empty summary kind still lists its counts:\n%s", text)
	}
}
