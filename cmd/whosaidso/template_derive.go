package main

// This file holds what the bytes a --pin read state beyond the pin itself,
// derived once from the FINAL draft, after every --pin and --set: a
// source.intake's original_digest and length (the digest and length of the
// bytes its final source_ref names), and a criterion.fix's unit, population
// identity and denominator when its final selectors read them uniquely
// (evidence.Select, then evidence.StatedMetadata, the rule evaluation
// compares them by); and a proof's code change between the two commits its
// author chose: both object formats and the changed paths under the claim's
// scope, from evidence.ScopeChanges, the observation the admission gate
// verifies them by, filled only when both commits resolve. A final reference is read only when a --pin in this
// command resolved the very artifact it names; a replaced pin or a --set
// selector is read as it finally stands, never through an earlier reading.
// Nothing here is a judgment: operator, target, reducer, falsifier and a
// code change's commits stay the author's; an absent, UNKNOWN or disagreeing field stays a placeholder;
// a value already there (a --set, a --criterion or --from copy) is the
// author's and is never replaced. Building the pin lives in template_pin.go.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"whosaidso/internal/evidence"
	"whosaidso/internal/model"
)

// criterionSelectors are the criterion.fix references whose readings state metadata.
const (
	resultSelector     = "expression.result_selector"
	populationSelector = "expression.population.selector"
)

// deriveFinal fills, where the template still holds a placeholder, what the
// final references' pinned bytes, or git, state.
func (t *boundTemplate) deriveFinal() error {
	switch t.event {
	case "proof.admit":
		return t.deriveCodeChanges()
	case "source.intake":
		pinned, ok := t.pinnedAt("source_ref")
		if !ok {
			return nil
		}
		from := "the bytes pinned at source_ref"
		if err := t.fillPlaceholder("original_digest", string(pinned.SHA256), from); err != nil {
			return err
		}
		return t.fillPlaceholder("length", json.Number(strconv.FormatUint(pinned.Length, 10)), from)
	case "criterion.fix":
		readings := map[string]*evidence.Reading{}
		for _, name := range []string{resultSelector, populationSelector} {
			if pinned, ok := t.pinnedAt(name); ok {
				if reading, err := evidence.Select(pinned, pinned.Ref.Selector); err == nil {
					readings[name] = &reading
				}
			}
		}
		if len(readings) == 0 {
			return nil
		}
		stated := evidence.StatedMetadata(readings[resultSelector], readings[populationSelector])
		for _, f := range [][2]string{{"unit", "expression.unit"}, {"population", "expression.population.identity"}, {"denominator", "expression.population.denominator"}} {
			if value, ok := stated[f[0]]; ok {
				if err := t.fillPlaceholder(f[1], value, "stated alike by every declaration the final selectors read"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// pinnedAt is the artifact the final reference at path names, with its final
// selector, when a --pin in this command resolved that same artifact; false
// when the reference is incomplete or names bytes no pin read.
func (t *boundTemplate) pinnedAt(path string) (evidence.ResolvedArtifact, bool) {
	var final model.ArtifactRef
	if !t.decodeAt(path, &final) {
		return evidence.ResolvedArtifact{}, false
	}
	for _, pinned := range t.pinned {
		same := final
		same.Selector = pinned.Ref.Selector
		if a, err := json.Marshal(same); err == nil {
			if b, err := json.Marshal(pinned.Ref); err == nil && bytes.Equal(a, b) {
				pinned.Ref = final
				return pinned, true
			}
		}
	}
	return evidence.ResolvedArtifact{}, false
}

// deriveCodeChanges fills each evidence member's code change whose two
// commits the author chose, from what git lists between them under the
// claim's scope; nothing when either commit does not resolve.
func (t *boundTemplate) deriveCodeChanges() error {
	var claim model.RecordRef
	var evidenceList []json.RawMessage
	if !t.decodeAt("claim", &claim) || !t.decodeAt("evidence", &evidenceList) {
		return nil
	}
	p, s, err := t.ledger()
	if err != nil {
		return nil // no ledger to read the claim's scope from: nothing is computed
	}
	record, ok := s.Snapshot().Record(claim)
	if !ok || record.Claim == nil || len(record.Claim.Scope.SourcePaths) == 0 {
		return nil // an empty scope asks git nothing, so no commit was resolved
	}
	format, err := objectFormat(t.c.ctx, p.Root)
	if err != nil {
		return nil
	}
	for i := range evidenceList {
		at := fmt.Sprintf("evidence[%d].code_change", i)
		var change model.CodeChange
		if !t.decodeAt(at, &change) || isPlaceholder(change.From.Commit) || isPlaceholder(change.To.Commit) {
			continue
		}
		change.From.ObjectFormat, change.To.ObjectFormat = format, format
		changed, err := evidence.ScopeChanges(t.c.ctx, evidence.ExecGit, p.Root, change.From, change.To, record.Claim.Scope.SourcePaths)
		if err != nil {
			continue // a commit that does not resolve here: the fields stay the author's to fill
		}
		from := fmt.Sprintf("git in %s, as the admission gate compares the commits", p.Root)
		for _, f := range []struct {
			path string
			v    any
		}{{at + ".from.object_format", change.From.ObjectFormat}, {at + ".to.object_format", change.To.ObjectFormat}, {at + ".changed_paths", changed}} {
			if list, _ := f.v.([]string); f.path == at+".changed_paths" && len(list) == 0 {
				t.filled = append(t.filled, f.path+": NOT filled; git lists no file under the claim's scope between the commits, so the gate refuses this code change")
				continue
			}
			if err := t.fillPlaceholder(f.path, f.v, from); err != nil {
				return err
			}
		}
	}
	return nil
}

// decodeAt decodes the final tree's value at path into v; false when path is
// absent or its value does not decode.
func (t *boundTemplate) decodeAt(path string, v any) bool {
	steps, err := parseTemplatePath(path)
	if err != nil {
		return false
	}
	node, ok := templateGet(t.body, steps)
	if !ok {
		return false
	}
	var buf bytes.Buffer
	writeTemplateJSON(&buf, node)
	return json.Unmarshal(buf.Bytes(), v) == nil
}

// fillPlaceholder puts v at path only while path still holds a placeholder.
func (t *boundTemplate) fillPlaceholder(path string, v any, from string) error {
	steps, err := parseTemplatePath(path)
	if err != nil {
		return usageError("whosaidso template: %v", err)
	}
	current, _ := templateGet(t.body, steps)
	if !allPlaceholders(current) || len(templatePlaceholders(current, "")) == 0 {
		return nil // the author's value, or nothing there
	}
	return t.put(path, v, from)
}
