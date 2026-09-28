package main

// This file holds which notes a printed template still owes its author, read
// from the final tree rather than from the skeleton: a choice is listed while
// some object at its path is undecided, an optional key while it still holds
// a placeholder (or is absent where it could be added), a minted id unless a
// flag replaced it, and each filled path once, with where its final value came
// from. Rendering a note lives in template.go; printing, in template_capture.go.

import (
	"strings"
)

// liveNotes are the notes that still apply to the final tree.
func (t *boundTemplate) liveNotes() []templateNote {
	var out []templateNote
	for _, n := range t.notes {
		if t.noteLive(&n) {
			out = append(out, n)
		}
	}
	return out
}

// noteLive reports whether n still applies; an optional key that is absent
// where it could be added is marked absent, to be noted as addable.
func (t *boundTemplate) noteLive(n *templateNote) bool {
	at := templatePath(n.Path)
	if n.Kind == "minted" { // not where a bind flag, --set or --from's copy replaced it
		steps, _ := parseTemplatePath(at)
		id, ok := templateGet(t.body, steps)
		return ok && id == n.ID
	}
	steps, err := parseTemplatePath(at)
	if at == "" {
		steps, err = nil, nil
	}
	if err != nil {
		return true
	}
	values := templateGetAll(t.body, steps, "")
	if n.Kind == "optional" {
		if len(values) == 0 {
			// absent: note it only where its parent is there to hold it
			n.Detail = "absent"
			return len(steps) > 0 && len(templateGetAll(t.body, steps[:len(steps)-1], "")) > 0
		}
		for _, v := range values {
			if len(templatePlaceholders(v[1], "")) > 0 {
				return true
			}
		}
		return false
	}
	// choose: live while some object at the path is undecided
	for _, v := range values {
		obj, ok := v[1].(templateObject)
		if !ok {
			continue
		}
		if choiceUndecided(obj, n.Union) {
			return true
		}
	}
	return false
}

// choiceUndecided reports whether the author still has to choose in obj: its
// tag is a placeholder, an untagged choice keeps other than exactly one
// member's key, or a one-member choice still holds a placeholder.
func choiceUndecided(obj templateObject, u templateUnion) bool {
	present := map[string]any{}
	for _, m := range obj {
		present[m.key] = m.value
	}
	switch {
	case u.tag != "":
		s, isText := present[u.tag].(string)
		return isText && isPlaceholder(s)
	case len(u.members) == 1:
		return len(templatePlaceholders(obj, "")) > 0
	}
	kept := 0
	for _, member := range u.members {
		for _, key := range u.branches[member] {
			if _, ok := present[key]; ok {
				kept++
				break
			}
		}
	}
	return kept != 1
}

// filledLines are the "PATH: from WHAT" lines, one per path: the last fill of
// a path is where its final value came from.
func (t *boundTemplate) filledLines() []string {
	last := map[string]int{}
	for i, f := range t.filled {
		path, _, _ := strings.Cut(f, ": ")
		last[path] = i
	}
	var out []string
	for i, f := range t.filled {
		path, _, _ := strings.Cut(f, ": ")
		if last[path] == i {
			out = append(out, f)
		}
	}
	return out
}
