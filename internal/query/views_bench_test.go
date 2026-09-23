package query

// Cost of the views: show ID reads one record and never builds the project
// (an allocation test that fails if it grows with the ledger), and a
// benchmark of every new view beside the old reads it replaces, on the 1k
// benchmark fixture (DATUM_VIEWS_1K_ROOT; see views_coverage_test.go).

import (
	"os"
	"testing"

	"datum/internal/model"
	"datum/internal/store"
)

func TestShowOneNeverBuildsTheWholeProject(t *testing.T) {
	allocs := func(extra int) (one, bare float64) {
		p := testProject(t)
		viewsWorld(t, p)
		more := []model.TypedEvent{}
		for i := 0; i < extra; i++ {
			more = append(more, testTask(2000+i))
		}
		if extra > 0 {
			appendEvents(t, p, 200, more...)
		}
		source, err := store.Replayed(p)
		if err != nil {
			t.Fatal(err)
		}
		read := func(r ViewRequest) func() {
			return func() {
				if _, err := ReadViewFrom(p, r, source); err != nil {
					t.Fatal(err)
				}
			}
		}
		return testing.AllocsPerRun(5, read(ViewRequest{View: "show", ID: testID(22)})), testing.AllocsPerRun(2, read(ViewRequest{View: "show"}))
	}
	small, smallBare := allocs(0)
	large, largeBare := allocs(300)
	if largeBare < 5*smallBare {
		t.Fatalf("control: bare show must grow with 300 more tasks, %v -> %v allocations", smallBare, largeBare)
	}
	if large > small*1.05+10 {
		t.Fatalf("show ID grew from %v to %v allocations with 300 unrelated records; it must read its record directly", small, large)
	}
	t.Logf("show ID allocations: %v with 17 records, %v with 317; bare show %v -> %v", small, large, smallBare, largeBare)
}

func BenchmarkViewsAgainstOldReads(b *testing.B) {
	w, ok := thousandFixture(b)
	if !ok {
		b.Skip("set DATUM_VIEWS_1K_ROOT to a retained DATUM_BENCH_ROOT holding n1000")
	}
	if _, err := store.Load(w.project); err != nil { // warm the cache image, as after any command
		b.Fatal(err)
	}
	// DATUM_VIEWS_BENCH_SOURCE=1 answers from one loaded prefix, so the numbers
	// are the views' own work without ledger loading and project discovery.
	var source Source
	if os.Getenv("DATUM_VIEWS_BENCH_SOURCE") != "" {
		loaded, err := store.Load(w.project)
		if err != nil {
			b.Fatal(err)
		}
		source = loaded
	}
	old := func(r Request) func() error {
		return func() error {
			if source != nil {
				a, err := ReadFrom(w.project, r, source)
				if err != nil {
					return err
				}
				return RenderJSON(discard{}, a)
			}
			project, err := store.Discover(w.project.Root)
			if err != nil {
				return err
			}
			a, err := Read(project, r)
			if err != nil {
				return err
			}
			return RenderJSON(discard{}, a)
		}
	}
	fresh := func(r ViewRequest) func() error {
		return func() error {
			if source != nil {
				a, err := ReadViewFrom(w.project, r, source)
				if err != nil {
					return err
				}
				return RenderViewJSON(discard{}, a)
			}
			project, err := store.Discover(w.project.Root)
			if err != nil {
				return err
			}
			a, err := ReadView(project, r)
			if err != nil {
				return err
			}
			return RenderViewJSON(discard{}, a)
		}
	}
	cases := []struct {
		name string
		run  func() error
	}{
		{"OldTodo", old(Request{Command: "todo"})},
		{"OldNow", old(Request{Command: "now"})},
		{"OldIntakePending", old(Request{Command: "intake pending"})},
		{"NewTodo", fresh(ViewRequest{View: "todo"})},
		{"OldContinue", old(Request{Command: "continue", ID: w.task, Observed: w.observed})},
		{"NewContinue", fresh(ViewRequest{View: "continue", ID: w.task, Observed: w.observed})},
		{"OldContextClaim", old(Request{Command: "context", ID: w.nonTasks[0]})},
		{"NewContinueClaim", fresh(ViewRequest{View: "continue", ID: w.nonTasks[0], Observed: w.observed})},
		{"OldShow", old(Request{Command: "show"})},
		{"OldState", old(Request{Command: "state"})},
		{"NewShow", fresh(ViewRequest{View: "show"})},
		{"OldInstruments", old(Request{Command: "instruments"})},
		{"NewShowKindInstrument", fresh(ViewRequest{View: "show", Kind: "instrument"})},
		{"OldShowOne", old(Request{Command: "show", ID: w.task})},
		{"NewShowOne", fresh(ViewRequest{View: "show", ID: w.task})},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := c.run(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
