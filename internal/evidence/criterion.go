package evidence

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"datum/internal/model"
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
	for _, selected := range []struct {
		name string
		read Reading
	}{
		{"result", o.Result}, {"selected population", o.Population},
	} {
		if why := metadataAgreement(c, selected.name, selected.read, selected.name == "result"); why != "" {
			m.Reason = why
			return m
		}
	}

	values, ok := o.Result.Scalars()
	if !ok {
		m.Reason = "result: " + o.Result.Reason
		return m
	}
	size, ok := o.Population.Size()
	if !ok {
		m.Reason = "population: " + o.Population.Reason
		return m
	}
	m.Compared, m.Population = len(values), size

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
	if c.Expression.Reducer != model.Count && len(values) != size {
		// all and any quantify over the declared population, so a result that
		// covers part of it answers a smaller question than the criterion asks.
		m.Reason = fmt.Sprintf("the result covers %d of the %d declared population members", len(values), size)
		return m
	}

	target, op := c.Expression.Target, c.Expression.Operator
	switch c.Expression.Reducer {
	case model.Count:
		count := numberScalar(json.Number(strconv.Itoa(len(values))))
		held, err := model.CompareScalars(count, op, target)
		if err != nil {
			m.Reason = "the count is not comparable with the target: " + err.Error()
			return m
		}
		m.Verdict = verdictOf(held)
		return m
	case model.All:
		absent := ""
		for i, v := range values {
			held, err := model.CompareScalars(v, op, target)
			if err != nil {
				// The reading exists but cannot be placed against the target, so
				// what it says about the criterion is unknown, not false.
				if absent == "" {
					absent = fmt.Sprintf("value %d is not comparable with the target: %s", i, err.Error())
				}
				continue
			}
			if !held {
				m.Verdict = False
				m.Reason = fmt.Sprintf("value %d does not satisfy the criterion", i)
				return m
			}
		}
		if absent != "" {
			m.Reason = absent
			return m
		}
		m.Verdict = True
		return m
	default: // model.Any
		absent := ""
		for i, v := range values {
			held, err := model.CompareScalars(v, op, target)
			if err != nil {
				if absent == "" {
					absent = fmt.Sprintf("value %d is not comparable with the target: %s", i, err.Error())
				}
				continue
			}
			if held {
				m.Verdict = True
				return m
			}
		}
		if absent != "" {
			m.Reason = absent
			return m
		}
		m.Verdict = False
		m.Reason = "no value satisfies the criterion"
		return m
	}
}

// metadataAgreement applies the same declaration rule to a reading and its
// members: omission is allowed, but unavailability and disagreement are not.
// Population members identify the denominator; their units need not be the
// result's measurement unit.
func metadataAgreement(c model.CriterionFix, label string, r Reading, result bool) string {
	for _, field := range []struct {
		name, expected string
		declared       model.Availability[string]
	}{
		{"unit", c.Expression.Unit, r.Unit},
		{"population", c.Expression.Population.Identity, r.Population},
		{"denominator", c.Expression.Population.Denominator, r.Denominator},
	} {
		if field.name == "unit" && !result {
			continue
		}
		check := func(at string, s model.Availability[string], present bool) string {
			if !present {
				return ""
			}
			if s.State != model.Known || s.Value == nil {
				return fmt.Sprintf("%s %s is unavailable: %s", at, field.name, s.Reason)
			}
			if *s.Value != field.expected {
				return fmt.Sprintf("%s %s mismatch: the criterion declares %s, the artifact states %s",
					at, field.name, quote(field.expected), quote(*s.Value))
			}
			return ""
		}
		if why := check(label, field.declared, field.declared.State != ""); why != "" {
			return why
		}
		for i, declared := range r.MemberMetadata {
			s, present := declared[field.name]
			if why := check(fmt.Sprintf("%s member %d", label, i), s, present); why != "" {
				return why
			}
		}
	}
	return ""
}

func verdictOf(b bool) Verdict {
	if b {
		return True
	}
	return False
}

// comparable checks that the family measured the same thing under the same
// conditions. Two runs whose effective configuration differs are two different
// measurements, and reporting one result for them would hide which one it came
// from. Values are compared, not the requested configuration, because what was
// asked for is not what ran.
func comparable(members []Observation) string {
	for i := 1; i < len(members); i++ {
		a, b := members[0], members[i]
		pair := string(a.InvocationRef.InvocationID) + " and " + string(b.InvocationRef.InvocationID)
		if why := mapDifference("effective configuration", a.ConfigEffective, b.ConfigEffective); why != "" {
			return pair + " are not comparable: " + why
		}
		if why := mapDifference("observed conditions", a.ConditionsObserved, b.ConditionsObserved); why != "" {
			return pair + " are not comparable: " + why
		}
	}
	return ""
}

func mapDifference(label string, a, b model.Availability[map[string]model.Availability[model.Scalar]]) string {
	switch {
	case a.State != model.Known && b.State != model.Known:
		// Neither run recorded it, so sameness is an assumption. It is a cheap
		// assumption to make and an expensive one to be wrong about.
		return "neither run observed its " + label
	case a.State != model.Known || b.State != model.Known:
		return "one run observed its " + label + " and the other did not"
	}
	left, right := *a.Value, *b.Value
	for _, name := range union(left, right) {
		lv, lok := left[name]
		rv, rok := right[name]
		switch {
		case !lok || !rok:
			// cameraPosition in one run and camera_pos in the other is the drift
			// the instrument declares its knob names to prevent: it makes two
			// comparable runs look different, or hides a real difference.
			return label + " names " + quote(name) + " in only one run"
		case lv.State != rv.State:
			return label + " for " + quote(name) + " was observed in only one run"
		case lv.State != model.Known:
			// Like model.SameActor, two unknowns cannot establish equality.
			return label + " for " + quote(name) + " was not observed in either run"
		case lv.State == model.Known:
			same, err := model.CompareScalars(*lv.Value, model.Equal, *rv.Value)
			if err != nil {
				return label + " for " + quote(name) + " is not comparable across the runs: " + err.Error()
			}
			if !same {
				return label + " for " + quote(name) + " differs between the runs"
			}
		}
	}
	return ""
}

func union(a, b map[string]model.Availability[model.Scalar]) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range []map[string]model.Availability[model.Scalar]{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	// Sorted so a refusal names the same knob every time it runs.
	sort.Strings(out)
	return out
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
