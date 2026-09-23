package query

// The stale-claims seam of `state`: the check is asked only when the caller
// supplies it, it is handed the very snapshot the answer reads, its section
// renders in the brief exactly as in --json, and no other read accepts it.

import (
	"strings"
	"testing"

	"datum/internal/model"
	"datum/internal/reduce"
)

func TestStateStaleSectionReadsTheAnsweredSnapshot(t *testing.T) {
	p := testProject(t)
	presetWorld(t, p)
	plain := presetAnswer(t, p, Request{Command: "state"})
	if plain.Preset.Stale != nil {
		t.Fatal("control: state without the check carries no stale section")
	}
	var seen reduce.Snapshot
	head := model.GitHead{ObjectFormat: "sha1", Commit: strings.Repeat("a", 40)}
	check := func(s reduce.Snapshot) []StaleClaim {
		seen = s
		return []StaleClaim{
			{Claim: testRef(21, 1), LastRun: model.InvocationRef{Project: projectID, InvocationID: testID(50)}, Stale: reduce.TruthTrue,
				RunHead: &head, CurrentHead: &head, ChangedPaths: []string{"internal/query/query.go"}},
			{Claim: testRef(22, 1), LastRun: model.InvocationRef{Project: projectID, InvocationID: testID(51)}, Stale: reduce.TruthUnknown,
				ChangedPaths: []string{}, Reason: "the last run's commit is not known clean"},
		}
	}
	a := presetAnswer(t, p, Request{Command: "state", Stale: check})
	if seen.Watermark().Sequence != a.Watermark.Sequence || a.Preset.Stale == nil || len(*a.Preset.Stale) != 2 {
		t.Fatalf("the check must read the answered snapshot and fill the section: watermark %+v vs %+v, %+v", seen.Watermark(), a.Watermark, a.Preset.Stale)
	}
	text := assertBriefAgrees(t, a)
	for _, want := range []string{
		"stale claims: 2\n  CLAIM " + string(testID(21)) + " rev 1 stale TRUE last run " + string(testID(50)) + " changed paths 1\n",
		"  CLAIM " + string(testID(22)) + " rev 1 stale UNKNOWN last run " + string(testID(51)) + " changed paths 0\n    the last run's commit is not known clean\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the brief must carry %q, got\n%s", want, text)
		}
	}
	for _, command := range []string{"now", "todo", "instruments", "show"} {
		if _, err := Read(p, Request{Command: command, Stale: check}); err == nil {
			t.Errorf("%s accepted the stale check; it belongs to state alone", command)
		}
	}
}
