package model

// Tests for the placeholder grammar the decoder refuses (placeholder.go):
// exactly the forms `datum template` writes, and no near miss.

import (
	"strings"
	"testing"
)

func TestIsPlaceholderExactForms(t *testing.T) {
	for _, s := range []string{"<text>", "<text: authored words, not blank>", "<one of: git | content>", "<actor-id: who, e.g. coordinator>", "<key: a name>", "<unsupported>"} {
		if !IsPlaceholder(s) {
			t.Errorf("%q is a placeholder the template writes", s)
		}
	}
	for _, s := range []string{"", "<>", "text", "<text:no space>", "<Text: x>", "<note: x>", " <text: x>", "<text: x> ", "see <text: x>", "<text: x", "a < b > c"} {
		if IsPlaceholder(s) {
			t.Errorf("%q is authored text, not a placeholder", s)
		}
	}
}

// The decoder refuses a placeholder as a value or as a map key, at its path.
func TestDecodeEventRefusesPlaceholder(t *testing.T) {
	for _, tc := range []struct{ data, path string }{
		{`{"a":{"b":["x","<text>"]}}`, "event.data.a.b[1]"},
		{`{"a":{"<key: a name>":"x"}}`, "event.data.a.<key: a name>"},
	} {
		_, err := DecodeEvent(Event{Type: "task.create", Data: []byte(tc.data)})
		if err == nil || !strings.Contains(err.Error(), tc.path) || !strings.Contains(err.Error(), "placeholder") {
			t.Errorf("%s: want a placeholder refusal at %s, got %v", tc.data, tc.path, err)
		}
	}
	// Control: the same shape without a placeholder fails on shape, not placeholder.
	if _, err := DecodeEvent(Event{Type: "task.create", Data: []byte(`{"a":"<note>"}`)}); err == nil || strings.Contains(err.Error(), "placeholder") {
		t.Errorf("a non-placeholder must reach the shape check: %v", err)
	}
}
