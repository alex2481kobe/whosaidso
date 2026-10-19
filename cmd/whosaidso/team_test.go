// Team todo integration exercises config discovery, replay, selection and
// both CLI renderings in a temporary project. It never uses a real ledger.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/query"
)

func TestCLITeamTodoThreeActors(t *testing.T) {
	t.Setenv("WHOSAIDSO_HOME", filepath.Join(t.TempDir(), "home"))
	root, data := cliFixture(t)
	path := filepath.Join(root, "whosaidso.toml")
	if err := os.WriteFile(path, []byte("id = 'test/cli'\nledger = '.whosaidso/events'\nteam = 'worker-a, worker-b, worker-c'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var events []model.Event
	if err := json.Unmarshal(data, &events); err != nil {
		t.Fatal(err)
	}
	decoded, err := model.DecodeEvent(events[0])
	if err != nil {
		t.Fatal(err)
	}
	first := decoded.(*model.TaskCreate)
	first.Spec.NextActor = model.Actor{ID: "worker-a"}
	second := *first
	second.ID = cliID(10)
	second.Spec.NextActor = model.Actor{ID: "worker-b"}
	second.Spec.Intent = "prepare blocked delivery"
	encode := func(items ...model.TypedEvent) []byte {
		t.Helper()
		raw := []model.Event{}
		for _, item := range items {
			e, err := model.EncodeEvent(item)
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw, e)
		}
		b, err := model.Encode(raw)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if _, stderr, code := cliRun(t, root, encode(first, &second), "author", "capture", "--events", "-", "--admit", "--reason", "fixture reviewed"); code != 0 {
		t.Fatalf("create fixture: %d %s", code, stderr)
	}
	ref := func(n int) model.RecordRef {
		return model.RecordRef{Project: "test/cli", RecordID: cliID(n), Revision: 1}
	}
	input := encode(&model.TaskStart{Task: ref(1), AttemptID: cliID(20), Actor: model.Actor{ID: "worker-a"}},
		&model.BlockerHold{Task: ref(10), BlockerID: cliID(21), Reason: model.BlockerResume, Actor: model.Actor{ID: "gatekeeper"}, Criterion: "approval to resume"})
	if _, stderr, code := cliRun(t, root, input, "worker-a", "capture", "--events", "-", "--admit", "--reason", "fixture reviewed"); code != 0 {
		t.Fatalf("capture fixture: %d %s", code, stderr)
	}
	read := func(args ...string) query.TodoAnswer {
		t.Helper()
		return readJSON[query.TodoAnswer](t, readProcess(t, root, nil, args...))
	}
	a := read("todo", "--json")
	idle := map[string]query.Attention{}
	for _, n := range a.Attention {
		if n.Kind == "actor-idle" {
			idle[n.WaitingActor.(map[string]any)["id"].(string)] = n
		}
	}
	if len(idle) != 2 || idle["worker-b"].Ref != ref(10) || !strings.Contains(idle["worker-b"].Reason, "approval to resume") || idle["worker-c"].Label == "" || idle["worker-c"].Ref.RecordID != "" {
		t.Fatalf("busy actor quiet, blocked actor names blocker, empty actor explicit: %+v", idle)
	}
	b := read("todo", "--actor", "worker-b", "--json")
	if len(b.Blocked) != 1 || len(b.InFlight) != 0 || len(b.Attention) != 2 || b.Totals.Tasks != 2 {
		t.Fatalf("filtered JSON: %+v", b)
	}
	text := string(readProcess(t, root, nil, "todo", "--actor", "worker-b"))
	brief, _, err := query.ViewBriefOf(readProcess(t, root, nil, "todo", "--actor", "worker-b", "--json"))
	if err != nil || brief != text || !strings.Contains(text, "actor-idle") || !strings.Contains(text, "totals are project-wide") {
		t.Fatalf("text/JSON disagree: %v\n%s", err, text)
	}
	t.Logf("todo --actor worker-b:\n%s", text)
	unknown := read("todo", "--actor", "missing", "--json")
	if len(unknown.Attention)+len(unknown.Blocked)+len(unknown.InFlight) != 0 || !strings.Contains(unknown.FilterNote, "no work or attention found") {
		t.Fatalf("unknown actor: %+v", unknown)
	}
}
