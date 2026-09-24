package model

// This file holds the template placeholder grammar and the decoder's refusal
// of a placeholder left in an event. `whosaidso template` writes every unfilled
// field as "<kind: hint>" or "<kind>"; a string of exactly that form is never
// an authored value, so no event carrying one decodes. The check lives in
// DecodeEvent because every capture passes through it (CLI capture, template
// --capture, check admission --events, run, handback, reconcile via
// EncodeEvent, and admission's own re-decode). Rendering templates does not
// belong here; it lives in cmd/whosaidso.

import (
	"fmt"
	"strings"
)

// placeholderKinds are the kinds `whosaidso template` writes. A string is a
// placeholder only when it is wholly "<kind>" or "<kind: hint>" with one of
// these kinds, so authored text that merely holds angle brackets decodes.
var placeholderKinds = map[string]bool{
	"text": true, "id": true, "project": true, "digest": true, "revision": true, "time": true, "number": true,
	"one of": true, "bool": true, "integer": true, "count": true, "actor-id": true, "path": true, "commit": true,
	"media-type": true, "pointer": true, "key": true, "unsupported": true,
}

// IsPlaceholder reports whether s is a placeholder `whosaidso template` writes.
func IsPlaceholder(s string) bool {
	if len(s) < 3 || s[0] != '<' || s[len(s)-1] != '>' {
		return false
	}
	kind, _, _ := strings.Cut(s[1:len(s)-1], ": ")
	return placeholderKinds[kind]
}

// refusePlaceholders refuses the first placeholder under an ordered tree,
// as a value or as an object key (a map key is authored too).
func refusePlaceholders(node any, at string) error {
	switch n := node.(type) {
	case []member:
		for _, m := range n {
			here := at + "." + m.key
			if IsPlaceholder(m.key) {
				return invalid(here, "the template placeholder key "+m.key+" is still unfilled")
			}
			if err := refusePlaceholders(m.value, here); err != nil {
				return err
			}
		}
	case []any:
		for i, v := range n {
			if err := refusePlaceholders(v, fmt.Sprintf("%s[%d]", at, i)); err != nil {
				return err
			}
		}
	case string:
		if IsPlaceholder(n) {
			return invalid(at, "the template placeholder "+n+" is still unfilled; write the value, not the hint")
		}
	}
	return nil
}
