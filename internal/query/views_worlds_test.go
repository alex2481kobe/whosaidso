package query

// Shared view-test inputs: the rich fixture's workspace observation and stale
// check, and the retained 1k benchmark fixture (DATUM_VIEWS_1K_ROOT names a
// retained DATUM_BENCH_ROOT holding n1000). Assertions do not belong here.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/store"
)

type thousandWorld struct {
	project  store.Project
	task     model.ID
	nonTasks []model.ID
	observed *Observation
}

func richObservation() *Observation {
	when := presetStart.Add(3 * time.Hour)
	return &Observation{ObservedAt: model.Availability[time.Time]{State: model.Known, Value: &when},
		Head: notKnown[model.GitHead]("not a git checkout"), Dirty: notKnown[bool]("not a git checkout")}
}

// richStale is a git never run: HEAD is unknown, so every observed claim with
// a clean last run reads UNKNOWN with that reason.
func richStale() *StaleGit {
	return &StaleGit{Head: notKnown[model.GitHead]("fixture: git is not run here"),
		Changes: func(model.GitHead, model.GitHead, []string) ([]string, error) { panic("fixture: git is not run here") }}
}

// thousandFixture opens a retained 1k benchmark fixture, or skips.
func thousandFixture(t testing.TB) (thousandWorld, bool) {
	root := os.Getenv("DATUM_VIEWS_1K_ROOT")
	if root == "" {
		return thousandWorld{}, false
	}
	if tt, ok := t.(*testing.T); ok {
		tt.Setenv("HOME", filepath.Join(root, "home"))
	} else if b, ok := t.(*testing.B); ok {
		b.Setenv("HOME", filepath.Join(root, "home"))
	}
	dir := filepath.Join(root, "n1000")
	data, err := os.ReadFile(filepath.Join(dir, "fixture.json"))
	if err != nil {
		t.Fatalf("DATUM_VIEWS_1K_ROOT has no n1000 fixture: %v", err)
	}
	var meta struct{ Task, Claim, Instrument model.RecordRef }
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatal(err)
	}
	project, err := store.Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	observed := &Observation{ObservedAt: known(when), Head: notKnown[model.GitHead]("fixture root is outside git"), Dirty: notKnown[bool]("fixture root is outside git")}
	return thousandWorld{project: project, task: meta.Task.RecordID, nonTasks: []model.ID{meta.Claim.RecordID, meta.Instrument.RecordID},
		observed: observed}, true
}
