package model

// This file holds the template placeholder grammar and the decoder's refusal
// of a placeholder left in an event. `whosaidso template` writes every unfilled
// field as "<kind: hint>" or "<kind>"; a string of exactly that form is never
// an authored value, so no event carrying one decodes. The check lives in
// DecodeEvent because every capture passes through it (CLI capture, template
// --capture, check admission --events, run, handback, reconcile via
// EncodeEvent, and admission's own re-decode). Placeholders lists them all, so
// a capture can refuse every one at once. Rendering templates does not belong
// here; it lives in cmd/whosaidso.

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

// placeholderAt is one placeholder left in a tree: where, what, and whether it
// sits as an object key rather than a value.
type placeholderAt struct {
	at, text string
	key      bool
}

// Placeholders lists every placeholder left in an event's data, values and
// object keys alike, at the paths the decoder names ("event.data.spec.intent").
// Capture refuses them all at once with it, where the decoder alone stops at
// the first. Data that does not parse lists nothing: the decoder names that.
func Placeholders(data []byte) []string {
	tree, err := parseOrdered(data)
	if err != nil {
		return nil
	}
	var found []placeholderAt
	collectPlaceholders(tree, "event.data", &found)
	out := make([]string, len(found))
	for i, f := range found {
		out[i] = f.at
	}
	return out
}

// refusePlaceholders refuses the first placeholder under an ordered tree,
// as a value or as an object key (a map key is authored too).
func refusePlaceholders(node any, at string) error {
	var found []placeholderAt
	collectPlaceholders(node, at, &found)
	if len(found) == 0 {
		return nil
	}
	f := found[0]
	if f.key {
		return invalid(f.at, "the template placeholder key "+f.text+" is still unfilled")
	}
	return invalid(f.at, "the template placeholder "+f.text+" is still unfilled; write the value, not the hint")
}

// collectPlaceholders appends every placeholder under node, in document order.
func collectPlaceholders(node any, at string, found *[]placeholderAt) {
	switch n := node.(type) {
	case []member:
		for _, m := range n {
			here := at + "." + m.key
			if IsPlaceholder(m.key) {
				*found = append(*found, placeholderAt{at: here, text: m.key, key: true})
			}
			collectPlaceholders(m.value, here, found)
		}
	case []any:
		for i, v := range n {
			collectPlaceholders(v, fmt.Sprintf("%s[%d]", at, i), found)
		}
	case string:
		if IsPlaceholder(n) {
			*found = append(*found, placeholderAt{at: at, text: n})
		}
	}
}
