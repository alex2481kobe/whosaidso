// Known answers: fixed go test text in, exact readings out. Every table here
// opens with input that must succeed. Evaluator tests live in
// criterion_test.go.

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// realRun is verbatim output of
// go test -bench 'Commands/^N1000$/^(ShowOne|Todo)$' -run '^$' -count 3 -benchmem ./internal/benchmarks/
// on an Apple M4, 2026-09-23. It has sub-benchmarks with slashes, a -10 procs
// suffix, three repeats and two custom metrics.
const realRun = "goos: darwin\n" +
	"goarch: arm64\n" +
	"pkg: datum/internal/benchmarks\n" +
	"cpu: Apple M4\n" +
	"BenchmarkCommands/N1000/ShowOne-10 \t       2\t 662256438 ns/op\t      3252 json-B/op\t      2859 text-B/op\t543252852 B/op\t17568992 allocs/op\n" +
	"BenchmarkCommands/N1000/ShowOne-10 \t       2\t 665484958 ns/op\t      3252 json-B/op\t      2859 text-B/op\t543273632 B/op\t17569048 allocs/op\n" +
	"BenchmarkCommands/N1000/ShowOne-10 \t       2\t 667107208 ns/op\t      3252 json-B/op\t      2859 text-B/op\t543261988 B/op\t17569029 allocs/op\n" +
	"BenchmarkCommands/N1000/Todo-10    \t       1\t1397100625 ns/op\t    575703 json-B/op\t    503899 text-B/op\t987096488 B/op\t34798116 allocs/op\n" +
	"BenchmarkCommands/N1000/Todo-10    \t       1\t1405629541 ns/op\t    575703 json-B/op\t    503899 text-B/op\t987094456 B/op\t34797910 allocs/op\n" +
	"BenchmarkCommands/N1000/Todo-10    \t       1\t1400206709 ns/op\t    575703 json-B/op\t    503899 text-B/op\t987002208 B/op\t34797888 allocs/op\n" +
	"PASS\n" +
	"ok  \tdatum/internal/benchmarks\t12.794s\n"

