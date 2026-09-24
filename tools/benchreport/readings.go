// Readings: parsed samples restated in the shape a WhoSaidSo criterion can select.
//
// What belongs here is grouping samples by benchmark name, refusing groups
// whose runs do not describe the same measurement, and the median. Nothing
// here reads text (parse.go does) and nothing here judges a number: the
// threshold belongs to a criterion.
//
// The shape follows tools/archtree/readings.go for the same reason: WhoSaidSo's
// evaluator takes a reading's unit, population and denominator from the
// selected object or its immediate parent, and reads "value" or "values"
// there. Every pointer is by name (the benchmark name, a unit-derived key),
// never by position, so a benchmark added ahead of another cannot re-aim a
// frozen criterion.

package main

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

const denomRuns = "benchmark runs"

// scalar is the median of one metric over a benchmark's runs. MedianOfRuns
// names the runs (1-based, in input order) the median was taken from: one run
// for an odd count, the two middle runs for an even count.
type scalar struct {
	Unit         string      `json:"unit"`
	Population   string      `json:"population"`
	Denominator  string      `json:"denominator"`
	Statistic    string      `json:"statistic"`
	Rule         string      `json:"rule"`
	MedianOfRuns []int       `json:"median_of_runs"`
	Value        json.Number `json:"value"`
}

// set is every run's value of one metric.
type set struct {
	Unit        string      `json:"unit"`
	Population  string      `json:"population"`
	Denominator string      `json:"denominator"`
	Values      []runNumber `json:"values"`
}

// runNumber is one run. Name is what the evaluator shows when this member
// fails; Run is the same number for a program.
type runNumber struct {
	Name  string      `json:"name"`
	Run   int         `json:"run"`
	Line  int         `json:"line"`
	Value json.Number `json:"value"`
}

type count struct {
	Unit        string `json:"unit"`
	Population  string `json:"population"`
	Denominator string `json:"denominator"`
	Value       int    `json:"value"`
}

type report struct {
	Instrument string `json:"instrument"`
	// Conditions lists, per header key, every distinct value the input
	// printed, in order. A key go test did not print is absent, not guessed.
	Conditions map[string][]string `json:"conditions"`
	Benchmarks []string            `json:"benchmarks"`
	Readings   struct {
		ByBenchmark map[string]map[string]any `json:"by_benchmark"`
	} `json:"readings"`
}

// metricKey names a unit's reading: "/" becomes "_per_", so ns/op is ns_per_op.
func metricKey(unit string) string { return strings.ReplaceAll(unit, "/", "_per_") }

func buildReport(samples []sample) (report, error) {
	var r report
	r.Instrument = "benchreport"
	r.Conditions = map[string][]string{}
	r.Benchmarks = []string{}
	r.Readings.ByBenchmark = map[string]map[string]any{}
	if len(samples) == 0 {
		return r, fmt.Errorf("the input holds no benchmark result lines")
	}
	groups := map[string][]sample{}
	for _, s := range samples {
		if _, ok := groups[s.Name]; !ok {
			r.Benchmarks = append(r.Benchmarks, s.Name)
		}
		groups[s.Name] = append(groups[s.Name], s)
		for _, k := range conditionKeys {
			if v, ok := s.Conditions[k]; ok && !contains(r.Conditions[k], v) {
				r.Conditions[k] = append(r.Conditions[k], v)
			}
		}
	}
	for _, name := range r.Benchmarks {
		b, err := benchmarkReadings(name, groups[name])
		if err != nil {
			return report{}, err
		}
		r.Readings.ByBenchmark[name] = b
	}
	return r, nil
}

