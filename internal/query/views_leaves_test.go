package query

// JSON-leaf helpers for the view tests: export an answer, walk it by key and
// index, visit every leaf with its path, and find list items by identity.
// What is compared, and where each old section lives now, is in
// views_coverage_test.go and views_sections_test.go.

import (
	"bytes"
	"encoding/json"
	"testing"
)

func exported(t testing.TB, v any) any {
	t.Helper()
	var buf bytes.Buffer
	var err error
	switch a := v.(type) {
	case Answer:
		err = RenderJSON(&buf, a)
	case ViewAnswer:
		err = RenderViewJSON(&buf, a)
	default:
		t.Fatalf("cannot export %T", v)
	}
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&buf)
	decoder.UseNumber()
	var out any
	if err := decoder.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// at walks string keys and int indexes; a missing step answers false.
func at(v any, steps ...any) (any, bool) {
	for _, step := range steps {
		switch k := step.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil, false
			}
			if v, ok = m[k]; !ok {
				return nil, false
			}
		case int:
			xs, ok := v.([]any)
			if !ok || k >= len(xs) {
				return nil, false
			}
			v = xs[k]
		}
	}
	return v, true
}

func items(v any, steps ...any) []any {
	xs, _ := at(v, steps...)
	list, _ := xs.([]any)
	return list
}

func str(v any, steps ...any) string {
	x, _ := at(v, steps...)
	switch s := x.(type) {
	case string:
		return s
	case json.Number:
		return s.String()
	}
	return ""
}

// eachLeaf visits every leaf and empty container with its path.
func eachLeaf(v any, path []any, visit func([]any, any)) {
	switch node := v.(type) {
	case map[string]any:
		if len(node) > 0 {
			for k, child := range node {
				eachLeaf(child, append(append([]any{}, path...), k), visit)
			}
			return
		}
	case []any:
		if len(node) > 0 {
			for i, child := range node {
				eachLeaf(child, append(append([]any{}, path...), i), visit)
			}
			return
		}
	}
	visit(path, v)
}

func encode(v any) string { b, _ := json.Marshal(v); return string(b) }

func joinPath(prefix []any, rest []any) []any { return append(append([]any{}, prefix...), rest...) }

// byKey finds the item of list whose value at steps equals want.
func byKey(list []any, want string, steps ...any) (any, bool) {
	for _, x := range list {
		if str(x, steps...) == want {
			return x, true
		}
	}
	return nil, false
}

func factKey(v any, steps ...any) string {
	return str(v, append(steps, "key", "id")...) + "@" + str(v, append(steps, "key", "revision")...)
}

// runIn finds a run body by invocation id in the new answer's run lists.
func runIn(runs []any, id string) (any, bool) { return byKey(runs, id, "invocation") }
