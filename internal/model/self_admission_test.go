package model

// review.admit's required fields (R18.2): packets, actor, outcome, reason,
// authors, captured_at and event_packets are present on every review, and a
// stored self_admission is refused. Self-admission is projected by reduce from
// the recorded author and the admitter, never stored. Authors and capture
// times are tested in author_record_test.go and capture_record_test.go.

import (
	"encoding/json"
	"os"
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
		Authors: map[ID]Actor{schemaID(1): {ID: "reviewer"}, schemaID(2): {ID: "lane-b"}, schemaID(3): {UnknownReason: "not recorded"}},
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

// R18.2: no review stores self_admission, whatever it says.
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

// Bundle 1 of WhoSaidSo's own ledger, as the R18.2 migration left it: its author
// and capture time come from the intake packet whose bytes hash to the digest
// the bundle records, and its reason prose is untouched.
func TestReviewCommittedSequenceOneRecordsItsVerifiedAuthor(t *testing.T) {
	// Read the actual committed history, not a recreated fixture or a prose guess.
	data, err := os.ReadFile("../../.datum/events/00000001-01M3408ER2RFD597S5KPXMYP4P.json")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := DecodeBundle(data)
	if err != nil || bundle.Sequence != 1 {
		t.Fatalf("committed genesis must decode: %+v, %v", bundle, err)
	}
	count := 0
	for _, raw := range bundle.Events {
		e, err := DecodeEvent(raw)
		if err != nil {
			t.Fatal(err)
		}
		review, ok := e.(*ReviewAdmit)
		if !ok {
			continue
		}
		count++
		packet := review.Packets[0].CommandID
		if a := review.Authors[packet]; a != (Actor{ID: "coordinator"}) {
			t.Fatalf("committed author is not the verified intake author: %+v", a)
		}
		want := time.Date(2026, 9, 22, 7, 28, 26, 222920000, time.UTC)
		if c := review.CapturedAt[packet]; c.State != Known || c.Value == nil || !c.Value.Equal(want) {
			t.Fatalf("committed capture time is not the verified intake stamp: %+v", c)
		}
		if !strings.Contains(review.Reason, "Self-admitted: true.") {
			t.Fatalf("historical reason changed: %q", review.Reason)
		}
	}
	if count != 1 {
		t.Fatalf("genesis holds %d reviews, want 1", count)
	}
}
