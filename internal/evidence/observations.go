package evidence

// Invocation observations and matching frozen selectors to actual outputs live here.
// Byte retrieval, selector interpretation, and criterion verdicts do not.
// This file stays below 200 lines because invocation-output binding is one responsibility.

import (
	"context"
	"errors"
	"fmt"
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

	result, why, err := r.readSelector(ctx, outs, c.Expression.ResultSelector)
	if err != nil {
		return Observation{}, err
	}
	if why != "" {
		o.Unavailable = "result selector: " + why
		return o, nil
	}
	o.Result = result

	population, why, err := r.readSelector(ctx, outs, c.Expression.Population.Selector)
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
func (r *Resolver) readSelector(ctx context.Context, outs []model.ArtifactRef, want model.ArtifactRef) (Reading, string, error) {
	match, why := matchOutput(outs, want)
	if why != "" {
		return Reading{}, why, nil
	}
	// The pin comes from the run's own output. The pointer comes from the
	// frozen criterion. That is what keeps a late edit to the criterion from
	// silently re-aiming at different bytes.
	ref := match
	ref.Selector = want.Selector
	resolved, err := r.Resolve(ctx, ref)
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

// matchOutput pairs a criterion selector with an output by the path each
// declares. Ambiguity is refused rather than resolved by position, since which
// of two same-path outputs was meant is not something order can answer.
func matchOutput(outs []model.ArtifactRef, want model.ArtifactRef) (model.ArtifactRef, string) {
	wanted := map[string]bool{}
	for _, p := range declaredPaths(want) {
		wanted[p] = true
	}
	if len(wanted) == 0 {
		return model.ArtifactRef{}, "the criterion selector declares no path to match an output against"
	}
	var hits []model.ArtifactRef
	for _, out := range outs {
		for _, p := range declaredPaths(out) {
			if wanted[p] {
				hits = append(hits, out)
				break
			}
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], ""
	case 0:
		paths := make([]string, 0, len(wanted))
		for p := range wanted {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		return model.ArtifactRef{}, "no output of this invocation declares " + strings.Join(paths, " or ")
	default:
		return model.ArtifactRef{}, fmt.Sprintf("%d outputs declare the selected path", len(hits))
	}
}

func faultCode(err error) string {
	var f *model.Fault
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
}
