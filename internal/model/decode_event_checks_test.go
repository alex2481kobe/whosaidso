package model

// DecodeEvent parses an event payload once. This table proves every refusal the
// second parse used to sit beside still fires, at the same code and path:
// UTF-8, duplicate keys, trailing content, exact field names, unknown fields,
// UTC, availability unions and semantic validation.

import (
	"bytes"
	"errors"
	"testing"
)

func TestDecodeEventKeepsEveryCheckAfterOneParse(t *testing.T) {
	good, err := EncodeEvent(schemaEvents()[9]) // invocation.seal: times, unions, nested refs
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeEvent(good); err != nil {
		t.Fatalf("control: %v", err)
	}
	cases := []struct {
		name, from, to, code, path string
	}{
		{"invalid utf-8", `"argv":["go"`, "\"argv\":[\"g\xff\"", "invalid-json", "event.data"},
		{"duplicate key", `"argv":`, `"argv":["forged"],"argv":`, "invalid-json", "$.envelope.argv"},
		{"trailing content", "", " {}", "invalid-json", "$"},
		{"case alias", `"argv":`, `"ARGV":`, "invalid-field", "event.data.envelope.ARGV"},
		{"unknown field", `"argv":`, `"argv_extra":[],"argv":`, "invalid-field", "event.data.envelope.argv_extra"},
		{"non-UTC time", `"started_at":"2026-09-21T12:00:00Z"`, `"started_at":"2026-09-21T13:00:00+01:00"`, "invalid-field", "event.data.envelope.started_at"},
		{"union: unknown with a value", `"isolation":{"state":"unknown","reason":"not observed by this producer"}`, `"isolation":{"state":"unknown","reason":"not observed by this producer","value":{}}`, "invalid-field", "event.data.envelope.isolation.value"},
		{"union: state outside the set", `"visual":{"state":"unknown"`, `"visual":{"state":"maybe"`, "invalid-field", "event.data.envelope.visual.state"},
		{"semantic: empty argv", `"argv":["go","test","./internal/model/"]`, `"argv":[]`, "invalid-field", "event.data.envelope.argv"},
		{"semantic: exit code on a signal", `"value":{"kind":"exit","exit_code":0}`, `"value":{"kind":"signal","exit_code":0}`, "invalid-field", "event.data.envelope.outcome.value"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := good.Data
			if c.name == "trailing content" {
				data = append(append([]byte{}, good.Data...), c.to...)
			} else {
				if !bytes.Contains(data, []byte(c.from)) {
					t.Fatalf("fixture lacks %q", c.from)
				}
				data = bytes.Replace(data, []byte(c.from), []byte(c.to), 1)
			}
			_, err := DecodeEvent(Event{Type: good.Type, Data: data})
			var f *Fault
			if !errors.As(err, &f) || f.Code != c.code || f.Path != c.path {
				t.Fatalf("want %s at %q, got %v", c.code, c.path, err)
			}
		})
	}
}
