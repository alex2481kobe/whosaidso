package query

// todo names who holds each in-flight task, when it was claimed and last
// worked on, and flags a claim idle past the project's stale_after as a
// takeover candidate, judged only against the time the caller supplies.

import (
	"strings"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestTodoBoardNamesHoldersAndFlagsStaleClaims(t *testing.T) {
	p := testProject(t)
	appendEvents(t, p, 100, testTask(1))
	claimed := appendEvents(t, p, 101, startBy("alice", 500)).RecordedAt.UTC()
	board := func(now time.Time) *TodoAnswer {
		t.Helper()
		return view_(t, p, ViewRequest{View: "todo", Now: now}).(*TodoAnswer)
	}
	staleNotes := func(a *TodoAnswer) int {
		n := 0
		for _, note := range a.Attention {
			if note.Kind == "stale-claim" {
				n++
			}
		}
		return n
	}
	a := board(time.Time{})
	if len(a.InFlight) != 1 || len(a.InFlight[0].Claims) != 1 {
		t.Fatalf("an in-flight task lists its claim: %+v", a.InFlight)
	}
	c := a.InFlight[0].Claims[0]
	if c.Attempt != testID(500) || !c.ClaimedAt.Equal(claimed) || !c.LastActivityAt.Equal(claimed) || c.Stale != nil || staleNotes(a) != 0 {
		t.Fatalf("the claim names its attempt and times, and without a clock judges nothing: %+v", c)
	}
	if a := board(claimed.Add(time.Hour)); *a.InFlight[0].Claims[0].Stale || staleNotes(a) != 0 || a.Totals.StaleClaims != 0 {
		t.Fatalf("an hour idle is not stale under the default 24h: %+v", a.InFlight[0].Claims[0])
	}
	late := board(claimed.Add(25 * time.Hour))
	if !*late.InFlight[0].Claims[0].Stale || staleNotes(late) != 1 || late.Totals.StaleClaims != 1 {
		t.Fatalf("25h idle is stale, flagged in attention and counted: %+v %+v", late.InFlight[0].Claims[0], late.Totals)
	}
	for _, note := range late.Attention {
		if note.Kind == "stale-claim" && (!strings.Contains(note.Reason, "alice") || !strings.Contains(note.Reason, "owner's approval")) {
			t.Fatalf("the flag names the holder and that release needs the owner's approval: %s", note.Reason)
		}
	}
	p.Policy.StaleAfter = 48 * time.Hour
	if a := board(claimed.Add(25 * time.Hour)); *a.InFlight[0].Claims[0].Stale {
		t.Fatal("the project's stale_after sets the threshold")
	}
	// A hold names the task, and anyone may record one: it is not the
	// holder's activity. Runs are (internal/acceptance/claim_status_test.go).
	appendEvents(t, p, 102, &model.BlockerHold{Task: testRef(1, 1), BlockerID: testID(700), Reason: "resume",
		Actor: model.Actor{ID: "alice"}, Criterion: "back tomorrow"})
	if c := board(time.Time{}).InFlight[0].Claims[0]; !c.LastActivityAt.Equal(claimed) {
		t.Fatalf("a hold does not move the claim's last activity: %+v", c)
	}
	if _, err := ReadView(p, ViewRequest{View: "show", Now: claimed}); err == nil {
		t.Fatal("the reading time belongs only to todo")
	}
}
