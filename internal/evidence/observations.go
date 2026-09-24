package evidence

// Invocation observations and output binding live here. Cross-invocation
// comparability lives in comparable.go.

import (
	"context"
	"errors"
	"fmt"
	"path"
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
		Instrument:         env.InstrumentRef,
		Execution:          env.ExecutionSourceIdentity,
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
	for _, out := range outs {
		if out.Content != nil && strings.HasPrefix(out.Content.MediaType, "image/") {
			o.ImageOutput = true
		}
	}
	runDir := RunDirIn(r.artifactDir(), env.InvocationID)

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
	if ref.Content != nil {
		// Only the matched run-dir path, then the store. Seal admission proved
		// these bytes were this run's output (its captured blobs or its run
		// directory) and published them, so the store holds THIS run's admitted
		// bytes under their digest. The run-dir path is the output's logical
		// name: whosaidso run stages outside the project and never writes there, so
		// the store copy is the only one. Older runs and hand-placed outputs
		// may still have bytes at the path, and they are read there first.
		pin := *ref.Content
		pin.Locators, ref.Content = []model.Locator{{Path: at}}, &pin
	}
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

// RunDir is the project-relative run directory that names one invocation's
// outputs in the default artifact store. Production names the project's store: RunDirIn.
func RunDir(invocation model.ID) string {
	return RunDirIn(DefaultArtifactDir, invocation)
}

// RunDirIn is the project-relative run directory that names one invocation's
// outputs, inside the project-relative artifact store artifactDir. It is a
// logical name that binds an output to its invocation (R9), independent of
// where the bytes physically are: write.Run stages outside the project and
// admission publishes by digest.
func RunDirIn(artifactDir string, invocation model.ID) string {
	return path.Join(artifactDir, "runs", string(invocation))
}

// OriginRunOutput marks bytes admission already holds as one invocation's own
// output (its captured blob or its run directory) and verified in memory.
const OriginRunOutput Origin = "run-output"

// RunOutput verifies bytes admission proved to be this invocation's own output
// against that output's reference, applying the checks readContent applies to
// bytes it reads: declared path syntax, the byte limit, the pin's digest and
// length, the LFS-pointer refusal and the media-type check. It reads nothing,
// so the bytes cannot be swapped for a same-digest copy found elsewhere, and
// admission publishes exactly what it proved. A run output is content-pinned
// only; one that also carries a git pin must go through Resolve to corroborate it.
func (r *Resolver) RunOutput(ref model.ArtifactRef, b []byte) (ResolvedArtifact, error) {
	if err := model.ValidateArtifactRef(ref, "artifact"); err != nil {
		return ResolvedArtifact{}, err
	}
	if err := r.checkPaths(ref); err != nil {
		return ResolvedArtifact{}, err
	}
	if ref.Content == nil || ref.Git != nil || len(ref.Content.Locators) == 0 {
		return ResolvedArtifact{}, fault("invalid-field", "artifact", "a run output is a content pin with a locator and no git pin")
	}
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
	if err := checkMediaType(b, ref.Content.MediaType); err != nil {
		return ResolvedArtifact{}, err
	}
	return ResolvedArtifact{Ref: ref, Bytes: b, SHA256: ref.Content.SHA256, Length: ref.Content.Length,
		MediaType: ref.Content.MediaType, Origin: OriginRunOutput, DeclaredPath: ref.Content.Locators[0].Path}, nil
}

// matchOutput pairs a criterion selector with an output by the path each
// declares. R8.3/R9: the contract path resolves in THIS run's own directory, so
// out/result.json names runs/<this-id>/out/result.json and never another run's
// file; a contract path that would leave that directory names nothing there.
// That is the ONLY form: an output declared at the bare contract path names a
// file any run, or the criterion's own example, could have put there. Ambiguity
// is refused rather than resolved by position, since which of two same-path
// outputs was meant is not something order can answer. A path not in canonical
// form names nothing: refused, not normalized, so no spelling can reach another
// run's directory. It returns the matched path.
func matchOutput(outs []model.ArtifactRef, runDir string, want model.ArtifactRef) (model.ArtifactRef, string, string) {
	wanted := map[string]bool{}
	var odd []string
	for _, p := range declaredPaths(want) {
		if path.Clean(p) != p {
			odd = append(odd, p)
			continue
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
