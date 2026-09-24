package main

// This file holds what a bound template decides once every flag is applied:
// which member of a union the author chose (the member whose keys they
// filled, or the tag they set), dropping the other members' keys nobody
// filled; and which keys count as unfilled, so --capture can omit an optional
// key and --set PATH=null can omit one on purpose. It chooses only where the
// author's own fill leaves one answer; a choice with two filled members stays
// for the gate to refuse. The tree and its paths live in template_tree.go.

import (
	"strings"
)

// unfilled reports whether the author left the value at at unfilled: every
// leaf is still a placeholder, or a placeholder remains and no bind flag,
// --pin or --set put anything at or under it. Values WhoSaidSo fills without a
// flag (the project id, a one-member choice) do not make a key filled.
func (t *boundTemplate) unfilled(at string, value any) bool {
	if allPlaceholders(value) {
		return true
	}
	if len(templatePlaceholders(value, "")) == 0 {
		return false
	}
	for put := range t.putPaths {
		if put == at || strings.HasPrefix(put, at+".") || strings.HasPrefix(put, at+"[") {
			return false
		}
	}
	return true
}

// resolveChoices applies every union note to each object it names.
func (t *boundTemplate) resolveChoices() {
	for _, n := range t.notes {
		if n.Kind != "choose" {
			continue
		}
		union := n.Union
		t.body = templateEachAt(t.body, n.Path, "", func(at string, obj templateObject) templateObject {
			return t.resolveChoice(at, obj, union)
		})
	}
}

// resolveChoice finds the chosen member of one union object: its tag when
// the author set it, else the one member whose keys hold filled values (and
// then sets the tag). The other members' unfilled keys are dropped; a filled
// one stays, for the gate to check or refuse.
func (t *boundTemplate) resolveChoice(at string, obj templateObject, u templateUnion) templateObject {
	value := func(key string) (any, bool) {
		for _, m := range obj {
			if m.key == key {
				return m.value, true
			}
		}
		return nil, false
	}
	here := func(key string) string { return joinStep(at, templateStep{key: key, index: -1}) }
	chosen := ""
	if u.tag != "" {
		if tag, ok := value(u.tag); ok {
			if s, ok := tag.(string); ok && !isPlaceholder(s) {
				chosen = s
			}
		}
	}
	if chosen == "" {
		var candidates []string
		for _, member := range u.members {
			for _, key := range u.branches[member] {
				if v, ok := value(key); ok && !t.unfilled(here(key), v) {
					candidates = append(candidates, member)
					break
				}
			}
		}
		if len(candidates) != 1 {
			return obj
		}
		chosen = candidates[0]
		if u.tag != "" {
			for i := range obj {
				if obj[i].key == u.tag {
					obj[i].value = chosen
				}
			}
		}
	}
	keep := map[string]bool{}
	for _, key := range u.branches[chosen] {
		keep[key] = true
	}
	drop := map[string]bool{}
	for member, keys := range u.branches {
		if member == chosen {
			continue
		}
		for _, key := range keys {
			if v, ok := value(key); ok && !keep[key] && t.unfilled(here(key), v) {
				drop[key] = true
			}
		}
	}
	out := obj[:0:0]
	for _, m := range obj {
		if !drop[m.key] {
			out = append(out, m)
		}
	}
	return out
}

// templateEachAt calls f on every object at a note path ("0" steps apply to
// every element of an array) and returns the updated node.
func templateEachAt(node any, path []string, at string, f func(string, templateObject) templateObject) any {
	switch n := node.(type) {
	case templateObject:
		if len(path) == 0 {
			return f(at, n)
		}
		for i := range n {
			if n[i].key == path[0] {
				n[i].value = templateEachAt(n[i].value, path[1:], joinStep(at, templateStep{key: n[i].key, index: -1}), f)
			}
		}
	case []any:
		if len(path) > 0 && path[0] == "0" {
			for i := range n {
				n[i] = templateEachAt(n[i], path[1:], joinStep(at, templateStep{index: i}), f)
			}
		}
	}
	return node
}

// optionalPath reports whether path (any indices) is a key the notes call optional.
func (t *boundTemplate) optionalPath(path string) bool {
	steps, err := parseTemplatePath(path)
	if err != nil {
		return false
	}
	var parts []string
	for _, s := range steps {
		if s.index >= 0 {
			parts = append(parts, "0")
		} else {
			parts = append(parts, s.key)
		}
	}
	for _, n := range t.notes {
		if n.Kind == "optional" && templatePath(n.Path) == templatePath(parts) {
			return true
		}
	}
	return false
}
