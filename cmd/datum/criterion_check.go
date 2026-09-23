package main

// This file holds `datum criterion check`: one hypothetical completed run whose
// output is the candidate bytes, evaluated by evidence.Observe and
// evidence.Evaluate, the calls admission makes for each exact proof member.
// The candidate is read from a temporary directory outside the project, so
// nothing is written. Flag parsing lives in check.go; no rule lives here.

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"datum/internal/evidence"
	"datum/internal/model"
	"datum/internal/store"
)

// criterionCheck builds one hypothetical completed run whose output is the
// candidate, then asks evidence.Observe and evidence.Evaluate, the calls
// admission makes for each exact proof member.
func criterionCheck(ctx context.Context, project store.Project, events []model.Event, blob, output string, stdout io.Writer) error {
	var fix *model.CriterionFix
	for _, raw := range events {
		event, err := model.DecodeEvent(raw)
		if err != nil {
			return err
		}
		if f, ok := event.(*model.CriterionFix); ok {
			if fix != nil {
				return fmt.Errorf("criterion check takes one criterion.fix; the events hold more")
			}
			fix = f
		}
	}
	if fix == nil {
		return fmt.Errorf("the events hold no criterion.fix")
	}
	selectors := []model.ArtifactRef{fix.Expression.ResultSelector, fix.Expression.Population.Selector}
	var candidate []byte
	if output != "" {
		data, err := os.ReadFile(output)
		if err != nil {
			return err
		}
		candidate = data
	}
	scratch, err := os.MkdirTemp("", "datum-criterion-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	invocation, err := model.NewID(time.Now(), rand.Reader)
	if err != nil {
		return err
	}
	runDir := evidence.RunDirIn(project.ArtifactDir(), invocation)
	placed := map[string][]byte{}
	var outputs []model.ArtifactRef
	for i, selector := range selectors {
		bytes := candidate
		if bytes == nil {
			if bytes, err = criterionExample(ctx, project, selector, blob, scratch); err != nil {
				return fmt.Errorf("the criterion's %s example does not resolve, so admission would refuse the criterion: %w", []string{"result", "population"}[i], err)
			}
		}
		media := "application/octet-stream"
		if selector.Content != nil {
			media = selector.Content.MediaType
		}
		for _, declared := range selectorPaths(selector) {
			at := path.Join(runDir, declared)
			if path.Clean(declared) != declared || !strings.HasPrefix(at, runDir+"/") {
				continue // matchOutput names nothing for it; Observe says so
			}
			if prior, ok := placed[at]; ok {
				if string(prior) != string(bytes) {
					return fmt.Errorf("the result and population examples are different bytes at the same path %s", declared)
				}
				continue
			}
			if err := writeScratch(scratch, at, bytes); err != nil {
				return err
			}
			placed[at] = bytes
			outputs = append(outputs, model.ArtifactRef{Kind: "content", Selector: model.Selector{Kind: "whole"},
				Content: &model.ContentPin{SHA256: model.HashBytes(bytes), Length: uint64(len(bytes)), MediaType: media, Locators: []model.Locator{{Path: at}}}})
		}
	}
	if output != "" && len(placed) > 1 {
		return fmt.Errorf("the criterion reads two different outputs; --output supplies one")
	}
	exit := 0
	now := time.Now().UTC()
	ref := model.CriterionRef{Claim: fix.Claim, CriterionID: fix.CriterionID, Revision: fix.Revision}
	env := model.InvocationEnvelope{InvocationID: invocation,
		CriterionRef:            model.Availability[model.CriterionRef]{State: model.Known, Value: &ref},
		ExecutionSourceIdentity: model.ExecutionIdentity{Project: project.ID},
		Outcome:                 model.Availability[model.ProcessOutcome]{State: model.Known, Value: &model.ProcessOutcome{Kind: "exit", ExitCode: &exit}},
		ObservedAt:              model.Availability[time.Time]{State: model.Known, Value: &now},
		OutputRefs:              model.Availability[[]model.ArtifactRef]{State: model.Known, Value: &outputs}}
	resolver := evidence.NewResolverAt(scratch, project.ArtifactDir())
	observation, err := resolver.Observe(ctx, *fix, env)
	if err != nil {
		return err
	}
	result, err := evidence.Evaluate(*fix, []evidence.Observation{observation})
	if err != nil {
		return err
	}
	source := "the criterion's pinned example"
	if output != "" {
		source = output
	}
	fmt.Fprintf(stdout, "criterion %s revision %d on claim %s, over %s (dry run; nothing was written)\n", fix.CriterionID, fix.Revision, fix.Claim.RecordID, source)
	fmt.Fprintf(stdout, "verdict: %s\n", result.Verdict)
	if result.Reason != "" {
		fmt.Fprintf(stdout, "why: %s\n", strings.TrimPrefix(result.Reason, string(invocation)+": "))
	}
	fmt.Fprintf(stdout, "result reading: %s\n", describeReading(observation.Result, observation.Unavailable))
	fmt.Fprintf(stdout, "population reading: %s\n", describeReading(observation.Population, observation.Unavailable))
	io.WriteString(stdout, "as one completed run; instrument validation, the family and comparability are for datum proof check\n")
	return nil
}

// criterionExample resolves a selector's pinned example as admission would,
// from the project; failing that, from --blob placed at the pin's locators in
// scratch, which is where capture --blob would have carried it from.
func criterionExample(ctx context.Context, project store.Project, selector model.ArtifactRef, blob, scratch string) ([]byte, error) {
	whole := selector
	whole.Selector = model.Selector{Kind: "whole"}
	resolved, err := evidence.NewResolverAt(project.Root, project.ArtifactDir()).Resolve(ctx, whole)
	if err == nil || blob == "" || selector.Content == nil {
		return resolved.Bytes, err
	}
	data, readErr := os.ReadFile(blob)
	if readErr != nil {
		return nil, readErr
	}
	root := filepath.Join(scratch, "example")
	for _, locator := range selector.Content.Locators {
		if err := writeScratch(root, locator.Path, data); err != nil {
			return nil, err
		}
	}
	content := whole
	content.Kind, content.Git = "content", nil
	resolved, err = evidence.NewResolverAt(root, project.ArtifactDir()).Resolve(ctx, content)
	return resolved.Bytes, err
}

func selectorPaths(ref model.ArtifactRef) []string {
	var out []string
	if ref.Git != nil {
		out = append(out, ref.Git.Path)
	}
	if ref.Content != nil {
		for _, l := range ref.Content.Locators {
			out = append(out, l.Path)
		}
	}
	return out
}

func writeScratch(root, rel string, data []byte) error {
	full := filepath.Join(root, filepath.FromSlash(rel))
	if !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return fmt.Errorf("path %q leaves the scratch directory", rel)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		return err
	}
	return os.WriteFile(full, data, 0600)
}

func describeReading(r evidence.Reading, unavailable string) string {
	if unavailable != "" {
		return "none: " + unavailable
	}
	if r.Kind == evidence.ReadingAbsent {
		return "absent: " + r.Reason
	}
	meta := func(a model.Availability[string]) string {
		switch {
		case a.State == "":
			return "not stated"
		case a.Value != nil:
			return *a.Value
		}
		return "unknown (" + a.Reason + ")"
	}
	values, _ := r.Scalars()
	shown := make([]string, 0, len(values))
	for _, v := range values {
		switch {
		case v.Number != nil:
			shown = append(shown, string(*v.Number))
		case v.String != nil:
			shown = append(shown, fmt.Sprintf("%q", *v.String))
		case v.Bool != nil:
			shown = append(shown, fmt.Sprint(*v.Bool))
		}
	}
	return fmt.Sprintf("%s, unit %s, population %s, denominator %s, values [%s]", r.Kind, meta(r.Unit), meta(r.Population), meta(r.Denominator), strings.Join(shown, " "))
}
