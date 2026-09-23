package model

// Wire and validation tests for review.admit packet capture times (U14
// review: criterion freezing is decided against them). Packet authors live in
// author_record_test.go.

import (
	"reflect"
	"testing"
	"time"
)

func capturedReview() *ReviewAdmit {
	e := authoredReview()
	at := time.Date(2026, 9, 22, 20, 46, 24, 567286000, time.UTC)
	later, last := at.Add(time.Second), at.Add(2*time.Second)
	e.CapturedAt = map[ID]Availability[time.Time]{schemaID(1): {State: Known, Value: &at}, schemaID(2): {State: Known, Value: &later}, schemaID(3): {State: Known, Value: &last}}
	return e
}

func TestReviewCapturedAtRoundTrip(t *testing.T) {
	want := capturedReview()
	raw, err := EncodeEvent(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeEvent(raw)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip: got %+v, want %+v: %v", got, want, err)
	}
}

func TestReviewCapturedAtInvalidSchema(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*ReviewAdmit)
	}{
		{"omitted", func(e *ReviewAdmit) { e.CapturedAt = nil }},
		{"empty map", func(e *ReviewAdmit) { e.CapturedAt = map[ID]Availability[time.Time]{} }},
		{"missing packet", func(e *ReviewAdmit) { delete(e.CapturedAt, schemaID(2)) }},
		{"wrong packet", func(e *ReviewAdmit) {
			now := time.Now().UTC()
			delete(e.CapturedAt, schemaID(2))
			e.CapturedAt[schemaID(4)] = Availability[time.Time]{State: Known, Value: &now}
		}},
		{"zero time", func(e *ReviewAdmit) {
			var zero time.Time
			e.CapturedAt[schemaID(1)] = Availability[time.Time]{State: Known, Value: &zero}
		}},
		{"known without a time", func(e *ReviewAdmit) { e.CapturedAt[schemaID(1)] = Availability[time.Time]{State: Known} }},
		{"unknown without a reason", func(e *ReviewAdmit) { e.CapturedAt[schemaID(1)] = Availability[time.Time]{State: Unknown} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := capturedReview()
			tc.edit(e)
			if _, err := EncodeEvent(e); err == nil {
				t.Fatal("encoder accepted an invalid capture record")
			}
		})
	}
}
