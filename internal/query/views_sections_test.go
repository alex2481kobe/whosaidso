package query

// The section-by-section walk of views_coverage_test.go: which old read maps
// to which new view, run on the rich query fixture, on viewsWorld (every task
// shape), and on the 1k benchmark fixture when DATUM_VIEWS_1K_ROOT names a
// retained DATUM_BENCH_ROOT holding n1000.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

type coverageWorld struct {
	project  store.Project
	task     model.ID // a task for continue/context
	nonTasks []model.ID
	observed *Observation
	stale    StaleCheck
}

func readOld(t *testing.T, w coverageWorld, r Request) any {
	t.Helper()
	a, err := Read(w.project, r)
	if err != nil {
		t.Fatalf("old %s: %v", r.Command, err)
	}
	return exported(t, a)
}

func readNew(t *testing.T, w coverageWorld, r ViewRequest) any {
	t.Helper()
	a, err := ReadView(w.project, r)
	if err != nil {
		t.Fatalf("new %s: %v", r.View, err)
	}
	return exported(t, a)
}

func checkSectionCoverage(t *testing.T, w coverageWorld) {
	c := &coverage{t: t}
	todo := readNew(t, w, ViewRequest{View: "todo"})
	show := readNew(t, w, ViewRequest{View: "show", Stale: w.stale})
	project := str(show, "project")

	oldTodo := readOld(t, w, Request{Command: "todo"})
	all := allTodoTasks(todo)
	for _, section := range []string{"blocked", "awaiting_acceptance", "ready"} {
		c.records("todo "+section, items(oldTodo, "preset", section), all, nil, "record")
	}
	c.records("todo decisions", items(oldTodo, "preset", "decisions"), items(todo, "open_decisions"), nil, "decision")
	limit, _ := at(oldTodo, "preset", "limit")
	c.item("todo limit", limit, func(path []any) (any, bool) { return at(todo, joinPath([]any{"omitted", "ready"}, path)...) })
	intakeCovered := func(where string, old any) {
		for _, o := range items(old, "intake") {
			n, ok := byKey(items(todo, "intake_pending"), str(o, "command_id"), "command_id")
			if !ok {
				c.fail("%s packet %s has no new home", where, str(o, "command_id"))
				continue
			}
			c.item(where+" packet", o, func(path []any) (any, bool) { return at(n, path...) })
		}
	}
	intakeCovered("todo intake", oldTodo)
	intakeCovered("intake pending", readOld(t, w, Request{Command: "intake pending"}))

	oldNow := readOld(t, w, Request{Command: "now"})
	c.nowCovered("now", mustAt(t, oldNow, "preset"), todo)

	oldState := readOld(t, w, Request{Command: "state", Stale: w.stale})
	c.stateCovered("state", mustAt(t, oldState, "preset"), show)
	c.stateCovered("context", mustAt(t, readOld(t, w, Request{Command: "context"}), "preset"), show)

	oldInstruments := readOld(t, w, Request{Command: "instruments"})
	instruments := readNew(t, w, ViewRequest{View: "show", Kind: "instrument"})
	c.records("instruments", items(oldInstruments, "preset", "instruments"), items(instruments, "records"), nil, "instrument")
	c.attentionCovered("instruments", items(oldInstruments, "preset", "attention"), items(instruments, "attention"))

	oldShow := readOld(t, w, Request{Command: "show"})
	for _, o := range items(oldShow, "records") {
		n, ok := byKey2(items(show, "records"), factKey(o, "fact"))
		if !ok {
			c.fail("show record %s has no new home", factKey(o, "fact"))
			continue
		}
		c.item("show "+factKey(o, "fact"), o, oldRecordFinder(n, items(show, "runs"), o, project))
	}
	for _, id := range append([]model.ID{w.task}, w.nonTasks...) {
		one := readOld(t, w, Request{Command: "show", ID: id})
		newOne := readNew(t, w, ViewRequest{View: "show", ID: id})
		o := items(one, "records")[0]
		c.item("show "+string(id), o, oldRecordFinder(items(newOne, "records")[0], items(newOne, "runs"), o, project))

		ctx := readOld(t, w, Request{Command: "context", ID: id})
		cont := readNew(t, w, ViewRequest{View: "continue", ID: id, Observed: w.observed})
		root, _ := at(cont, "records", str(cont, "record", "record_id")+"@"+str(cont, "record", "revision"))
		rec := items(ctx, "records")[0]
		c.item("context "+string(id)+" record", rec, oldRecordFinder(root, items(cont, "runs"), rec, project))
		c.closureCovered("context "+string(id), mustAt(t, ctx, "preset", "closure"), cont)
		c.attentionCovered("context "+string(id), items(ctx, "preset", "attention"), items(cont, "attention"))
	}

	oldCont := readOld(t, w, Request{Command: "continue", ID: w.task, Observed: w.observed})
	cont := readNew(t, w, ViewRequest{View: "continue", ID: w.task, Observed: w.observed})
	oc := mustAt(t, oldCont, "preset", "continue")
	for _, key := range []string{"observed", "progress", "attempts", "handoff", "generated"} {
		v, _ := at(oc, key)
		c.item("continue "+key, v, func(path []any) (any, bool) { return at(cont, joinPath([]any{key}, path)...) })
	}
	task, _ := at(oc, "task")
	c.item("continue task", task, func(path []any) (any, bool) { return at(cont, joinPath([]any{"record"}, path)...) })
	c.runs("continue runs", items(oc, "runs"), items(cont, "runs"))
	c.closureCovered("continue", mustAt(t, oc, "closure"), cont)
	c.attentionCovered("continue", items(oldCont, "preset", "attention"), items(cont, "attention"))
	c.nowCovered("continue now", mustAt(t, oc, "now"), todo)
	c.stateCovered("continue state", mustAt(t, oc, "state"), show)
	c.done()
	t.Logf("section coverage: %d old leaves found in their new homes", c.leaves)
}

