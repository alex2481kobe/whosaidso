package model

// Wire and validation tests for review.admit packet authors and event
// attribution. Self-admission states live in
// self_admission_test.go.

import (
	"encoding/json"
	"reflect"
	"testing"
)

func authoredReview() *ReviewAdmit {
	e := reviewFixture()
	e.Authors = map[ID]Actor{
		schemaID(1): {ID: "reviewer"}, schemaID(2): {ID: "agent-b"}, schemaID(3): {UnknownReason: "captured without identity"},
	}
	e.EventPackets = []ID{schemaID(2), schemaID(1), schemaID(2)}
	return e
}

func TestReviewAuthorsRoundTrip(t *testing.T) {
	want := authoredReview()
	raw, err := EncodeEvent(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeEvent(raw)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip: got %+v, want %+v: %v", got, want, err)
	}
	var wire struct {
		Authors      map[string]Actor `json:"authors"`
		EventPackets []string         `json:"event_packets"`
	}
	if err := json.Unmarshal(raw.Data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Authors[string(schemaID(2))].ID != "agent-b" || len(wire.EventPackets) != 3 {
		t.Fatalf("public wire spelling changed: %s", raw.Data)
	}
}

func TestReviewAuthorsInvalidSchema(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*ReviewAdmit)
	}{
		{"empty map", func(e *ReviewAdmit) { e.Authors = map[ID]Actor{} }},
		{"missing packet", func(e *ReviewAdmit) { delete(e.Authors, schemaID(2)) }},
		{"wrong packet", func(e *ReviewAdmit) {
			delete(e.Authors, schemaID(2))
			e.Authors[schemaID(4)] = Actor{ID: "agent-b"}
		}},
		{"blank actor", func(e *ReviewAdmit) { e.Authors[schemaID(1)] = Actor{} }},
		{"both branches", func(e *ReviewAdmit) { e.Authors[schemaID(1)] = Actor{ID: "a", UnknownReason: "b"} }},
		{"event names unreviewed packet", func(e *ReviewAdmit) { e.EventPackets[1] = schemaID(4) }},
		{"events on a rejected review", func(e *ReviewAdmit) { e.Outcome = "rejected" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := authoredReview()
			tc.edit(e)
			if _, err := EncodeEvent(e); err == nil {
				t.Fatal("encoder accepted an invalid packet author record")
			}
		})
	}
	if _, err := EncodeEvent(authoredReview()); err != nil {
		t.Fatalf("control: the unedited review must encode: %v", err)
	}
	// Omission is never read as unknown; an unknown author is explicit.
	for _, edit := range []func(*ReviewAdmit){
		func(e *ReviewAdmit) { e.Authors = nil },
		func(e *ReviewAdmit) { e.EventPackets = nil },
	} {
		e := authoredReview()
		edit(e)
		if _, err := EncodeEvent(e); err == nil {
			t.Fatal("encoder accepted a review with authors or event_packets omitted")
		}
	}
}
