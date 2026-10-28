// Todo navigation reaches the same prerequisite hierarchy in fresh CLI
// processes. These fixtures own both their ledger and their machine home.
package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/query"
)

func TestTodoTopAndUnderNavigatePrerequisites(t *testing.T) {
	root, data := cliFixture(t)
	cliControl(t, root, data)
	events, err := model.DecodeEvents(data)
	if err != nil {
		t.Fatal(err)
	}
	typed, err := model.DecodeEvent(events[0])
	if err != nil {
		t.Fatal(err)
	}
	parent := typed.(*model.TaskCreate)
	parent.ID = cliID(10)
	parent.Spec.Intent = "Deliver the parent task"
	parent.Spec.Prerequisites = []model.Prerequisite{{Kind: "task-success", Target: model.RecordRef{Project: "test/cli", RecordID: cliID(1), Revision: 1}, WaiverPolicy: "forbid"}}
	raw, err := model.EncodeEvent(parent)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal([]model.Event{raw})
	if err != nil {
		t.Fatal(err)
	}
	if _, errs, code := cliRun(t, root, payload, "agent", "capture", "--command-id", string(cliID(50)), "--events", "-"); code != 0 {
		t.Fatalf("capture: %d %s", code, errs)
	}
	if _, errs, code := cliRun(t, root, nil, "reviewer", "admit", "--command-id", string(cliID(51)), "--outcome", "accepted", "--reason", "reviewed fixture", string(cliID(50))); code != 0 {
		t.Fatalf("admit: %d %s", code, errs)
	}
	top := readJSON[query.TaskListAnswer](t, readProcess(t, root, nil, "todo", "--top", "--json"))
	if len(top.Tasks) != 1 || top.Tasks[0].ID != cliID(10) || top.Tasks[0].Nested != 1 {
		t.Fatalf("top: %+v", top)
	}
	under := readJSON[query.TaskListAnswer](t, readProcess(t, root, nil, "todo", "--under", string(cliID(10)), "--json"))
	if len(under.Tasks) != 1 || under.Tasks[0].ID != cliID(1) {
		t.Fatalf("under: %+v", under)
	}
	brief := string(readProcess(t, root, nil, "todo", "--top"))
	if !strings.Contains(brief, "open nested 1") || !strings.Contains(brief, "Deliver the parent task") {
		t.Fatalf("compact text: %s", brief)
	}
	for _, args := range [][]string{{"todo", "--top", "--under", string(cliID(10))}, {"todo", "--under", "bad"}} {
		if _, _, code := cliRun(t, root, nil, "", args...); code != 2 {
			t.Fatalf("invalid flags %v: %d", args, code)
		}
	}
}
