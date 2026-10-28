// Startup work must depend on the selected page, even when an unselected task
// has many prerequisites. Allocation counts avoid a machine-speed assertion.
package query

import (
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func TestScreenStartupSkipsUnselectedStatusWork(t *testing.T) {
	p := screenProject(t)
	events := []model.TypedEvent{}
	for i := 1; i <= 25; i++ {
		task := testTask(i)
		if i == 1 {
			for j := 0; j < 10000; j++ {
				task.Spec.Prerequisites = append(task.Spec.Prerequisites, model.Prerequisite{Kind: "task-success", Target: model.RecordRef{Project: "outside/project", RecordID: testID(100000 + j), Revision: 1}, WaiverPolicy: "forbid"})
			}
		}
		events = append(events, task)
	}
	appendEvents(t, p, 1001, events...)
	state, err := store.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	var answer *ScreenAnswer
	allocations := testing.AllocsPerRun(1, func() { answer, err = ReadScreenFrom(p, ScreenRequest{View: "records"}, state) })
	if err != nil || len(answer.Rows) != 24 || answer.Rows[0].ID != testID(25) {
		t.Fatalf("startup selection: %v", err)
	}
	// The generous ceiling separates a small page from evaluating ten thousand
	// unrelated prerequisites, without depending on elapsed time or CPU load.
	if allocations > 5000 {
		t.Fatalf("startup evaluated unselected status work: %.0f allocations", allocations)
	}
	t.Logf("selected page: %.0f allocations", allocations)
}
