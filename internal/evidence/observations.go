package evidence

// Invocation observations, output binding, and cross-invocation comparability live here.

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"datum/internal/model"
)

// ---- observations --------------------------------------------------------

// Observation is one sealed invocation's contribution to a criterion family.
// Every measurable thing in it was read out of the artifact that invocation
// produced. There is no field here for a number an author reports separately,
// which is the point: there is nothing to transcribe and therefore nothing to
// drift.
type Observation struct {
	InvocationRef      model.InvocationRef
	CriterionRef       model.Availability[model.CriterionRef]
	Outcome            model.Availability[model.ProcessOutcome]
	ConfigEffective    model.Availability[map[string]model.Availability[model.Scalar]]
	ConditionsObserved model.Availability[map[string]model.Availability[model.Scalar]]
	Visual             model.Availability[model.VisualObservation]
	Result             Reading
	Population         Reading
	// Unavailable is why this observation has no reading at all. Nonblank means
	// the evaluation must refuse, never that the value was zero.
	Unavailable string
}

// Observe reads the frozen criterion's selectors out of the artifact THIS
// invocation produced.
//
// The criterion is fixed before the run, so its selector pins an artifact that
// states where a result lives. The bytes read are always the run's own output,
// matched by the declared path: reading a criterion's pinned example and calling
// it this run's result would report a number no instrument produced here.
func (r *Resolver) Observe(ctx context.Context, c model.CriterionFix, env model.InvocationEnvelope) (Observation, error) {
	if err := model.ValidateSchema(c); err != nil {
		return Observation{}, err
	}
	o := Observation{
		InvocationRef:      model.InvocationRef{Project: env.ExecutionSourceIdentity.Project, InvocationID: env.InvocationID},
		CriterionRef:       env.CriterionRef,
		Outcome:            env.Outcome,
		ConfigEffective:    env.ConfigEffective,
		ConditionsObserved: env.ConditionsObserved,
		Visual:             env.Visual,
	}
	if env.OutputRefs.State != model.Known || env.OutputRefs.Value == nil {
		o.Unavailable = "the invocation reports no observed outputs: " + env.OutputRefs.Reason
		return o, nil
	}
	outs := *env.OutputRefs.Value
	runDir := RunDir(env.InvocationID)

	result, why, err := r.readSelector(ctx, outs, runDir, c.Expression.ResultSelector)
	if err != nil {
		return Observation{}, err
	}
	if why != "" {
		o.Unavailable = "result selector: " + why
		return o, nil
	}
	o.Result = result

	population, why, err := r.readSelector(ctx, outs, runDir, c.Expression.Population.Selector)
	if err != nil {
		return Observation{}, err
	}
	if why != "" {
		o.Unavailable = "population selector: " + why
		return o, nil
	}
	o.Population = population
	return o, nil
}

// readSelector finds the output this selector names and reads it. A missing or
// unreadable artifact comes back as a reason, not an error, because the family
// still has to be evaluated with that observation counted and refused.
func (r *Resolver) readSelector(ctx context.Context, outs []model.ArtifactRef, runDir string, want model.ArtifactRef) (Reading, string, error) {
	match, at, why := matchOutput(outs, runDir, want)
	if why != "" {
		return Reading{}, why, nil
	}
	// The pin comes from the run's own output. The pointer comes from the
	// frozen criterion. That is what keeps a late edit to the criterion from
	// silently re-aiming at different bytes.
	ref := match
	ref.Selector = want.Selector
	res := r
	if strings.HasPrefix(at, runDir+"/") && ref.Content != nil {
		// Only the run's own directory may answer. A same-digest copy elsewhere,
		// such as the criterion's example in the store, is not this run's output.
		pin, own := *ref.Content, *r
		pin.Locators, ref.Content, own.noStore, res = []model.Locator{{Path: at}}, &pin, true, &own
	}
	resolved, err := res.Resolve(ctx, ref)
	if err != nil {
		if code := faultCode(err); code == "unavailable" || code == "io" {
			return Reading{}, err.Error(), nil
		}
		return Reading{}, "", err
	}
	reading, err := Select(resolved, want.Selector)
	if err != nil {
		return Reading{}, err.Error(), nil
	}
	return reading, "", nil
}

