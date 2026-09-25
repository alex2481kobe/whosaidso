package evidence

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// Verdict is three-valued on purpose. UNKNOWN is not a soft FALSE: it is the
// answer when the information needed to decide is absent, and it always carries
// the reason it is absent. Collapsing it into either direction is how a record
// ends up asserting something nobody measured.
type Verdict string

const (
	True    Verdict = "TRUE"
	False   Verdict = "FALSE"
	Unknown Verdict = "UNKNOWN"
)

// MemberResult is one observation's own verdict, kept whatever it says. A
// family lists every member it evaluated, including the failed and the stopped
// ones, so a later reader can see that nothing was dropped.
type MemberResult struct {
	InvocationRef model.InvocationRef
	Verdict       Verdict
	Reason        string
	Unit          string
	Compared      int // values the predicate was applied to
	Population    int // members the criterion declared it covers
}

// Evaluation is the result over the whole criterion family.
type Evaluation struct {
	Criterion model.CriterionRef
	Verdict   Verdict
	Reason    string
	Inclusion string
	Retry     string
	Members   []MemberResult
}

// Evaluate computes a criterion result from a frozen criterion and the
// observations in its family.
//
// The family is combined conjunctively: the criterion holds when every included
// observation satisfies it. That is what makes a counterexample impossible to
// retry away. A run that found 0.2mm and a later run that found zero do not
// average, and the later one does not supersede the earlier one. The family
// answers FALSE and the contradiction stays visible.
//
// Nothing here consults an authored summary of a run. Every number came out of
// the artifact the run produced.
func Evaluate(c model.CriterionFix, obs []Observation) (Evaluation, error) {
	// A malformed criterion is an error, not an UNKNOWN. UNKNOWN means we could
	// not learn something. This one means the question itself was never well formed.
	if err := model.ValidateSchema(c); err != nil {
		return Evaluation{}, err
	}
	id := model.CriterionRef{Claim: c.Claim, CriterionID: c.CriterionID, Revision: c.Revision}
	ev := Evaluation{Criterion: id, Inclusion: c.Policy.Inclusion, Retry: c.Policy.Retry}

	members := append([]Observation(nil), obs...)
	sort.SliceStable(members, func(i, j int) bool {
		if members[i].InvocationRef.Project != members[j].InvocationRef.Project {
			return members[i].InvocationRef.Project < members[j].InvocationRef.Project
		}
		return members[i].InvocationRef.InvocationID < members[j].InvocationRef.InvocationID
	})
	seen := map[model.InvocationRef]bool{}
	for _, o := range members {
		if seen[o.InvocationRef] {
			return Evaluation{}, fault("criterion", "observations",
				"the same invocation appears twice in the family: "+string(o.InvocationRef.InvocationID))
		}
		seen[o.InvocationRef] = true
		if o.CriterionRef.State == model.Known && o.CriterionRef.Value != nil && *o.CriterionRef.Value != id {
			// Membership is the criterion reference the run carried. A caller
			// handing over a foreign observation has built the wrong family.
			return Evaluation{}, fault("criterion", "observations",
				"invocation "+string(o.InvocationRef.InvocationID)+" was run for a different criterion")
		}
	}

	if len(members) == 0 {
		ev.Verdict, ev.Reason = Unknown,
			"no local observation carries this criterion. A reference to someone else's result is context, not evidence"
		return ev, nil
	}

	for _, o := range members {
		ev.Members = append(ev.Members, evaluateMember(c, o))
	}

	// A definite counterexample is decided before comparability, because a run
	// that failed the criterion failed it on its own, without needing a second
	// run to compare against.
	for _, m := range ev.Members {
		if m.Verdict == False {
			ev.Verdict, ev.Reason = False, string(m.InvocationRef.InvocationID)+": "+m.Reason
			return ev, nil
		}
	}
	for _, m := range ev.Members {
		if m.Verdict == Unknown {
			ev.Verdict, ev.Reason = Unknown, string(m.InvocationRef.InvocationID)+": "+m.Reason
			return ev, nil
		}
	}
	if why := comparable(members); why != "" {
		// Every member passed, but they were not measuring the same thing, so
		// there is no single result to report.
		ev.Verdict, ev.Reason = Unknown, why
		return ev, nil
	}
	ev.Verdict = True
	return ev, nil
}