// run returns the report as generic JSON, numbers kept as their text.
func run(t *testing.T, input string) map[string]any {
	t.Helper()
	b, err := measure(strings.NewReader(input))
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func bench(t *testing.T, out map[string]any, name string) map[string]any {
	t.Helper()
	b, ok := out["readings"].(map[string]any)["by_benchmark"].(map[string]any)[name].(map[string]any)
	if !ok {
		t.Fatalf("no readings for %s", name)
	}
	return b
}

// text renders any JSON value compactly for an exact comparison.
func text(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestRepeatsMediansAndSamples(t *testing.T) {
	out := run(t, realRun)
	show := bench(t, out, "BenchmarkCommands/N1000/ShowOne")
	for key, want := range map[string]string{
		"ns_per_op":     `{"denominator":"benchmark runs","median_of_runs":[2],"population":"median of BenchmarkCommands/N1000/ShowOne runs in datum/internal/benchmarks","rule":"middle value of 3 runs sorted by value","statistic":"median","unit":"ns/op","value":665484958}`,
		"B_per_op":      `{"denominator":"benchmark runs","median_of_runs":[3],"population":"median of BenchmarkCommands/N1000/ShowOne runs in datum/internal/benchmarks","rule":"middle value of 3 runs sorted by value","statistic":"median","unit":"B/op","value":543261988}`,
		"allocs_per_op": `{"denominator":"benchmark runs","median_of_runs":[3],"population":"median of BenchmarkCommands/N1000/ShowOne runs in datum/internal/benchmarks","rule":"middle value of 3 runs sorted by value","statistic":"median","unit":"allocs/op","value":17569029}`,
		"runs":          `{"denominator":"benchmark runs","population":"BenchmarkCommands/N1000/ShowOne runs in datum/internal/benchmarks","unit":"runs","value":3}`,
		"conditions":    `{"cpu":"Apple M4","goarch":"arm64","goos":"darwin","pkg":"datum/internal/benchmarks","procs":"10"}`,
		"ns_per_op_samples": `{"denominator":"benchmark runs","population":"BenchmarkCommands/N1000/ShowOne runs in datum/internal/benchmarks","unit":"ns/op","values":[` +
			`{"line":5,"name":"run 1","run":1,"value":662256438},{"line":6,"name":"run 2","run":2,"value":665484958},{"line":7,"name":"run 3","run":3,"value":667107208}]}`,
	} {
		if got := text(show[key]); got != want {
			t.Errorf("%s:\n got %s\nwant %s", key, got, want)
		}
	}
	todo := bench(t, out, "BenchmarkCommands/N1000/Todo")
	// Sorted 1397100625, 1400206709, 1405629541: the median is run 3, not run 2.
	if got := text(todo["ns_per_op"].(map[string]any)["value"]); got != "1400206709" {
		t.Errorf("Todo ns/op median = %s, want 1400206709", got)
	}
	if got := text(todo["ns_per_op"].(map[string]any)["median_of_runs"]); got != "[3]" {
		t.Errorf("Todo median_of_runs = %s, want [3]", got)
	}
	// Custom metrics are read by the same rule, with the unit as printed.
	if got := text(todo["json-B_per_op"].(map[string]any)["unit"]); got != `"json-B/op"` {
		t.Errorf("json-B/op unit = %s", got)
	}
	if got := text(out["benchmarks"]); got != `["BenchmarkCommands/N1000/ShowOne","BenchmarkCommands/N1000/Todo"]` {
		t.Errorf("benchmarks = %s", got)
	}
	if got := text(out["conditions"]); got != `{"cpu":["Apple M4"],"goarch":["arm64"],"goos":["darwin"],"pkg":["datum/internal/benchmarks"]}` {
		t.Errorf("conditions = %s", got)
	}
}

// An even count takes the exact mean of the two middle runs and names both.
func TestEvenCountMedianIsExactMeanOfMiddle(t *testing.T) {
	out := run(t, "pkg: p\n"+
		"BenchmarkX \t 10\t 2.25 ns/op\n"+
		"BenchmarkX \t 10\t 9 ns/op\n"+
		"BenchmarkX \t 10\t 1.5 ns/op\n"+
		"BenchmarkX \t 10\t 0.5 ns/op\n")
	med := bench(t, out, "BenchmarkX")["ns_per_op"].(map[string]any)
	if got := text(med["value"]); got != "1.875" {
		t.Errorf("median = %s, want 1.875", got)
	}
	if got := text(med["median_of_runs"]); got != "[1,3]" {
		t.Errorf("median_of_runs = %s, want [1,3]", got)
	}
	if got := med["rule"]; got != "mean of the two middle values of 4 runs sorted by value" {
		t.Errorf("rule = %v", got)
	}
	// procs 1: go prints no suffix, and no cpu header was given, so none is recorded.
	if got := text(bench(t, out, "BenchmarkX")["conditions"]); got != `{"pkg":"p","procs":"1"}` {
		t.Errorf("conditions = %s", got)
	}
}

// Without -benchmem there is no B/op and no allocs/op, and no key claims one.
func TestMissingBenchmemColumnsAreAbsent(t *testing.T) {
	b := bench(t, run(t, "BenchmarkY/a/b-8 \t 100\t 42 ns/op\n"), "BenchmarkY/a/b")
	for _, key := range []string{"B_per_op", "allocs_per_op", "B_per_op_samples", "allocs_per_op_samples"} {
		if v, ok := b[key]; ok {
			t.Errorf("%s present without -benchmem: %s", key, text(v))
		}
	}
	if got := text(b["ns_per_op"].(map[string]any)["value"]); got != "42" {
		t.Errorf("ns/op = %s", got)
	}
	if got := text(b["ns_per_op"].(map[string]any)["population"]); got != `"median of BenchmarkY/a/b runs"` {
		t.Errorf("population without a pkg header = %s", got)
	}
}

// A second package's header replaces the first's; a cpu line printed only
// for the first package must not be credited to the second.
func TestConditionsFollowEachPackageHeader(t *testing.T) {
	out := run(t, "goos: linux\ngoarch: amd64\npkg: a\ncpu: Fast CPU\nBenchmarkA-4 \t 1\t 5 ns/op\nPASS\nok  \ta\t0.1s\n"+
		"goos: linux\ngoarch: amd64\npkg: b\nBenchmarkB-4 \t 1\t 6 ns/op\nPASS\nok  \tb\t0.1s\n")
	if got := text(bench(t, out, "BenchmarkB")["conditions"]); got != `{"goarch":"amd64","goos":"linux","pkg":"b","procs":"4"}` {
		t.Errorf("BenchmarkB conditions = %s", got)
	}
	if got := text(out["conditions"]); got != `{"cpu":["Fast CPU"],"goarch":["amd64"],"goos":["linux"],"pkg":["a","b"]}` {
		t.Errorf("conditions = %s", got)
	}
}

// Every refusal names its line. The first row is the control.
func TestMalformedInputIsRefusedWithItsLine(t *testing.T) {
	const good = "pkg: p\nBenchmarkX-8 \t 10\t 5 ns/op\t 3 B/op\n"
	if _, err := measure(strings.NewReader(good)); err != nil {
		t.Fatalf("control refused: %v", err)
	}
	for name, tc := range map[string]struct{ input, want string }{
		"name only":             {good + "BenchmarkX-8 \t\n", "line 3: malformed"},
		"no results":            {good + "BenchmarkX-8 \t 10\n", "line 3: malformed"},
		"odd value/unit pairs":  {good + "BenchmarkX-8 \t 10\t 5 ns/op\t 3\n", "line 3: malformed"},
		"value not a number":    {good + "BenchmarkX-8 \t 10\t fast ns/op\t 3 B/op\n", "line 3: malformed"},
		"unit twice":            {good + "BenchmarkX-8 \t 10\t 5 ns/op\t 6 ns/op\n", "line 3: malformed"},
		"stray output":          {good + "hello from the benchmark\n", "line 3: not a line"},
		"failure":               {good + "--- FAIL: BenchmarkX\n", "line 3: the run reports a failure"},
		"FAIL summary":          {good + "FAIL\tp\t0.1s\n", "line 3: the run reports a failure"},
		"repeat lost a unit":    {good + "BenchmarkX-8 \t 10\t 5 ns/op\n", "line 3: BenchmarkX reports units"},
		"repeat changed procs":  {good + "BenchmarkX-4 \t 10\t 5 ns/op\t 3 B/op\n", "line 3: BenchmarkX ran with GOMAXPROCS"},
		"repeat changed pkg":    {good + "goos: x\npkg: q\nBenchmarkX-8 \t 10\t 5 ns/op\t 3 B/op\n", "line 5: BenchmarkX ran under conditions"},
		"unit key collision":    {"BenchmarkX \t 1\t 5 a/b\t 6 a_per_b\n", "line 1: BenchmarkX: unit"},
		"unit names a set key":  {"BenchmarkX \t 1\t 5 x\t 6 x_samples\n", "line 1: BenchmarkX: unit"},
		"set key names a unit":  {"BenchmarkX \t 1\t 5 x_samples\t 6 x\n", "line 1: BenchmarkX: unit"},
		"unit named runs":       {"BenchmarkX \t 1\t 5 runs\n", "line 1: BenchmarkX: unit"},
		"no results at all":     {"pkg: p\nPASS\n", "no benchmark result lines"},
		"go test -json refused": {"{\"Action\":\"output\"}\n", "line 1: not a line"},
	} {
		_, err := measure(strings.NewReader(tc.input))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got error %v, want one containing %q", name, err, tc.want)
		}
	}
}

// Log blocks go test prints under --- BENCH are skipped as a block, and only
// while indented: the next unindented line is parsed again.
func TestBenchLogBlockIsBounded(t *testing.T) {
	out := run(t, "BenchmarkX \t 1\t 5 ns/op\n--- BENCH: BenchmarkX\n    x_test.go:9: note\nBenchmarkX \t 1\t 7 ns/op\n")
	if got := text(bench(t, out, "BenchmarkX")["runs"].(map[string]any)["value"]); got != "2" {
		t.Errorf("runs = %s, want 2", got)
	}
	for input, line := range map[string]string{
		"BenchmarkX \t 1\t 5 ns/op\n--- BENCH: BenchmarkX\n    note\nstray\n":           "line 4",
		"BenchmarkX \t 1\t 5 ns/op\n--- BENCH: BenchmarkX\n    note\nPASS\n    stray\n": "line 5",
	} {
		if _, err := measure(strings.NewReader(input)); err == nil || !strings.Contains(err.Error(), line) {
			t.Errorf("a line after the log block closed must be parsed; want %s refused, got %v", line, err)
		}
	}
}