func mustAt(t *testing.T, v any, steps ...any) any {
	t.Helper()
	x, ok := at(v, steps...)
	if !ok {
		t.Fatalf("control: old answer lacks %v", steps)
	}
	return x
}

func richObservation() *Observation {
	when := presetStart.Add(3 * time.Hour)
	return &Observation{ObservedAt: model.Availability[time.Time]{State: model.Known, Value: &when},
		Head: notKnown[model.GitHead]("not a git checkout"), Dirty: notKnown[bool]("not a git checkout")}
}

func richStale(s reduce.Snapshot) []StaleClaim {
	return []StaleClaim{{Claim: testRef(21, 1), LastRun: model.InvocationRef{Project: projectID, InvocationID: testID(50)},
		Stale: reduce.TruthUnknown, ChangedPaths: []string{}, Reason: "fixture: git is not run here"}}
}

func TestEveryOldSectionIsInItsNewHomeOnTheRichFixture(t *testing.T) {
	p := testProject(t)
	richWorld(t, p)
	checkSectionCoverage(t, coverageWorld{project: p, task: testID(1),
		nonTasks: []model.ID{testID(2), testID(10), testID(11), testID(20), testID(22), testID(31), testID(32)},
		observed: richObservation(), stale: richStale})
}

// thousandFixture opens a retained 1k benchmark fixture, or skips.
func thousandFixture(t testing.TB) (coverageWorld, bool) {
	root := os.Getenv("DATUM_VIEWS_1K_ROOT")
	if root == "" {
		return coverageWorld{}, false
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
	return coverageWorld{project: project, task: meta.Task.RecordID, nonTasks: []model.ID{meta.Claim.RecordID, meta.Instrument.RecordID},
		observed: observed}, true
}

func TestEveryOldSectionIsInItsNewHomeOnThe1kFixture(t *testing.T) {
	w, ok := thousandFixture(t)
	if !ok {
		t.Skip("set DATUM_VIEWS_1K_ROOT to a retained DATUM_BENCH_ROOT holding n1000")
	}
	checkSectionCoverage(t, w)
}