// benchmarkReadings refuses a group whose runs were not the same measurement:
// different conditions, a different GOMAXPROCS, or a different set of units.
// Merging those into one median would answer a question nobody asked.
func benchmarkReadings(name string, runs []sample) (map[string]any, error) {
	first := runs[0]
	for _, s := range runs[1:] {
		if s.Procs != first.Procs {
			return nil, fmt.Errorf("line %d: %s ran with GOMAXPROCS %s here and %s at line %d; one reading cannot hold both",
				s.Line, name, s.Procs, first.Procs, first.Line)
		}
		if !sameMap(s.Conditions, first.Conditions) {
			return nil, fmt.Errorf("line %d: %s ran under conditions %v here and %v at line %d; one reading cannot hold both",
				s.Line, name, s.Conditions, first.Conditions, first.Line)
		}
		if units(s) != units(first) {
			return nil, fmt.Errorf("line %d: %s reports units [%s] here and [%s] at line %d; a run would be missing from a set",
				s.Line, name, units(s), units(first), first.Line)
		}
	}
	pop := name + " runs"
	if pkg, ok := first.Conditions["pkg"]; ok {
		pop += " in " + pkg
	}
	cond := map[string]string{"procs": first.Procs}
	for k, v := range first.Conditions {
		cond[k] = v
	}
	out := map[string]any{
		"conditions": cond,
		"runs":       count{Unit: "runs", Population: pop, Denominator: denomRuns, Value: len(runs)},
	}
	metrics := append([]metric{{Unit: "iterations"}}, first.Metrics...)
	for _, m := range metrics {
		key := metricKey(m.Unit)
		if _, taken := out[key]; taken {
			return nil, fmt.Errorf("line %d: %s: unit %q names reading %q, which is already taken", first.Line, name, m.Unit, key)
		}
		if _, taken := out[key+"_samples"]; taken {
			return nil, fmt.Errorf("line %d: %s: unit %q names reading %q, which is already taken", first.Line, name, m.Unit, key+"_samples")
		}
		vals := make([]runNumber, len(runs))
		for i, s := range runs {
			vals[i] = runNumber{Name: fmt.Sprintf("run %d", i+1), Run: i + 1, Line: s.Line, Value: json.Number(valueOf(s, m.Unit))}
		}
		med, err := median(m.Unit, vals)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", name, err)
		}
		med.Population = "median of " + pop
		med.Denominator = denomRuns
		out[key] = med
		out[key+"_samples"] = set{Unit: m.Unit, Population: pop, Denominator: denomRuns, Values: vals}
	}
	return out, nil
}

func valueOf(s sample, unit string) string {
	if unit == "iterations" {
		return s.Iterations
	}
	for _, m := range s.Metrics {
		if m.Unit == unit {
			return m.Value
		}
	}
	return "" // unreachable: units() agreed for every run
}

// median sorts by exact value (ties keep input order). An odd count takes the
// middle run's own printed text; an even count takes the exact mean of the two
// middle runs, which is a finite decimal because both are.
func median(unit string, vals []runNumber) (scalar, error) {
	type ranked struct {
		run int
		rat *big.Rat
		txt json.Number
	}
	rs := make([]ranked, len(vals))
	for i, v := range vals {
		q, ok := new(big.Rat).SetString(string(v.Value))
		if !ok {
			return scalar{}, fmt.Errorf("line %d: %q is not a number", v.Line, v.Value)
		}
		rs[i] = ranked{v.Run, q, v.Value}
	}
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].rat.Cmp(rs[j].rat) < 0 })
	n := len(rs)
	out := scalar{Unit: unit, Statistic: "median"}
	if n%2 == 1 {
		mid := rs[n/2]
		out.Rule = fmt.Sprintf("middle value of %d runs sorted by value", n)
		out.MedianOfRuns, out.Value = []int{mid.run}, mid.txt
		return out, nil
	}
	lo, hi := rs[n/2-1], rs[n/2]
	mean := new(big.Rat).Add(lo.rat, hi.rat)
	mean.Quo(mean, big.NewRat(2, 1))
	out.Rule = fmt.Sprintf("mean of the two middle values of %d runs sorted by value", n)
	out.MedianOfRuns = []int{lo.run, hi.run}
	sort.Ints(out.MedianOfRuns)
	out.Value = json.Number(decimal(mean))
	return out, nil
}

// decimal prints a rational with a finite decimal expansion exactly.
func decimal(q *big.Rat) string {
	for prec := 0; ; prec++ {
		scaled := new(big.Rat).Mul(q, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(prec)), nil)))
		if scaled.IsInt() {
			return q.FloatString(prec)
		}
	}
}

func units(s sample) string {
	u := make([]string, len(s.Metrics))
	for i, m := range s.Metrics {
		u[i] = m.Unit
	}
	sort.Strings(u)
	return strings.Join(u, " ")
}

func sameMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
