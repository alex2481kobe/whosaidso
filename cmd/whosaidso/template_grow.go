package main

// This file holds how a --set or --pin path reaches a place the tree lacks:
// only where the event's schema has one. An absent key grows from the event's
// own skeleton at that place (an optional key a --from copy omitted, such as
// replacement.progress), and a list grows by one element past its end; any
// other absent key or index is refused. Setting the value lives in
// template_tree.go; which paths the bind flags fill, in template_bind.go.

import (
	"fmt"
)

const projectPlaceholder = "<project: the id declared in whosaidso.toml>"

// skeletonAt is the skeleton at steps, every index read as element 0, from a
// fresh instance of the event's skeleton filled as the draft was with what
// needs no flag (fillUnflagged): an appended element mints its own ids at
// revision 1, as element 0 did, and never copies one already minted. False
// when the schema has nothing there.
func (t *boundTemplate) skeletonAt(steps []templateStep) (any, bool) {
	skeleton, notes, err := buildTemplateTree(t.event)
	if err != nil {
		return nil, false
	}
	fresh := &boundTemplate{c: t.c, event: t.event, body: skeleton, notes: notes, minted: t.minted, author: t.author, projectID: t.projectID}
	if err := fresh.fillUnflagged(); err != nil {
		return nil, false
	}
	normal := make([]templateStep, len(steps))
	for i, s := range steps {
		normal[i] = s
		if s.index > 0 {
			normal[i].index = 0
		}
	}
	return templateGet(fresh.body, normal)
}

// grow adds to node what steps pass through and node lacks, from the
// skeleton, and returns the updated node. The last step's own list element
// is left to templateSet, which appends a whole element one past the end.
func (t *boundTemplate) grow(node any, steps []templateStep, at []templateStep) (any, error) {
	if len(steps) == 0 {
		return node, nil
	}
	s := steps[0]
	here := append(at[:len(at):len(at)], s)
	switch n := node.(type) {
	case templateObject:
		if s.index >= 0 {
			return node, nil // templateSet refuses an index into an object
		}
		for i := range n {
			if n[i].key == s.key {
				v, err := t.grow(n[i].value, steps[1:], here)
				n[i].value = v
				return n, err
			}
		}
		skeleton, ok := t.skeletonAt(here)
		if !ok {
			return node, nil // not in the schema: templateSet refuses it
		}
		v, err := t.grow(skeleton, steps[1:], here)
		return append(n, templateMember{s.key, v}), err
	case []any:
		switch {
		case s.index < 0:
			return node, nil
		case s.index > len(n):
			return node, fmt.Errorf("%s has %d element(s); a path can add only %s, one past the end",
				orEvent(stepsText(at)), len(n), stepsText(append(at[:len(at):len(at)], templateStep{index: len(n)})))
		case s.index == len(n) && len(steps) == 1:
			return node, nil
		case s.index == len(n):
			skeleton, ok := t.skeletonAt(here)
			if !ok {
				return node, nil
			}
			v, err := t.grow(skeleton, steps[1:], here)
			return append(n, v), err
		}
		v, err := t.grow(n[s.index], steps[1:], here)
		n[s.index] = v
		return n, err
	}
	return node, nil
}

// stepsText spells steps as the notes print a path.
func stepsText(steps []templateStep) string {
	at := ""
	for _, s := range steps {
		at = joinStep(at, s)
	}
	return at
}

// replacePlaceholder replaces every occurrence of placeholder under node and
// counts them.
func replacePlaceholder(node any, placeholder, value string) (any, int) {
	count := 0
	var walk func(node any) any
	walk = func(node any) any {
		switch n := node.(type) {
		case templateObject:
			for i := range n {
				n[i].value = walk(n[i].value)
			}
		case []any:
			for i := range n {
				n[i] = walk(n[i])
			}
		case string:
			if n == placeholder {
				count++
				return value
			}
		}
		return node
	}
	return walk(node), count
}
