package main

// This file holds what a --pin's own bytes state beyond the pin itself: a
// source.intake's original_digest and length (the same digest and length the
// pin was built from, one computation), and a criterion.fix's unit,
// population identity and denominator when the pinned selectors read them
// uniquely (evidence.Select, then evidence.StatedMetadata, the rule
// evaluation compares them by). Nothing here is a judgment: operator, target,
// reducer and falsifier stay the author's, and an absent, UNKNOWN or
// disagreeing field stays a placeholder. Building the pin lives in
// template_pin.go.

import (
	"encoding/json"
	"strconv"

	"whosaidso/internal/evidence"
	"whosaidso/internal/model"
)

// criterionSelectors are the criterion.fix pins whose readings state metadata.
const (
	resultSelector     = "expression.result_selector"
	populationSelector = "expression.population.selector"
)

// derive records or fills what the bytes pinned at name state.
func (t *boundTemplate) derive(name string, pinned evidence.ResolvedArtifact, selector model.Selector) error {
	switch {
	case t.event == "source.intake" && name == "source_ref":
		from := "the bytes pinned at source_ref"
		if err := t.put("original_digest", string(pinned.SHA256), from); err != nil {
			return err
		}
		return t.put("length", json.Number(strconv.FormatUint(pinned.Length, 10)), from)
	case t.event == "criterion.fix" && (name == resultSelector || name == populationSelector):
		reading, err := evidence.Select(pinned, selector)
		if err != nil {
			return nil // nothing readable at this selector states anything
		}
		if t.readings == nil {
			t.readings = map[string]*evidence.Reading{}
		}
		t.readings[name] = &reading
	}
	return nil
}

// fillStated puts each metadata field the pinned readings state uniquely
// where the template still holds its placeholder; a value already there (a
// --criterion copy, a --set) is the author's and is never replaced.
func (t *boundTemplate) fillStated() error {
	if len(t.readings) == 0 {
		return nil
	}
	stated := evidence.StatedMetadata(t.readings[resultSelector], t.readings[populationSelector])
	for _, f := range []struct{ key, path string }{
		{"unit", "expression.unit"},
		{"population", "expression.population.identity"},
		{"denominator", "expression.population.denominator"},
	} {
		value, ok := stated[f.key]
		steps, _ := parseTemplatePath(f.path)
		current, _ := templateGet(t.body, steps)
		if s, isText := current.(string); !ok || !isText || !isPlaceholder(s) {
			continue
		}
		if err := t.put(f.path, value, "stated alike by every declaration the pinned selectors read"); err != nil {
			return err
		}
	}
	return nil
}
