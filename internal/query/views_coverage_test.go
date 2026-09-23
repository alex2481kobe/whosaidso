package query

// Section coverage: every section an old read returned is present in its new
// home (COMMAND-SPEC §3; review-views-and-guard table), compared leaf by leaf
// on the JSON exports. Each old item is found in the new answer by identity
// (record key, invocation id, packet id, attention object); every leaf of it
// must then be present, and equal, at its translated path. The translation is
// written out per representation below, so a leaf that moved is named and one
// that vanished fails. Runs on the rich fixture, and on the 1k benchmark
// fixture when DATUM_VIEWS_1K_ROOT names a retained DATUM_BENCH_ROOT.
//
// This file holds the comparison and the per-representation translations;
// the fixtures and the section-by-section walk are in views_sections_test.go.

import (
	"fmt"
	"strings"
	"testing"
)

// finder maps an old item's relative leaf path to the value at its new home.
type finder func(path []any) (any, bool)

// coverage accumulates misses so one run names every moved or lost leaf.
type coverage struct {
	t      *testing.T
	misses []string
	leaves int
}

func (c *coverage) item(where string, old any, find finder) {
	eachLeaf(old, nil, func(path []any, want any) {
		c.leaves++
		got, ok := find(path)
		// A reducer projection exports an empty list as null; the views export
		// []. Both say "none", so that one spelling change is not a loss.
		if want == nil && encode(got) == "[]" {
			return
		}
		if !ok || encode(got) != encode(want) {
			if len(c.misses) < 20 {
				c.misses = append(c.misses, fmt.Sprintf("%s %v: old %s, new %s (present %v)", where, path, encode(want), encode(got), ok))
			}
		}
	})
}

func (c *coverage) fail(format string, args ...any) {
	c.misses = append(c.misses, fmt.Sprintf(format, args...))
}

func (c *coverage) done() {
	c.t.Helper()
	if len(c.misses) > 0 {
		c.t.Fatalf("%d leaves checked; old sections missing from their new home:\n%s", c.leaves, strings.Join(c.misses, "\n"))
	}
	if c.leaves == 0 {
		c.t.Fatal("control: no old leaf was compared")
	}
}

// claimViewFinder maps an old ClaimView leaf into a new Detail: under claim,
// except observations, whose run bodies live in the answer's runs.
func claimViewFinder(detail any, runs []any, old any, prefix string) finder {
	return func(path []any) (any, bool) {
		if len(path) >= 2 && path[0] == "observations" {
			id := str(old, "observations", path[1], "invocation")
			if listed, _ := at(detail, prefix, "observations", path[1]); str(listed) != id {
				return nil, false
			}
			run, ok := runIn(runs, id)
			if !ok {
				return nil, false
			}
			return at(run, path[2:]...)
		}
		return at(detail, joinPath([]any{prefix}, path)...)
	}
}

// oldRecordFinder maps an old show Record (reducer projections) into a Detail.
func oldRecordFinder(detail any, runs []any, old any, project string) finder {
	return func(path []any) (any, bool) {
		if len(path) < 2 {
			return at(detail, path...)
		}
		kind, field, rest := path[0], path[1], path[2:]
		switch kind {
		case "claim", "decision", "instrument":
		default:
			return at(detail, path...)
		}
		switch {
		case field == kind:
			return at(detail, joinPath([]any{"fact", "key"}, rest)...)
		case field == "spec":
			return at(detail, joinPath([]any{"fact", kind}, rest)...)
		case field == "support":
			return at(detail, joinPath([]any{"support"}, rest)...)
		case field == "dispositions":
			return at(detail, joinPath([]any{"decision", "rulings"}, rest)...)
		case field == "observations" && len(rest) >= 1:
			run, ok := runIn(runs, str(old, "claim", "observations", rest[0], "key", "invocation_id"))
			if !ok {
				return nil, false
			}
			return at(run, invocationPath(rest[1:], project)...)
		}
		return at(detail, path...)
	}
}

// invocationPath maps a reducer Invocation leaf into a RunDetail.
func invocationPath(p []any, project string) []any {
	if len(p) == 0 {
		return p
	}
	rename := map[string]string{"started": "origin", "sealed": "seal_origin"}
	switch {
	case p[0] == "key" && len(p) == 2 && p[1] == "invocation_id":
		return []any{"invocation"}
	case p[0] == "key" && len(p) == 2 && p[1] == "project", p[0] == "attempt" && len(p) == 2 && p[1] == "project":
		return []any{"task", "project"}
	case p[0] == "attempt" && len(p) == 2 && p[1] == "task":
		return []any{"task", "record_id"}
	case p[0] == "attempt" && len(p) == 2 && p[1] == "attempt":
		return []any{"attempt"}
	}
	if to, ok := rename[p[0].(string)]; ok {
		return joinPath([]any{to}, p[1:])
	}
	return p
}

