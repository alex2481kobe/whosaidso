package model

// review.admit's required fields: packets, actor, outcome, reason,
// authors, captured_at and event_packets are present on every review, and a
// stored self_admission is refused. Self-admission is projected by reduce from
// the recorded author and the admitter, never stored. Authors and capture
// times are tested in author_record_test.go and capture_record_test.go.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// reviewFixture is a complete review of three packets with no events before
// it: every required field present, every packet captured at one known time.
func reviewFixture() *ReviewAdmit {
	at := time.Date(2026, 9, 22, 20, 46, 24, 0, time.UTC)
	return &ReviewAdmit{
		Packets: []PacketRef{
			{CommandID: schemaID(1), Digest: HashBytes([]byte("first"))},
			{CommandID: schemaID(2), Digest: HashBytes([]byte("second"))},
			{CommandID: schemaID(3), Digest: HashBytes([]byte("third"))},
		},
		Actor: Actor{ID: "reviewer"}, Outcome: "accepted", Reason: "  exact words\n\t",
		Authors: map[ID]Actor{schemaID(1): {ID: "reviewer"}, schemaID(2): {ID: "agent-b"}, schemaID(3): {UnknownReason: "not recorded"}},
		CapturedAt: map[ID]Availability[time.Time]{
			schemaID(1): {State: Known, Value: &at}, schemaID(2): {State: Known, Value: &at},
			schemaID(3): {State: Unknown, Reason: "not recorded"},
		},
		EventPackets: []ID{},
	}
}

func TestReviewRequiredFields(t *testing.T) {
	raw, err := EncodeEvent(reviewFixture())
	if err != nil {
		t.Fatalf("control: a complete review must encode: %v", err)
	}
	if _, err := DecodeEvent(raw); err != nil {
		t.Fatalf("control: a complete review must decode: %v", err)
	}
	for _, field := range []string{"packets", "actor", "outcome", "reason", "authors", "captured_at", "event_packets"} {
		for _, value := range []string{"", "null"} {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw.Data, &fields); err != nil {
				t.Fatal(err)
			}
			if value == "" {
				delete(fields, field)
			} else {
				fields[field] = json.RawMessage(value)
			}
			data, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeEvent(Event{Type: raw.Type, Data: data}); err == nil {
				t.Errorf("decoder accepted a review with %s %s", field, map[string]string{"": "omitted", "null": "null"}[value])
			}
		}
	}
}

// No review stores self_admission, whatever it says.
func TestReviewStoredSelfAdmissionIsRefused(t *testing.T) {
	raw, err := EncodeEvent(reviewFixture())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw.Data), "self_admission") {
		t.Fatalf("control: the encoded review stores a self-admission: %s", raw.Data)
	}
	for _, value := range []string{"{}", "null", `{"` + string(schemaID(1)) + `":"true"}`, `{"` + string(schemaID(1)) + `":"unknown"}`} {
		data := append(append([]byte{}, raw.Data[:len(raw.Data)-1]...), []byte(`,"self_admission":`+value+`}`)...)
		if _, err := DecodeEvent(Event{Type: raw.Type, Data: data}); err == nil {
			t.Errorf("self_admission=%s accepted", value)
		}
	}
}
