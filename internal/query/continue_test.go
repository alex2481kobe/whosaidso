package query

// continue tests: the brief composes NOW, STATE and one task's closure,
// attempts and runs with the caller's observation, and writes nothing.

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"datum/internal/model"
)

func TestContinueComposesTheBriefAndWritesNothing(t *testing.T) {
	p := testProject(t)
	presetWorld(t, p)
	before := treeBytes(t, p.Root)
	a := presetAnswer(t, p, Request{Command: "continue", ID: testID(1)})
	assertHonestRendering(t, a)
	if !reflect.DeepEqual(before, treeBytes(t, p.Root)) {
		t.Fatal("continue wrote to the project; the brief is a disposable export and no handoff record exists")
	}
	c := a.Preset.Continue
	for name, got := range map[string]model.AvailabilityState{"head": c.Observed.Head.State, "dirty": c.Observed.Dirty.State, "time": c.Observed.ObservedAt.State} {
		if got != model.Unknown {
			t.Fatalf("an unsupplied %s observation must be UNKNOWN, got %s", name, got)
		}
	}
	if c.Now == nil || c.State == nil || len(*c.Now.InFlight) != 1 || len(*c.State.Claims) != 3 || len(c.Runs) != 3 || len(c.Attempts) != 1 {
		t.Fatalf("continue must carry NOW, STATE, this task's runs and attempts, got %+v", c)
	}
	if _, ok := c.Attempts[0].NextAction.(Unknown); !ok || !c.Attempts[0].Live || !strings.HasPrefix(c.Handoff, "none") {
		t.Fatalf("a live attempt has no recorded next action yet: that is UNKNOWN, got %+v", c.Attempts[0])
	}
	if _, ok := c.Progress.(Unknown); !ok {
		t.Fatalf("absent progress must be UNKNOWN, got %+v", c.Progress)
	}
	appendEvents(t, p, 106, &model.AttemptTerminal{Task: testRef(1, 1), AttemptID: testID(70), Outcome: model.AttemptNoReading,
		Reason: "instrument unvalidated", NextAction: "validate instrument 11", DeliveryRefs: []model.ArtifactRef{testArtifact()}})
	at := presetStart.Add(3 * time.Hour)
	dirty := true
	head := model.GitHead{ObjectFormat: "sha1", Commit: strings.Repeat("b", 40)}
	observed := Observation{Head: known(head), Dirty: known(dirty), ObservedAt: known(at)}
	a = presetAnswer(t, p, Request{Command: "continue", ID: testID(1), Observed: &observed})
	assertHonestRendering(t, a)
	c = a.Preset.Continue
	got := c.Attempts[0]
	if got.NextAction != "validate instrument 11" || len(got.DeliveryRefs) != 1 || got.Live || got.Outcome != model.AttemptNoReading {
		t.Fatalf("continue must carry the terminal next action and delivery refs, got %+v", got)
	}
	if *c.Observed.Head.Value != head || !*c.Observed.Dirty.Value || !c.Observed.ObservedAt.Value.Equal(at) {
		t.Fatalf("continue must carry the caller's observation verbatim, got %+v", c.Observed)
	}
}
