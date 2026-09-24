// Criterion tests: benchreport's readings put through WhoSaidSo's own observation
// and evaluation path (internal/evidence Observe then Evaluate), exactly as a
// run's stdout would be, following tools/archtree/criterion_test.go. A reading
// that only looks selectable proves nothing; these assert the verdict.

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whosaidso/internal/evidence"
	"whosaidso/internal/model"
)

const (
	critProject    = model.ProjectID("datum/datum")
	critClaim      = model.ID("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	critID         = model.ID("01ARZ3NDEKTSV4RRFFQ69G5FAW")
	critAttempt    = model.ID("01ARZ3NDEKTSV4RRFFQ69G5FBV")
	critInstrument = model.ID("01ARZ3NDEKTSV4RRFFQ69G5FBW")
	critInvocation = model.ID("01ARZ3NDEKTSV4RRFFQ69G5FAX")
)

func stdoutPin(body []byte, pointer string) model.ArtifactRef {
	return model.ArtifactRef{
		Kind: "content",
		Content: &model.ContentPin{SHA256: model.HashBytes(body), Length: uint64(len(body)),
			MediaType: "application/json", Locators: []model.Locator{{Path: "stdout"}}},
		Selector: model.Selector{Kind: "json-pointer", Pointer: pointer},
	}
}

// atMost is "<reading at result> is at most <max> <unit>" over the population
// the pointer (or, for a set, its /values) selects.
func atMost(t *testing.T, example []byte, result, popPtr, unit, identity, max string) model.CriterionFix {
	t.Helper()
	target := json.Number(max)
	c := model.CriterionFix{
		Claim:       model.RecordRef{Project: critProject, RecordID: critClaim, Revision: 1},
		CriterionID: critID,
		Revision:    1,
		Expression: model.CriterionExpression{
			ResultSelector: stdoutPin(example, result),
			Unit:           unit,
			Population: model.Population{
				Identity:    identity,
				Selector:    stdoutPin(example, popPtr),
				Denominator: "benchmark runs",
			},
			Operator: model.LessEqual,
			Target:   model.Scalar{Type: "number", Number: &target},
			Reducer:  model.All,
		},
		Policy:     model.EvaluationPolicy{Inclusion: "entire-criterion-family", Retry: "retain-all"},
		Author:     model.Actor{ID: "bench-instrument-lane"},
		SourceRefs: []model.ArtifactRef{},
	}
	if err := model.ValidateSchema(c); err != nil {
		t.Fatalf("fixture criterion is not valid: %v", err)
	}
	return c
}

// evaluate stores stdout as admission stores a run's stdout and lets WhoSaidSo's
// resolver find, read and judge it.
func evaluate(t *testing.T, c model.CriterionFix, stdout []byte) evidence.Evaluation {
	t.Helper()
	root := t.TempDir()
	// write.Run's stdout is the output named stdout; admission publishes its
	// bytes to the store by digest, where Observe reads them.
	full := filepath.Join(root, filepath.FromSlash(evidence.DefaultArtifactDir), string(model.HashBytes(stdout)))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, stdout, 0o644); err != nil {
		t.Fatal(err)
	}
	ref := model.CriterionRef{Claim: c.Claim, CriterionID: c.CriterionID, Revision: c.Revision}
	exit := 0
	outcome := model.ProcessOutcome{Kind: "exit", ExitCode: &exit}
	pin := stdoutPin(stdout, "").Content
	out := model.RunOutput{Name: "stdout", SHA256: pin.SHA256, Length: pin.Length, MediaType: pin.MediaType}
	env := model.InvocationEnvelope{
		InvocationID:            critInvocation,
		AttemptID:               critAttempt,
		InstrumentRef:           model.RecordRef{Project: critProject, RecordID: critInstrument, Revision: 1},
		CriterionRef:            model.Availability[model.CriterionRef]{State: model.Known, Value: &ref},
		ExecutionSourceIdentity: model.ExecutionIdentity{Project: critProject, SourceRefs: []model.ArtifactRef{}},
		Argv:                    []string{"go", "run", "./tools/benchreport"},
		StartedAt:               time.Unix(1_700_000_000, 0).UTC(),
		Outcome:                 model.Availability[model.ProcessOutcome]{State: model.Known, Value: &outcome},
		Outputs:                 model.Availability[[]model.RunOutput]{State: model.Known, Value: &[]model.RunOutput{out}},
	}
	o, err := evidence.NewResolver(root).Observe(context.Background(), c, env)
	if err != nil {
		t.Fatal(err)
	}
	ev, err := evidence.Evaluate(c, []evidence.Observation{o})
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func reportOf(t *testing.T, input string) []byte {
	t.Helper()
	b, err := measure(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const (
	showMedian = "/readings/by_benchmark/BenchmarkCommands~1N1000~1ShowOne/ns_per_op"
	todoMedian = "/readings/by_benchmark/BenchmarkCommands~1N1000~1Todo/ns_per_op"
	showPop    = "median of BenchmarkCommands/N1000/ShowOne runs in whosaidso/internal/benchmarks"
	todoPop    = "median of BenchmarkCommands/N1000/Todo runs in whosaidso/internal/benchmarks"
)

// "BenchmarkX median <= 1e9 ns/op", on the real 1k run: ShowOne's median is
// 665484958 (TRUE) and Todo's is 1400206709 (FALSE).
func TestMedianCriterionDecides(t *testing.T) {
	out := reportOf(t, realRun)
	if ev := evaluate(t, atMost(t, out, showMedian, showMedian, "ns/op", showPop, "1000000000"), out); ev.Verdict != evidence.True {
		t.Fatalf("ShowOne median <= 1e9 ns/op: %s", ev.Describe())
	}
	if ev := evaluate(t, atMost(t, out, todoMedian, todoMedian, "ns/op", todoPop, "1000000000"), out); ev.Verdict != evidence.False {
		t.Fatalf("Todo median <= 1e9 ns/op: %s", ev.Describe())
	}
	// Frozen against one run, judged against another's bytes. One slow run of
	// three leaves the median alone; two slow runs move it, and it fails.
	oneSlow := reportOf(t, strings.ReplaceAll(realRun, " 665484958 ns/op", "1665484958 ns/op"))
	if ev := evaluate(t, atMost(t, out, showMedian, showMedian, "ns/op", showPop, "1000000000"), oneSlow); ev.Verdict != evidence.True {
		t.Fatalf("ShowOne median 667107208 <= 1e9 ns/op: %s", ev.Describe())
	}
	slow := reportOf(t, strings.ReplaceAll(strings.ReplaceAll(realRun, " 665484958 ns/op", "1665484958 ns/op"), " 667107208 ns/op", "1667107208 ns/op"))
	if ev := evaluate(t, atMost(t, out, showMedian, showMedian, "ns/op", showPop, "1000000000"), slow); ev.Verdict != evidence.False {
		t.Fatalf("ShowOne median 1665484958 <= 1e9 ns/op: %s", ev.Describe())
	}
}

// Every run, not only the median: the samples set with its /values population.
func TestSamplesCriterionDecides(t *testing.T) {
	out := reportOf(t, realRun)
	const set = "/readings/by_benchmark/BenchmarkCommands~1N1000~1ShowOne/ns_per_op_samples"
	const pop = "BenchmarkCommands/N1000/ShowOne runs in whosaidso/internal/benchmarks"
	if ev := evaluate(t, atMost(t, out, set, set+"/values", "ns/op", pop, "667107208"), out); ev.Verdict != evidence.True {
		t.Fatalf("every ShowOne run <= its slowest: %s", ev.Describe())
	}
	ev := evaluate(t, atMost(t, out, set, set+"/values", "ns/op", pop, "667107207"), out)
	if ev.Verdict != evidence.False || !strings.Contains(ev.Reason, "run 3") {
		t.Fatalf("run 3 is 1ns over, and the reason must name it: %s", ev.Describe())
	}
}

// Absent is UNKNOWN, never zero: a benchmark that did not run, and B/op from
// a run without -benchmem.
func TestAbsentReadingsAreUnknown(t *testing.T) {
	out := reportOf(t, realRun)
	noShow := reportOf(t, strings.Join(strings.SplitAfter(realRun, "\n")[:4], "")+
		strings.Join(strings.SplitAfter(realRun, "\n")[7:], ""))
	if ev := evaluate(t, atMost(t, out, showMedian, showMedian, "ns/op", showPop, "1000000000"), noShow); ev.Verdict != evidence.Unknown {
		t.Fatalf("ShowOne did not run: %s", ev.Describe())
	}
	const bytes = "/readings/by_benchmark/BenchmarkX/B_per_op"
	with := reportOf(t, "BenchmarkX \t 1\t 5 ns/op\t 7 B/op\n")
	without := reportOf(t, "BenchmarkX \t 1\t 5 ns/op\n")
	c := atMost(t, with, bytes, bytes, "B/op", "median of BenchmarkX runs", "10")
	if ev := evaluate(t, c, with); ev.Verdict != evidence.True {
		t.Fatalf("control, B/op reported: %s", ev.Describe())
	}
	if ev := evaluate(t, c, without); ev.Verdict != evidence.Unknown {
		t.Fatalf("B/op not reported must be UNKNOWN, not 0 <= 10: %s", ev.Describe())
	}
}

// Units are the artifact's: a criterion in B/op cannot read ns/op, and a
// median's population is not its samples'.
func TestUnitAndPopulationAreChecked(t *testing.T) {
	out := reportOf(t, realRun)
	if ev := evaluate(t, atMost(t, out, showMedian, showMedian, "B/op", showPop, "1000000000"), out); ev.Verdict != evidence.Unknown ||
		!strings.Contains(ev.Reason, "unit") {
		t.Fatalf("B/op criterion on ns/op reading: %s", ev.Describe())
	}
	pop := strings.TrimPrefix(showPop, "median of ")
	if ev := evaluate(t, atMost(t, out, showMedian, showMedian, "ns/op", pop, "1000000000"), out); ev.Verdict != evidence.Unknown ||
		!strings.Contains(ev.Reason, "population mismatch") {
		t.Fatalf("samples population on a median: %s", ev.Describe())
	}
}
