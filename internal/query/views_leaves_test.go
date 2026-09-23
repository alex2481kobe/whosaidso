package query

// JSON-leaf helpers for the view tests: export an answer and walk it by key
// and index.

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

func factKey(v any, steps ...any) string {
	return str(v, append(steps, "key", "id")...) + "@" + str(v, append(steps, "key", "revision")...)
}