// evaluateMember is the whole executable vocabulary applied to one observation.
func evaluateMember(c model.CriterionFix, o Observation) MemberResult {
	m := MemberResult{InvocationRef: o.InvocationRef, Verdict: Unknown}
	switch {
	case o.Unavailable != "":
		m.Reason = o.Unavailable
		return m
	case o.CriterionRef.State != model.Known:
		m.Reason = "the invocation does not name the criterion it was run for: " + o.CriterionRef.Reason
		return m
	case o.Outcome.State != model.Known:
		// A killed observer leaves the run's outcome unobserved. It stays in the
		// family and it stays UNKNOWN. It cannot be dropped for being awkward.
		m.Reason = "the invocation was not observed to completion: " + o.Outcome.Reason
		return m
	}

	// A bare number is not a measurement. Whether it is comparable with the
	// threshold depends entirely on what it is counted in, and that has to come
	// from the artifact, not from the record that points at it.
	if o.Result.Unit.State != model.Known || o.Result.Unit.Value == nil {
		m.Reason = o.Result.Unit.Reason
		return m
	}
	m.Unit = *o.Result.Unit.Value
	// Reading-wide declarations and the denominator remain prerequisites for every value.
	result := o.Result
	result.MemberMetadata = nil
	for _, selected := range []struct {
		name string
		read Reading
	}{
		{"result", result}, {"selected population", o.Population},
	} {
		if why := metadataAgreement(c, selected.name, selected.read, selected.name == "result"); why != "" {
			m.Reason = why
			return m
		}
	}

	values, ok := o.Result.selectedValues()
	if !ok {
		m.Reason = "result: " + o.Result.Reason
		return m
	}
	size, ok := o.Population.Size()
	if !ok {
		m.Reason = "population: " + o.Population.Reason
		return m
	}
	m.Population = size
	if len(values) > size || (c.Expression.Reducer != model.Count && len(values) != size) {
		m.Reason = fmt.Sprintf("the result covers %d of the %d declared population members", len(values), size)
		return m
	}
	// A local failure disqualifies that value, not its independently usable siblings.
	issues, absent := resultIssues(c, o.Result, len(values))
	if absent != "" && c.Expression.Reducer != model.All {
		m.Reason = absent
		return m
	}

	if size == 0 {
		if c.Expression.EmptyResult == nil {
			// An empty result file and a clean run look identical from here.
			// Deciding between them is the author's job, before the run.
			m.Reason = "the declared population is empty and the criterion does not say what an empty population means"
			return m
		}
		m.Verdict = verdictOf(*c.Expression.EmptyResult)
		m.Reason = "the frozen criterion defines an empty population as " + strconv.FormatBool(*c.Expression.EmptyResult)
		return m
	}

	target, op := c.Expression.Target, c.Expression.Operator
	if c.Expression.Reducer == model.Count {
		count := numberScalar(json.Number(strconv.Itoa(len(values))))
		held, err := model.CompareScalars(count, op, target)
		if err != nil {
			m.Reason = "the count is not comparable with the target: " + err.Error()
			return m
		}
		m.Compared, m.Verdict = len(values), verdictOf(held)
		return m
	}
	for i, v := range values {
		if issues[i] != "" {
			continue
		}
		m.Compared++
		held, err := model.CompareScalars(v, op, target)
		if err != nil {
			if absent == "" {
				absent = fmt.Sprintf("member %d: %s is not comparable with the target: %s", i, scalarText(v), err.Error())
			}
			continue
		}
		if c.Expression.Reducer == model.All && !held {
			// An unnamed member is named by its index, never read as a value.
			m.Verdict, m.Reason = False, fmt.Sprintf("member %d: %s does not satisfy %s %s", i, scalarText(v), op, scalarText(target))
			if i < len(o.Result.MemberNames) && o.Result.MemberNames[i] != "" {
				m.Reason = fmt.Sprintf("%s: %s does not satisfy %s %s", printable(o.Result.MemberNames[i]), scalarText(v), op, scalarText(target))
			}
			return m
		}
		if c.Expression.Reducer == model.Any && held {
			m.Verdict = True
			return m
		}
	}
	if absent != "" {
		m.Reason = absent
		return m
	}
	m.Verdict = verdictOf(c.Expression.Reducer == model.All)
	if m.Verdict == False {
		m.Reason = "no value satisfies the criterion"
	}
	return m
}

// scalarText is a scalar as a reason shows it: numbers keep their decimal
// text, strings are quoted.
func scalarText(s model.Scalar) string {
	switch {
	case s.Number != nil:
		return string(*s.Number)
	case s.String != nil:
		return strconv.Quote(*s.String)
	case s.Bool != nil:
		return strconv.FormatBool(*s.Bool)
	}
	return "UNKNOWN"
}

// printable shows an artifact-stated name bare only when it cannot fake the
// rest of the reason; otherwise it is quoted.
func printable(name string) string {
	for _, r := range name {
		if !unicode.IsPrint(r) || r == '"' {
			return strconv.Quote(name)
		}
	}
	if strings.TrimSpace(name) != name || strings.Contains(name, ": ") {
		return strconv.Quote(name)
	}
	return name
}

// resultIssues checks each member through the same metadata rule as its reading.
// Only ALL can earn FALSE without using every value. ANY and COUNT still require
// complete member availability; population failures never become local exceptions.
func resultIssues(c model.CriterionFix, r Reading, n int) ([]string, string) {
	issues, first := make([]string, n), ""
	for i := range issues {
		if i < len(r.MemberMetadata) {
			issues[i] = metadataFields(c, fmt.Sprintf("result member %d", i), r.MemberMetadata[i], true)
		}
		if issues[i] == "" && i < len(r.MemberReasons) {
			issues[i] = r.MemberReasons[i]
		}
		if first == "" {
			first = issues[i]
		}
	}
	return issues, first
}

func verdictOf(b bool) Verdict {
	if b {
		return True
	}
	return False
}

// Describe renders an evaluation for a person. It states the verdict and, when
// the verdict is UNKNOWN, why: an UNKNOWN without a reason is the shape of
// answer this package exists to refuse.
func (e Evaluation) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s over %d observation(s)", e.Verdict, len(e.Members))
	if e.Reason != "" {
		fmt.Fprintf(&b, ": %s", e.Reason)
	}
	return b.String()
}
