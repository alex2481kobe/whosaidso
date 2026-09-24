// Command benchreport is an instrument that restates go test -bench output as
// readings a WhoSaidSo criterion can select. It reports measurements and does not
// judge them: whether 660ms is fast enough is a criterion's question, frozen
// before the run, never this program's.
//
// Input is go test's text output, on stdin or from the file named by -in.
// Output is JSON on stdout. Any line it does not understand is refused with
// its line number and exit 1; nothing is skipped silently.
//
// SELECT FROM /readings/by_benchmark/<name>/, with / in the name written ~1
// (JSON pointer), and the -GOMAXPROCS suffix removed:
//
//	.../ns_per_op, .../B_per_op, .../allocs_per_op   median over the runs
//	.../ns_per_op_samples (and so on)                every run, as a set
//	.../runs                                         how many runs there were
//	.../iterations, .../iterations_samples           b.N per run
//
// Every other unit a benchmark reports (b.ReportMetric) is read the same way,
// its key being the unit with each / written _per_ (json-B/op is
// json-B_per_op). Each reading states its unit ("ns/op", "B/op",
// "allocs/op"), population ("median of <name> runs in <pkg>" for a median,
// "<name> runs in <pkg>" for a set) and denominator ("benchmark runs") on the
// object holding the value. A median also says which it is: median_of_runs
// names the run (odd count) or the two middle runs whose exact mean it is
// (even count), numbered from 1 in input order. For a set, the population
// selector is the same pointer plus /values.
//
// A benchmark that did not run, or a unit it did not report (no -benchmem and
// no b.ReportAllocs, so no B/op), has no key at all. A criterion selecting it
// evaluates UNKNOWN, never zero. /conditions holds each header go test printed
// (goos, goarch, pkg, cpu) with every distinct value seen; each benchmark's
// own conditions, including procs, are under its conditions member.
//
// REFUSED, loudly with the line number: a Benchmark line without an iteration
// count or with results that are not number/unit pairs; a value that is not a
// JSON number; a unit given twice on one line; runs of one benchmark that
// disagree on GOMAXPROCS, header conditions or the set of units reported (a
// set would silently miss a run); FAIL or --- FAIL anywhere; any line that is
// not one go test -bench prints; input with no result lines at all.
//
// VALIDATED by known-answer tests in known_answer_test.go (each
// mutation-checked); criterion_test.go puts readings through WhoSaidSo's own
// evidence.Observe and evidence.Evaluate.
//
// BLIND TO, and this matters more than the numbers it prints:
//
//   - machine noise. Two runs on the same commit differ; a median of three
//     narrows that and does not remove it. It reports no spread statistic, so
//     a criterion on the median cannot tell a tight result from a wild one.
//     The samples are there for a criterion that needs them.
//   - thermal state, power mode and background load. A laptop that throttled
//     halfway, or a build running beside the benchmark, is invisible here.
//   - cold versus warm caches: file-system cache, CPU caches, a first run
//     that paid for page faults the rest did not. Each run is counted alike.
//   - the Go version, compiler flags, GOGC, GOAMD64 and every other setting
//     the go test header does not print. Only goos, goarch, pkg and cpu are
//     recorded, and only when printed.
//   - the code measured. A benchmark name is a label; renaming a benchmark,
//     or changing what it does under the same name, is invisible, and a
//     renamed benchmark is a new key, so a frozen criterion reads UNKNOWN.
//   - whether the benchmark measures what its name says: setup inside the
//     timed loop, dead-code elimination, a fixture smaller than intended.
//   - output from another tool. It parses go test text only, not
//     `go test -json`, and a benchmark that prints to stdout mid-line breaks
//     the result line, which is then refused, not repaired.
//   - a sub-benchmark whose own name ends in -<digits> when GOMAXPROCS is 1:
//     go prints no procs suffix then, and this strips the digits anyway.
//
// Usage:
//
//	go test -bench . -run '^$' -count 3 -benchmem ./pkg | go run ./tools/benchreport
//	go run ./tools/benchreport -in bench.txt
package main

// This command is three files. main.go is the command surface, parse.go reads
// go test text into samples, readings.go groups them and computes medians.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	in := flag.String("in", "", "read go test output from this file instead of stdin")
	flag.Parse()
	var r io.Reader = os.Stdin
	if *in != "" {
		f, err := os.Open(*in)
		if err != nil {
			fail(err)
		}
		defer f.Close()
		r = f
	}
	out, err := measure(r)
	if err != nil {
		fail(err)
	}
	fmt.Println(string(out))
}

// measure is the whole instrument short of printing, and what the tests run.
func measure(r io.Reader) ([]byte, error) {
	samples, err := parse(r)
	if err != nil {
		return nil, err
	}
	rep, err := buildReport(samples)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(rep, "", "  ")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "benchreport:", err)
	os.Exit(1)
}
