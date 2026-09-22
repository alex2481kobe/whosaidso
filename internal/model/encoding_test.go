package model

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func utf8Packet(author string, data json.RawMessage) Packet {
	return Packet{
		Version: WireVersion, Project: "datum/datum", CommandID: "01K5V8Q1110000000000000000",
		RequestDigest: HashBytes([]byte("authored input")), Author: Actor{ID: author},
		CapturedAt: time.Date(2026, 9, 22, 1, 2, 3, 0, time.UTC),
		Events:     []Event{{Type: "task.create", Data: data}},
	}
}

// Invalid UTF-8 is refused BEFORE json.Marshal can turn it into U+FFFD, and the
// refusal names the field, so "holder-\xff" never becomes a different actor.
func TestEncodeRefusesInvalidUTF8BeforeMarshalling(t *testing.T) {
	if _, err := Encode(utf8Packet("holder", json.RawMessage(`{"intent":"x"}`))); err != nil {
		t.Fatalf("good control must encode: %v", err)
	}
	type inner struct {
		Note string `json:"note"`
	}
	type embedded struct {
		Promoted string `json:"promoted"`
	}
	type outer struct {
		embedded
		List  []string          `json:"list"`
		Map   map[string]string `json:"map"`
		Ptr   *inner            `json:"ptr"`
		Any   any               `json:"any"`
		Plain string
		Skip  string `json:"-"`
		Bytes []byte `json:"bytes"`
	}
	const bad = "holder-\xff"
	for _, tc := range []struct {
		name  string
		value any
		path  string
	}{
		{"packet actor", utf8Packet(bad, json.RawMessage(`{"intent":"x"}`)), "$.author.id"},
		{"raw event data", utf8Packet("holder", json.RawMessage("{\"intent\":\""+bad+"\"}")), "$.events[0].data"},
		{"pointer to packet", &Packet{Author: Actor{UnknownReason: bad}}, "$.author.unknown_reason"},
		{"slice element", outer{List: []string{"ok", bad}}, "$.list[1]"},
		{"map value", outer{Map: map[string]string{"k": bad}}, "$.map.k"},
		{"map key", outer{Map: map[string]string{bad: "v"}}, "$.map.<map key>"},
		{"pointer field", outer{Ptr: &inner{Note: bad}}, "$.ptr.note"},
		{"interface", outer{Any: map[string]any{"deep": []any{bad}}}, "$.any.deep[0]"},
		{"untagged field", outer{Plain: bad}, "$.Plain"},
		{"promoted field", outer{embedded: embedded{Promoted: bad}}, "$.promoted"},
		{"bare string", bad, "$"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Encode(tc.value)
			var f *Fault
			if !errors.As(err, &f) || f.Code != "invalid-field" || f.Path != tc.path {
				t.Fatalf("want invalid-field at %s, got %v and bytes %q", tc.path, err, out)
			}
		})
	}
	// A skipped field and a base64 byte slice never reach the output as text.
	if _, err := Encode(outer{Skip: bad, Bytes: []byte(bad)}); err != nil {
		t.Fatalf("json-skipped and base64 fields carry no text: %v", err)
	}
}

// Valid non-ASCII text, including a literal U+FFFD, comes back byte for byte.
func TestEncodeRoundTripsValidUnicodeExactly(t *testing.T) {
	for _, text := range []string{
		"café", "é", "漢字", "🐋", "�", "a‍b", "<&>",
	} {
		data, _ := json.Marshal(map[string]string{"intent": text})
		encoded, err := Encode(utf8Packet("holder-"+text, data))
		if err != nil {
			t.Fatalf("%q refused: %v", text, err)
		}
		decoded, err := DecodePacket(encoded)
		if err != nil {
			t.Fatalf("%q did not decode: %v", text, err)
		}
		var body map[string]string
		if err := json.Unmarshal(decoded.Events[0].Data, &body); err != nil {
			t.Fatal(err)
		}
		if decoded.Author.ID != "holder-"+text || body["intent"] != text {
			t.Errorf("%q came back as %q and %q", text, decoded.Author.ID, body["intent"])
		}
		again, err := Encode(decoded)
		if err != nil || string(again) != string(encoded) {
			t.Errorf("%q is not a fixed point of Encode: %v", text, err)
		}
	}
}
