// Current-record lists select before copying. Old revisions stay available in
// detail and history, without making each task list copy their payloads.
package query

import (
	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
	"testing"
)

func TestCurrentListsSkipHistoricalPayloads(t *testing.T) {
	p := screenProject(t)
	task := testTask(1)
	appendEvents(t, p, 1000, task)
	for revision := 1; revision <= 30; revision++ {
		appendEvents(t, p, 1000+revision, &model.TaskAmend{Target: testRef(1, model.Revision(revision)), Provenance: task.Provenance, Replacement: task.Spec})
	}
	source, err := store.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	var got int
	allocations := testing.AllocsPerRun(1, func() {
		records := currentRecords(source.Snapshot())
		got = len(records)
		if got == 1 && records[0].Key.Revision != 31 {
			t.Fatal("current list returned an old revision")
		}
	})
	if got != 1 || allocations > 100 {
		t.Fatalf("current list copied historical revisions: %d records, %.0f allocations", got, allocations)
	}
	all := source.Snapshot().Records()
	if len(all) != 31 {
		t.Fatal("list optimization removed history")
	}
}
