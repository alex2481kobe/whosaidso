// Parsing: `go test -bench` text output turned into samples, one per result
// line, with the header conditions in force when each line was printed.
//
// What belongs here is reading lines and refusing any line this parser does
// not understand, with its line number. Nothing here computes a median or
// shapes a reading (readings.go does), and nothing here judges a number.
//
// The parser is strict on purpose. Every line is either a result line, a
// header line, or one of the few fixed lines go test prints around them.
// Anything else stops the parse: a line skipped because it looked unfamiliar
// could have been a result this instrument then reports as absent.

package main

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// conditionKeys are the header lines go test prints before a package's
// results. They are recorded as printed and never inferred when missing.
var conditionKeys = []string{"goos", "goarch", "pkg", "cpu"}

// sample is one result line: one run of one benchmark.
type sample struct {
	Line       int
	Name       string // as printed, minus the -GOMAXPROCS suffix
	Procs      string // the stripped suffix, "1" when go printed none
	Iterations string
	Metrics    []metric // in printed order
	Conditions map[string]string
}

type metric struct {
	Unit  string
	Value string // exact decimal text as printed
}

var (
	// A result line: name, whitespace, iteration count, then value/unit pairs.
	// go test replaces spaces in sub-benchmark names with underscores, so a
	// name never contains whitespace.
	resultLine = regexp.MustCompile(`^(Benchmark\S*)\s+(\d+)\s+(.*)$`)
	procsTail  = regexp.MustCompile(`^(.*)-(\d+)$`)
	// JSON number grammar: a value that is not a JSON number cannot be
	// selected as one, so it is refused here rather than printed as a string.
	jsonNumber = regexp.MustCompile(`^-?(0|[1-9]\d*)(\.\d+)?([eE][+-]?\d+)?$`)
	// Fixed lines go test prints around results.
	okLine      = regexp.MustCompile(`^ok\s+\S+\s+\S+`)
	noTestsLine = regexp.MustCompile(`^\?\s+\S+\s+\[no test files\]$`)
)

// parse reads go test output. It returns every result line as a sample, in
// input order, or the first line it refuses.
func parse(r io.Reader) ([]sample, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	cond := map[string]string{}
	var out []sample
	inLog := false // inside a --- BENCH/--- SKIP block, whose lines are indented
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), "\r")
		if inLog && (strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")) {
			continue
		}
		inLog = false
		switch {
		case strings.TrimSpace(line) == "":
			continue
		case strings.HasPrefix(line, "Benchmark"):
			s, err := parseResult(line, cond)
			if err != nil {
				return nil, fmt.Errorf("line %d: %v: %q", n, err, line)
			}
			s.Line = n
			out = append(out, s)
		case line == "PASS", okLine.MatchString(line), noTestsLine.MatchString(line),
			line == "testing: warning: no tests to run":
			continue
		case strings.HasPrefix(line, "--- BENCH: "), strings.HasPrefix(line, "--- SKIP: "):
			inLog = true
		case strings.HasPrefix(line, "FAIL"), strings.HasPrefix(line, "--- FAIL"):
			return nil, fmt.Errorf("line %d: the run reports a failure, so its results are not a clean measurement: %q", n, line)
		default:
			key, value, isHeader := header(line)
			if !isHeader {
				return nil, fmt.Errorf("line %d: not a line go test -bench prints: %q", n, line)
			}
			if key == "goos" {
				// go test prints goos first in each package's header, so it
				// starts a new set of conditions: a cpu line from the previous
				// package must not carry over to one that printed none.
				cond = map[string]string{}
			}
			cond[key] = value
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func header(line string) (key, value string, ok bool) {
	for _, k := range conditionKeys {
		if v, found := strings.CutPrefix(line, k+": "); found && strings.TrimSpace(v) != "" {
			return k, strings.TrimSpace(v), true
		}
	}
	return "", "", false
}

func parseResult(line string, cond map[string]string) (sample, error) {
	m := resultLine.FindStringSubmatch(line)
	if m == nil {
		return sample{}, fmt.Errorf("malformed benchmark line: expected a name, an iteration count and results")
	}
	s := sample{Name: m[1], Procs: "1", Iterations: m[2], Conditions: map[string]string{}}
	if p := procsTail.FindStringSubmatch(m[1]); p != nil {
		s.Name, s.Procs = p[1], p[2]
	}
	for k, v := range cond {
		s.Conditions[k] = v
	}
	fields := strings.Fields(m[3])
	if len(fields) == 0 || len(fields)%2 != 0 {
		return sample{}, fmt.Errorf("malformed benchmark line: results are not value/unit pairs")
	}
	seen := map[string]bool{}
	for i := 0; i < len(fields); i += 2 {
		value, unit := fields[i], fields[i+1]
		if !jsonNumber.MatchString(value) {
			return sample{}, fmt.Errorf("malformed benchmark line: %q is not a number", value)
		}
		if jsonNumber.MatchString(unit) {
			return sample{}, fmt.Errorf("malformed benchmark line: unit %q is a number", unit)
		}
		if seen[unit] {
			return sample{}, fmt.Errorf("malformed benchmark line: unit %q appears twice", unit)
		}
		seen[unit] = true
		s.Metrics = append(s.Metrics, metric{Unit: unit, Value: value})
	}
	return s, nil
}