// RunDir is the project-relative directory write.Run gives one invocation.
func RunDir(invocation model.ID) string {
	return path.Join(DefaultArtifactDir, "runs", string(invocation))
}

// matchOutput pairs a criterion selector with an output by the path each
// declares. R8.3: the contract path resolves in THIS run's own directory, so
// out/result.json names runs/<this-id>/out/result.json and never another run's
// file; a contract path that would leave that directory names nothing there.
// An output declared at the contract path itself still matches (U07's form).
// Ambiguity is refused rather than resolved by position, since which of two
// same-path outputs was meant is not something order can answer. A path not in
// canonical form names nothing: refused, not normalized, so no spelling can
// slip past the runs/ prefix check. It returns the matched path.
func matchOutput(outs []model.ArtifactRef, runDir string, want model.ArtifactRef) (model.ArtifactRef, string, string) {
	wanted := map[string]bool{}
	var odd []string
	for _, p := range declaredPaths(want) {
		if path.Clean(p) != p {
			odd = append(odd, p)
			continue
		}
		if !strings.HasPrefix(p, DefaultArtifactDir+"/runs/") || strings.HasPrefix(p, runDir+"/") {
			wanted[p] = true
		}
		if joined := path.Join(runDir, p); strings.HasPrefix(joined, runDir+"/") {
			wanted[joined] = true
		}
	}
	if len(wanted) == 0 {
		return model.ArtifactRef{}, "", "the criterion selector declares no canonical path to match an output against " + strings.Join(odd, ", ")
	}
	var hits []model.ArtifactRef
	var at string
	for _, out := range outs {
		for _, p := range declaredPaths(out) {
			if wanted[p] {
				hits, at = append(hits, out), p
				break
			}
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], at, ""
	case 0:
		paths := make([]string, 0, len(wanted))
		for p := range wanted {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		return model.ArtifactRef{}, "", "no output of this invocation declares " + strings.Join(paths, " or ")
	default:
		return model.ArtifactRef{}, "", fmt.Sprintf("%d outputs declare the selected path", len(hits))
	}
}

func faultCode(err error) string {
	var f *model.Fault
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
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

// metadataAgreement keeps reading-wide and member declarations separate while
// applying one rule: omission is allowed, unavailability and disagreement are not.
func metadataAgreement(c model.CriterionFix, label string, r Reading, result bool) string {
	fields := map[string]model.Availability[string]{}
	for key, s := range map[string]model.Availability[string]{"unit": r.Unit, "population": r.Population, "denominator": r.Denominator} {
		if s.State != "" {
			fields[key] = s
		}
	}
	if why := metadataFields(c, label, fields, result); why != "" {
		return why
	}
	for i, declared := range r.MemberMetadata {
		if why := metadataFields(c, fmt.Sprintf("%s member %d", label, i), declared, result); why != "" {
			return why
		}
	}
	return ""
}

func metadataFields(c model.CriterionFix, label string, fields map[string]model.Availability[string], result bool) string {
	for _, field := range []struct{ name, expected string }{
		{"unit", c.Expression.Unit},
		{"population", c.Expression.Population.Identity},
		{"denominator", c.Expression.Population.Denominator},
	} {
		// Population members identify the denominator, not the measurement unit.
		if field.name == "unit" && !result {
			continue
		}
		s, present := fields[field.name]
		if !present {
			continue
		}
		if s.State != model.Known || s.Value == nil {
			return fmt.Sprintf("%s %s is unavailable: %s", label, field.name, s.Reason)
		}
		if *s.Value != field.expected {
			return fmt.Sprintf("%s %s mismatch: the criterion declares %s, the artifact states %s",
				label, field.name, quote(field.expected), quote(*s.Value))
		}
	}
	return ""
}
