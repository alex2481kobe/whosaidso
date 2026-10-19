// Actor views select admitted work, not prose or historical holders. These
// tests use synthetic ledgers; CLI/config integration lives with the CLI.
package query

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func teamWorld(t *testing.T) store.Project {
	t.Helper()
	p := testProject(t)
	t.Setenv("WHOSAIDSO_HOME", filepath.Join(t.TempDir(), "home"))
	if err := os.WriteFile(filepath.Join(p.Root, "whosaidso.toml"), []byte("id = 'example/query-tests'\nledger = '.whosaidso/events'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 6; n++ {
		task := testTask(n)
		task.Spec.NextActor = model.Actor{ID: "worker-a"}
		if n == 5 {
			task.Spec.NextActor = model.Actor{ID: "worker-c"}
		}
		if n == 6 {
			task.Spec.NextActor = model.Actor{UnknownReason: "unassigned"}
		}
		appendEvents(t, p, 100+n, task)
	}
	appendEvents(t, p, 110,
		&model.TaskStart{Task: testRef(1, 1), AttemptID: testID(70), Actor: model.Actor{ID: "worker-b"}},
		&model.TaskStart{Task: testRef(4, 1), AttemptID: testID(71), Actor: model.Actor{ID: "worker-b"}},
		&model.BlockerHold{Task: testRef(3, 1), BlockerID: testID(80), Reason: model.BlockerResume,
			Actor: model.Actor{ID: "gatekeeper"}, Criterion: "permission to resume"})
	appendEvents(t, p, 111, admittedAs(111, map[int]string{0: "worker-b"},
		&model.AttemptTerminal{Task: testRef(4, 1), AttemptID: testID(71), Outcome: model.AttemptSuccess,
			Reason: "done", NextAction: "accept delivery", DeliveryRefs: []model.ArtifactRef{testArtifact()}})...)
	return p
}

func TestTodoActorEverySectionAndLiveHolder(t *testing.T) {
	p := teamWorld(t)
	a := view_(t, p, ViewRequest{View: "todo", Actor: "worker-a"}).(*TodoAnswer)
	for i, section := range [][]TodoTask{a.InFlight, a.Ready, a.Blocked, a.AwaitingAcceptance} {
		if len(section) != 1 || section[0].Ref != testRef(i+1, 1) {
			t.Fatalf("section %d must contain task %d only: %+v", i, i+1, section)
		}
	}
	b := view_(t, p, ViewRequest{View: "todo", Actor: "worker-b"}).(*TodoAnswer)
	if len(b.InFlight) != 1 || len(b.AwaitingAcceptance) != 0 || len(b.Ready) != 0 || len(b.Blocked) != 0 {
		t.Fatalf("only the live claim belongs to holder: %+v", b)
	}
	if !reflect.DeepEqual(a.Totals, todoOf(t, p).Totals) {
		t.Fatal("totals must stay project-wide")
	}
}

func TestTodoActorAttentionAndUnknown(t *testing.T) {
	p := teamWorld(t)
	a := view_(t, p, ViewRequest{View: "todo", Actor: "gatekeeper"}).(*TodoAnswer)
	if len(a.Attention) != 1 || a.Attention[0].Ref != testRef(3, 1) || len(a.Blocked) != 0 {
		t.Fatalf("attention follows waiting actor even on another actor's task: %+v", a)
	}
	for _, name := range []string{"missing", "worker", "WORKER-A"} {
		a := view_(t, p, ViewRequest{View: "todo", Actor: name}).(*TodoAnswer)
		if len(a.InFlight)+len(a.Ready)+len(a.Blocked)+len(a.AwaitingAcceptance)+len(a.Attention) != 0 || !strings.Contains(a.FilterNote, "no work or attention found") {
			t.Fatalf("unknown exact name needs empty view and note: %+v", a)
		}
		var out bytes.Buffer
		if err := RenderViewBrief(&out, a); err != nil || !strings.Contains(out.String(), a.FilterNote) {
			t.Fatalf("brief loses note: %v %s", err, &out)
		}
	}
}

func TestTodoActorFilterBeforeLimit(t *testing.T) {
	p := teamWorld(t)
	a := view_(t, p, ViewRequest{View: "todo", Actor: "worker-c", Limit: 1}).(*TodoAnswer)
	if len(a.Ready) != 1 || a.Ready[0].Ref != testRef(5, 1) || a.Omitted["ready"].Offered != 1 || a.Omitted["ready"].Omitted != 0 {
		t.Fatalf("other actors must not consume limit: %+v", a)
	}
}

func TestTodoIdleRequiresRosterAndNeitherHeldNorReady(t *testing.T) {
	p := teamWorld(t)
	if len(todoOf(t, p).Attention) != 2 {
		t.Fatal("no roster must add no alarms")
	}
	p.Policy.Team = []string{"worker-a", "worker-b", "worker-c", "worker-d"}
	a := view_(t, p, ViewRequest{View: "todo", Limit: 1}).(*TodoAnswer)
	var idle []Attention
	for _, note := range a.Attention {
		if note.Kind == "actor-idle" {
			idle = append(idle, note)
		}
	}
	if len(idle) != 1 || idle[0].WaitingActor != (model.Actor{ID: "worker-d"}) || idle[0].Ref.RecordID != "" || !strings.Contains(idle[0].Reason, "no BLOCKED task") {
		t.Fatalf("held and READY actors stay busy even beyond limit: %+v", idle)
	}
}

func TestTodoIdleNearestBlockedAndNamedInflightIsNotHeld(t *testing.T) {
	p := teamWorld(t)
	// A READY task for worker-a prevents its alarm. Hold it so only another
	// actor's live claim and blocked work remain for worker-a.
	appendEvents(t, p, 112, &model.BlockerHold{Task: testRef(2, 1), BlockerID: testID(81), Reason: model.BlockerResume,
		Actor: model.Actor{ID: "gatekeeper"}, Criterion: "release second task"})
	p.Policy.Team = []string{"worker-a", "worker-b", "worker-d"}
	a := view_(t, p, ViewRequest{View: "todo", Actor: "worker-a"}).(*TodoAnswer)
	var idle []Attention
	for _, note := range a.Attention {
		if note.Kind == "actor-idle" {
			idle = append(idle, note)
		}
	}
	if len(idle) != 1 || idle[0].Ref != testRef(2, 1) || !strings.Contains(idle[0].Reason, "gatekeeper") || !strings.Contains(idle[0].Reason, "release second task") {
		t.Fatalf("named in-flight work is not held; nearest tied blocked task wins by id: %+v", idle)
	}
	appendEvents(t, p, 113, &model.BlockerHold{Task: testRef(2, 1), BlockerID: testID(82), Reason: model.BlockerResume,
		Actor: model.Actor{ID: "gatekeeper"}, Criterion: "another permission"})
	a = view_(t, p, ViewRequest{View: "todo", Actor: "worker-a"}).(*TodoAnswer)
	for _, note := range a.Attention {
		if note.Kind == "actor-idle" && note.Ref != testRef(3, 1) {
			t.Fatalf("fewer reasons beats earlier record id: %+v", note)
		}
	}
}

func TestTodoActorFilterBelongsOnlyToTodo(t *testing.T) {
	for _, view := range []string{"show", "history", "continue"} {
		if err := CheckView(ViewRequest{View: view, Actor: "worker-a", ID: testID(1)}); err == nil {
			t.Fatalf("%s accepted todo filter", view)
		}
	}
}

func TestTodoActorDecisionsAndIntake(t *testing.T) {
	p := teamWorld(t)
	appendEvents(t, p, 112, &model.DecisionOpen{ID: testID(30), Provenance: prov("author"), Spec: decisionSpec()})
	capturePacket(t, p, 8)
	for _, name := range []string{"author", "owner", "missing"} {
		a := view_(t, p, ViewRequest{View: "todo", Actor: name}).(*TodoAnswer)
		wantPackets, wantDecisions := 0, 0
		if name == "author" {
			wantPackets = 1
		}
		if name == "owner" {
			wantDecisions = 1
		}
		if len(a.PacketsNotAccepted) != wantPackets || len(a.OpenDecisions) != wantDecisions {
			t.Fatalf("%s: packets=%d decisions=%d; want %d/%d", name, len(a.PacketsNotAccepted), len(a.OpenDecisions), wantPackets, wantDecisions)
		}
	}
}

func TestTodoIdleReportsEveryBlocker(t *testing.T) {
	p := teamWorld(t)
	appendEvents(t, p, 112, &model.BlockerHold{Task: testRef(3, 1), BlockerID: testID(82), Reason: model.BlockerResume,
		Actor: model.Actor{ID: "reviewer"}, Criterion: "second permission"})
	a := todoOf(t, p)
	notes := idleActors(&TodoAnswer{Blocked: a.Blocked}, []string{"worker-a"})
	if len(notes) != 1 || !strings.Contains(notes[0].Reason, "permission to resume") || !strings.Contains(notes[0].Reason, "second permission") || !strings.Contains(notes[0].Reason, "reviewer") {
		t.Fatalf("idle alarm must report every blocker: %+v", notes)
	}
}

func TestTodoTeamReadLeavesLedgerUntouched(t *testing.T) {
	p := teamWorld(t)
	t.Setenv("WHOSAIDSO_NO_CACHE", "1")
	p.Policy.Team = []string{"worker-a", "worker-b", "worker-c", "worker-d"}
	before := treeBytes(t, p.Root)
	view_(t, p, ViewRequest{View: "todo", Actor: "worker-d"})
	if !reflect.DeepEqual(before, treeBytes(t, p.Root)) {
		t.Fatal("team todo changed project files")
	}
}
