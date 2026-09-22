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
	e.CapturedAt = map[ID]time.Time{schemaID(1): at, schemaID(2): at.Add(time.Second), schemaID(3): at.Add(2 * time.Second)}
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
		{"empty map", func(e *ReviewAdmit) { e.CapturedAt = map[ID]time.Time{} }},
		{"missing packet", func(e *ReviewAdmit) { delete(e.CapturedAt, schemaID(2)) }},
		{"wrong packet", func(e *ReviewAdmit) {
			delete(e.CapturedAt, schemaID(2))
			e.CapturedAt[schemaID(4)] = time.Now().UTC()
		}},
		{"zero time", func(e *ReviewAdmit) { e.CapturedAt[schemaID(1)] = time.Time{} }},
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
