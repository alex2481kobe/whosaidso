package main

// This file holds the template tree a bound template is filled in: parsing a
// field path (a.b[0].c), setting a value at it, reading JSON into the same
// ordered tree, and finding the placeholders still unfilled. The tree is the
// one template.go renders. Which value a bind flag puts where lives in
// template_bind.go and template_ledger.go; no ledger is read here.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"datum/internal/model"
)

// templateStep is one step of a field path: an object key or an array index.
type templateStep struct {
	key   string
	index int // -1 for a key
}

// parseTemplatePath reads a.b[0].c, the spelling the template notes print.
func parseTemplatePath(p string) ([]templateStep, error) {
	var steps []templateStep
	for _, part := range strings.Split(p, ".") {
		key, rest, _ := strings.Cut(part, "[")
		if key == "" && (len(steps) == 0 || rest == "") {
			return nil, fmt.Errorf("path %q has an empty key", p)
		}
		if key != "" {
			steps = append(steps, templateStep{key: key, index: -1})
		}
		for rest != "" {
			number, after, ok := strings.Cut(rest, "]")
			n, err := strconv.Atoi(number)
			if !ok || err != nil || n < 0 || (after != "" && after[0] != '[') {
				return nil, fmt.Errorf("path %q has a malformed index", p)
			}
			steps = append(steps, templateStep{index: n})
			rest = strings.TrimPrefix(after, "[")
		}
	}
	return steps, nil
}

// templateSet puts value at path under node and returns the updated node. A
// key must already exist: a path can fill the template, never invent a field.
// An index one past an array's end appends, only when the whole element is set.
func templateSet(node any, steps []templateStep, value any, at string) (any, error) {
	if len(steps) == 0 {
		return value, nil
	}
	s := steps[0]
	switch n := node.(type) {
	case templateObject:
		if s.index >= 0 {
			return nil, fmt.Errorf("%s is an object, not an array", at)
		}
		for i := range n {
			if n[i].key == s.key {
				v, err := templateSet(n[i].value, steps[1:], value, joinStep(at, s))
				n[i].value = v
				return n, err
			}
		}
		return nil, fmt.Errorf("%s has no field %q", orEvent(at), s.key)
	case []any:
		if s.index < 0 {
			return nil, fmt.Errorf("%s is an array; give an index", at)
		}
		if s.index == len(n) && len(steps) == 1 {
			return append(n, value), nil
		}
		if s.index >= len(n) {
			return nil, fmt.Errorf("%s has %d elements; index %d is not one of them", at, len(n), s.index)
		}
		v, err := templateSet(n[s.index], steps[1:], value, joinStep(at, s))
		n[s.index] = v
		return n, err
	}
	return nil, fmt.Errorf("%s holds a value, not a field %s", orEvent(at), joinStep("", s))
}

// templateGet reads the node at path, or reports it absent.
func templateGet(node any, steps []templateStep) (any, bool) {
	for _, s := range steps {
		switch n := node.(type) {
		case templateObject:
			found := false
			for _, m := range n {
				if m.key == s.key && s.index < 0 {
					node, found = m.value, true
					break
				}
			}
			if !found {
				return nil, false
			}
		case []any:
			if s.index < 0 || s.index >= len(n) {
				return nil, false
			}
			node = n[s.index]
		default:
			return nil, false
		}
	}
	return node, true
}

func joinStep(at string, s templateStep) string {
	if s.index >= 0 {
		return fmt.Sprintf("%s[%d]", at, s.index)
	}
	if at == "" {
		return s.key
	}
	return at + "." + s.key
}

func orEvent(at string) string {
	if at == "" {
		return "the event"
	}
	return at
}

// templateClone deep-copies a tree, so one skeleton element can seed several.
func templateClone(node any) any {
	switch n := node.(type) {
	case templateObject:
		out := make(templateObject, len(n))
		for i, m := range n {
			out[i] = templateMember{m.key, templateClone(m.value)}
		}
		return out
	case []any:
		out := make([]any, len(n))
		for i, v := range n {
			out[i] = templateClone(v)
		}
		return out
	}
	return node
}

// templateValue reads JSON into the ordered tree, numbers kept as written.
func templateValue(data []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	v, err := templateDecode(d)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err == nil {
		return nil, fmt.Errorf("trailing data after the JSON value")
	}
	return v, nil
}

func templateDecode(d *json.Decoder) (any, error) {
	tok, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			out := templateObject{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return nil, err
				}
				v, err := templateDecode(d)
				if err != nil {
					return nil, err
				}
				out = append(out, templateMember{key.(string), v})
			}
			_, err := d.Token()
			return out, err
		case '[':
			out := []any{}
			for d.More() {
				v, err := templateDecode(d)
				if err != nil {
					return nil, err
				}
				out = append(out, v)
			}
			_, err := d.Token()
			return out, err
		}
	}
	return tok, nil
}

// templateTree is v, a Go value, as the ordered tree (json.Marshal's field order).
func templateTree(v any) (any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return templateValue(data)
}

// isPlaceholder reports whether s is a placeholder template.go wrote. The
// grammar is the model's, so the decoder refuses exactly what --capture does.
func isPlaceholder(s string) bool { return model.IsPlaceholder(s) }

// templatePlaceholders lists the path of every placeholder left under node:
// a value, or an object key (a map's key is a placeholder too).
func templatePlaceholders(node any, at string) []string {
	var out []string
	switch n := node.(type) {
	case templateObject:
		for _, m := range n {
			here := joinStep(at, templateStep{key: m.key, index: -1})
			if isPlaceholder(m.key) {
				out = append(out, here)
				continue
			}
			out = append(out, templatePlaceholders(m.value, here)...)
		}
	case []any:
		for i, v := range n {
			out = append(out, templatePlaceholders(v, joinStep(at, templateStep{index: i}))...)
		}
	case string:
		if isPlaceholder(n) {
			out = append(out, orEvent(at))
		}
	}
	return out
}

// templateDropUnfilled removes an optional key whose whole value is still
// placeholders, and returns the updated node: an optional field nobody filled
// is an omitted one. path is a note path ("0" steps), which applies to every
// element of an array.
func templateDropUnfilled(node any, path []string) any {
	if len(path) == 0 {
		return node
	}
	switch n := node.(type) {
	case []any:
		if path[0] == "0" {
			for i := range n {
				n[i] = templateDropUnfilled(n[i], path[1:])
			}
		}
	case templateObject:
		for i, m := range n {
			if m.key != path[0] {
				continue
			}
			if len(path) > 1 {
				n[i].value = templateDropUnfilled(m.value, path[1:])
			} else if allPlaceholders(m.value) {
				return append(n[:i:i], n[i+1:]...)
			}
			return n
		}
	}
	return node
}

// allPlaceholders reports whether every leaf under node is a placeholder.
func allPlaceholders(node any) bool {
	switch n := node.(type) {
	case templateObject:
		for _, m := range n {
			if !isPlaceholder(m.key) && !allPlaceholders(m.value) {
				return false
			}
		}
		return true
	case []any:
		for _, v := range n {
			if !allPlaceholders(v) {
				return false
			}
		}
		return true
	case string:
		return isPlaceholder(n)
	}
	return false
}

// MarshalJSON writes the object in its own key order, for a tree placed in a
// JSON answer (check admission --family's proof skeleton).
func (o templateObject) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	writeTemplateJSON(&b, o)
	return b.Bytes(), nil
}
