package evidence

// Invocation observations and output binding live here. Cross-invocation
// comparability lives in comparable.go.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"whosaidso/internal/model"
)

// ---- observations --------------------------------------------------------

// Observation is one sealed invocation's contribution to a criterion family.
// Every measurable thing in it was read out of the artifact that invocation
// produced. There is no field here for a number an author reports separately,
// which is the point: there is nothing to transcribe and therefore nothing to
// drift.
type Observation struct {
	InvocationRef model.InvocationRef
	CriterionRef  model.Availability[model.CriterionRef]
	Instrument    model.RecordRef
	Execution     model.ExecutionIdentity
	// ImageOutput is whether any output declares an image media type: a run
	// that made a picture has visual conditions whether or not it reported them.
	ImageOutput        bool
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
// The criterion is fixed before the run, so its selector pins an example and
// names the output a result lives in. The bytes read are always the run's own
// output, matched by name among the outputs this invocation's seal lists and
// fetched by that output's digest: reading a criterion's pinned example and
// calling it this run's result would report a number no instrument produced
// here. Seal admission proved each listed output's bytes were in the run's own
// packet, so a same-digest copy in the store is those bytes.
func (r *Resolver) Observe(ctx context.Context, c model.CriterionFix, env model.InvocationEnvelope) (Observation, error) {
	if err := model.ValidateSchema(c); err != nil {
		return Observation{}, err
	}
	o := Observation{
		InvocationRef:      model.InvocationRef{Project: env.ExecutionSourceIdentity.Project, InvocationID: env.InvocationID},
		CriterionRef:       env.CriterionRef,
		Instrument:         env.InstrumentRef,
		Execution:          env.ExecutionSourceIdentity,
		Outcome:            env.Outcome,
		ConfigEffective:    env.ConfigEffective,
		ConditionsObserved: env.ConditionsObserved,
		Visual:             env.Visual,
	}
	if env.Outputs.State != model.Known || env.Outputs.Value == nil {
		o.Unavailable = "the invocation reports no observed outputs: " + env.Outputs.Reason
		return o, nil
	}
	outs := *env.Outputs.Value
	for _, out := range outs {
		if strings.HasPrefix(out.MediaType, "image/") {
			o.ImageOutput = true
		}
	}

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
func (r *Resolver) readSelector(ctx context.Context, outs []model.RunOutput, want model.ArtifactRef) (Reading, string, error) {
	match, why := matchOutput(outs, want)
	if why != "" {
		return Reading{}, why, nil
	}
	// The pin comes from the run's own output. The pointer comes from the
	// frozen criterion. That is what keeps a late edit to the criterion from
	// silently re-aiming at different bytes.
	ref := match.Ref()
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

// OriginRunOutput marks bytes admission already holds as one invocation's own
// output (its captured blob) and verified in memory.
const OriginRunOutput Origin = "run-output"

// RunOutput verifies bytes admission proved to be this invocation's own output
// against that output's pin, applying the checks readContent applies to bytes
// it reads: the byte limit, the digest and length, the LFS-pointer refusal and
// the media-type check. It reads nothing, so the bytes cannot be swapped for a
// same-digest copy found elsewhere, and admission publishes exactly what it proved.
func (r *Resolver) RunOutput(out model.RunOutput, b []byte) (ResolvedArtifact, error) {
	ref := out.Ref()
	if int64(len(b)) > r.maxBytes() {
		return ResolvedArtifact{}, fault("unavailable", "artifact.content", "the run output exceeds the resolver byte limit")
	}
	if err := agree(model.HashBytes(b), uint64(len(b)), *ref.Content); err != nil {
		return ResolvedArtifact{}, err
	}
	if ptr, ok := parseLFSPointer(b); ok {
		return ResolvedArtifact{}, fault("unavailable", "artifact.content",
			"the content pin names an LFS pointer, not its payload. The payload needs a content pin naming oid "+ptr.oid)
	}
	if err := checkMediaType(b, out.MediaType); err != nil {
		return ResolvedArtifact{}, err
	}
	return ResolvedArtifact{Ref: ref, Bytes: b, SHA256: out.SHA256, Length: out.Length,
		MediaType: out.MediaType, Origin: OriginRunOutput, DeclaredPath: out.Name}, nil
}

// matchOutput pairs a criterion selector with the run output it names. R8.3/R9:
// each path the selector declares is an output name inside THIS invocation, so
// out/result.json names this run's out/result.json and never another run's
// file or the criterion's own example. A declared path that is not a valid
// output name names nothing: refused, not normalized, so no spelling reaches a
// different output. Ambiguity is refused rather than resolved by position,
// since which of two named outputs was meant is not something order can answer.
func matchOutput(outs []model.RunOutput, want model.ArtifactRef) (model.RunOutput, string) {
	wanted := map[string]bool{}
	var odd []string
	for _, p := range declaredPaths(want) {
		if model.RunOutputName(p, "selector") != nil {
			odd = append(odd, p)
			continue
		}
		wanted[p] = true
	}
	if len(wanted) == 0 {
		return model.RunOutput{}, "the criterion selector declares no canonical output name to match " + strings.Join(odd, ", ")
	}
	var hits []model.RunOutput
	for _, out := range outs {
		if wanted[out.Name] {
			hits = append(hits, out)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], ""
	case 0:
		names := make([]string, 0, len(wanted))
		for p := range wanted {
			names = append(names, p)
		}
		sort.Strings(names)
		return model.RunOutput{}, "no output of this invocation is named " + strings.Join(names, " or ")
	default:
		return model.RunOutput{}, fmt.Sprintf("%d outputs of this invocation are named by the selector", len(hits))
	}
}

func faultCode(err error) string {
	var f *model.Fault
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
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
