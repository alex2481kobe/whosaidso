package query

// Run visibility tests: durations from recorded times only, and the scope
// verdict against the attempt's task revision. UNKNOWN wherever not recorded.

import (
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/reduce"
)

func runsByID(t *testing.T, a Answer) map[model.ID]RunView {
	t.Helper()
	out := map[model.ID]RunView{}
	for _, r := range *a.Preset.Runs {
		out[r.Invocation] = r
	}
	return out
}

func TestRunsShowDurationAndFlagInputsOutsideTaskScope(t *testing.T) {
	p := testProject(t)
	presetWorld(t, p)
	a := presetAnswer(t, p, Request{Command: "state"})
	assertHonestRendering(t, a)
	byID := runsByID(t, a)
	long, outside, open := byID[testID(50)], byID[testID(51)], byID[testID(52)]
	if d, ok := long.Duration.(Duration); !ok || d.Nanoseconds != int64(2*time.Hour) || d.Text != "2h0m0s" {
		t.Fatalf("a sealed run's duration is observed_at minus started_at, got %+v", long.Duration)
	}
	if long.Scope.WithinTaskScope != reduce.TruthTrue || len(long.Scope.Outside) != 0 || long.Task != testRef(1, 1) {
		t.Fatalf("a run reading only declared source paths is within scope, got %+v", long.Scope)
	}
	if outside.Scope.WithinTaskScope != reduce.TruthFalse || len(outside.Scope.Outside) != 1 || outside.Scope.Outside[0] != "internal/reduce/task.go" {
		t.Fatalf("an input outside scope.source_paths must be flagged FALSE and named, got %+v", outside.Scope)
	}
	if _, ok := open.Duration.(Unknown); !ok || open.Sealed {
		t.Fatalf("an unsealed run's duration must be UNKNOWN, never zero, got %+v", open.Duration)
	}
	if _, ok := open.Outcome.(Unknown); !ok || open.Scope.WithinTaskScope != reduce.TruthUnknown || open.Scope.Reason == "" {
		t.Fatalf("an unsealed run with a pathless input must be UNKNOWN outcome and UNKNOWN scope with a reason, got %+v", open)
	}
	if _, ok := open.InputPaths[0].(Unknown); !ok {
		t.Fatalf("a content-only input has an UNKNOWN path, not a blank one, got %+v", open.InputPaths)
	}
	flagged := 0
	for _, note := range a.Preset.Attention {
		if note.Kind == "run-outside-task-scope" {
			flagged++
		}
	}
	if flagged != 1 {
		t.Fatalf("exactly the out-of-scope run must be raised under attention, got %+v", a.Preset.Attention)
	}
}

func TestScopeCheckIsUnknownWithoutDeclaredPathsOrRecordedInputs(t *testing.T) {
	cases := []struct {
		name     string
		scope    []string
		paths    []string
		pathless bool
		want     reduce.Truth
	}{
		{"no declared scope", nil, []string{"a/b.go"}, false, reduce.TruthUnknown},
		{"no recorded paths", []string{"a"}, nil, false, reduce.TruthUnknown},
		{"one pathless input", []string{"a"}, []string{"a/b.go"}, true, reduce.TruthUnknown},
		{"outside beats pathless", []string{"a"}, []string{"b/c.go"}, true, reduce.TruthFalse},
		{"prefix is not containment", []string{"a/b"}, []string{"a/bc.go"}, false, reduce.TruthFalse},
		{"directory contains", []string{"a/b"}, []string{"a/b/c.go"}, false, reduce.TruthTrue},
		{"exact file", []string{"a/b.go"}, []string{"a/b.go"}, false, reduce.TruthTrue},
	}
	for _, c := range cases {
		got := scopeCheck(c.scope, c.paths, c.pathless, "")
		if got.WithinTaskScope != c.want || got.WithinTaskScope == reduce.TruthUnknown && got.Reason == "" {
			t.Fatalf("%s: got %+v, want %s with a reason when UNKNOWN", c.name, got, c.want)
		}
	}
}

func TestDurationIsUnknownWhenObservationTimeIsMissingOrBackwards(t *testing.T) {
	start := presetStart
	if _, d := elapsed(start, notKnown[time.Time]("runner died")); d != unknown("seal recorded no observation time: runner died") {
		t.Fatalf("missing observation time must be UNKNOWN with its reason, got %+v", d)
	}
	if _, d := elapsed(start, known(start.Add(-time.Second))); d.(Unknown).State != "UNKNOWN" {
		t.Fatalf("a backwards clock must be UNKNOWN, never a negative or zero duration, got %+v", d)
	}
	if _, d := elapsed(start, known(start)); d != (Duration{Nanoseconds: 0, Text: "0s"}) {
		t.Fatalf("an observed zero duration is a real reading, got %+v", d)
	}
}