// attentionCovered: every old attention object is one of the new ones, whole.
func (c *coverage) attentionCovered(where string, old, new []any) {
	have := map[string]bool{}
	for _, n := range new {
		have[encode(n)] = true
	}
	for _, o := range old {
		c.leaves++
		if !have[encode(o)] {
			c.fail("%s attention %s is missing", where, encode(o))
		}
	}
}

// records: list of Details by fact key.
func (c *coverage) records(where string, old []any, newList []any, runs []any, kind string) {
	for _, o := range old {
		var key string
		switch kind {
		case "record":
			key = factKey(o, "fact")
		default:
			key = str(o, "ref", "record_id") + "@" + str(o, "ref", "revision")
		}
		n, ok := byKey2(newList, key)
		if !ok {
			c.fail("%s %s %s has no new home", where, kind, key)
			continue
		}
		switch kind {
		case "record":
			c.item(where+" "+key, o, func(path []any) (any, bool) { return at(n, path...) })
		case "claim":
			c.item(where+" "+key, o, claimViewFinder(n, runs, o, "claim"))
		default:
			c.item(where+" "+key, o, func(path []any) (any, bool) { return at(n, joinPath([]any{kind}, path)...) })
		}
	}
}

func byKey2(list []any, key string) (any, bool) {
	for _, x := range list {
		if factKey(x, "fact") == key {
			return x, true
		}
	}
	return nil, false
}

func (c *coverage) runs(where string, old, new []any) {
	for _, o := range old {
		id := str(o, "invocation")
		n, ok := runIn(new, id)
		if !ok {
			c.fail("%s run %s has no new home", where, id)
			continue
		}
		c.item(where+" run "+id, o, func(path []any) (any, bool) { return at(n, path...) })
	}
}

func todoRuns(todo any) []any {
	var out []any
	for _, task := range items(todo, "in_flight") {
		out = append(out, items(task, "runs")...)
	}
	return out
}

func allTodoTasks(todo any) []any {
	var out []any
	for _, section := range []string{"in_flight", "awaiting_acceptance", "blocked", "ready"} {
		out = append(out, items(todo, section)...)
	}
	return out
}

// nowCovered: old now -> todo.
func (c *coverage) nowCovered(where string, now, todo any) {
	c.records(where+" in_flight", items(now, "in_flight"), items(todo, "in_flight"), nil, "record")
	c.records(where+" open decisions", items(now, "decisions"), items(todo, "open_decisions"), nil, "decision")
	c.runs(where+" runs", items(now, "runs"), todoRuns(todo))
	c.attentionCovered(where, items(now, "attention"), items(todo, "attention"))
}

// stateCovered: old state (or bare context) -> bare show.
func (c *coverage) stateCovered(where string, state, show any) {
	runs := items(show, "runs")
	c.records(where+" claims", items(state, "claims"), items(show, "records"), runs, "claim")
	c.records(where+" decisions", items(state, "decisions"), items(show, "records"), runs, "decision")
	c.records(where+" closed", items(state, "closed"), items(show, "records"), runs, "record")
	c.runs(where+" runs", items(state, "runs"), runs)
	c.attentionCovered(where, items(state, "attention"), items(show, "attention"))
	for i, s := range items(state, "stale") {
		c.item(fmt.Sprintf("%s stale %d", where, i), s, func(path []any) (any, bool) {
			return at(show, joinPath([]any{"stale", "claims", i}, path)...)
		})
	}
}

// closureCovered: an old closure (nodes carrying views) -> refs plus records.
func (c *coverage) closureCovered(where string, old, cont any) {
	records, _ := at(cont, "records")
	runs := items(cont, "runs")
	node := func(section string, o any, refs []any) {
		key := str(o, "ref", "record_id") + "@" + str(o, "ref", "revision")
		var ref any
		for _, r := range refs {
			if str(r, "ref", "record_id")+"@"+str(r, "ref", "revision") == key {
				ref = r
			}
		}
		if ref == nil {
			c.fail("%s %s node %s has no new ref", where, section, key)
			return
		}
		body, resolved := at(records, key)
		c.item(where+" "+section+" "+key, o, func(path []any) (any, bool) {
			if !resolved && path[0] == "corrections" {
				return []any{}, true // an unresolved node never had corrections to carry
			}
			switch path[0] {
			case "ref", "via", "unresolved":
				return at(ref, path...)
			case "current_revision":
				return at(body, path...)
			case "claim":
				view, _ := at(o, "claim")
				return claimViewFinder(body, runs, view, "claim")(path[1:])
			}
			return at(body, path...)
		})
	}
	for _, o := range items(old, "mandatory") {
		node("mandatory", o, items(cont, "closure", "mandatory"))
	}
	for _, o := range items(old, "optional") {
		node("optional", o, items(cont, "context", "refs"))
	}
	for _, key := range []string{"root", "cycles"} {
		v, _ := at(old, key)
		c.item(where+" "+key, v, func(path []any) (any, bool) { return at(cont, joinPath([]any{"closure", key}, path)...) })
	}
	limit, _ := at(old, "limit")
	c.item(where+" limit", limit, func(path []any) (any, bool) { return at(cont, joinPath([]any{"context", "limit"}, path)...) })
}
