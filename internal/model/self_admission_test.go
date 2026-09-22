package model

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func selfAdmissionReview() *ReviewAdmit {
	return &ReviewAdmit{
		Packets: []PacketRef{
			{CommandID: schemaID(1), Digest: HashBytes([]byte("first"))},
			{CommandID: schemaID(2), Digest: HashBytes([]byte("second"))},
			{CommandID: schemaID(3), Digest: HashBytes([]byte("third"))},
		},
		Actor: Actor{ID: "reviewer"}, Outcome: "accepted", Reason: "  exact words\n\t",
		SelfAdmission: map[ID]SelfAdmissionState{
			schemaID(1): SelfAdmissionTrue, schemaID(2): SelfAdmissionFalse, schemaID(3): SelfAdmissionUnknown,
		},
	}
}

func TestReviewSelfAdmissionRoundTrip(t *testing.T) {
	for _, outcome := range []string{"accepted", "rejected", "correction-requested"} {
		t.Run(outcome, func(t *testing.T) {
			want := selfAdmissionReview()
			want.Outcome = outcome
			raw, err := EncodeEvent(want)
			if err != nil {
				t.Fatal(err)
			}
			got, err := DecodeEvent(raw)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("round trip: got %+v, want %+v: %v", got, want, err)
			}
			// Pin the public wire spelling, not just a Go-to-Go round trip.
			var wire struct {
				SelfAdmission map[string]string `json:"self_admission"`
			}
			if err := json.Unmarshal(raw.Data, &wire); err != nil {
				t.Fatal(err)
			}
			for i, want := range []string{"true", "false", "unknown"} {
				if got := wire.SelfAdmission[string(schemaID(i+1))]; got != want {
					t.Errorf("packet %d wire state = %q, want %q", i+1, got, want)
				}
			}
		})
	}
}

func TestReviewSelfAdmissionInvalidSchema(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*ReviewAdmit)
	}{
		{"empty map", func(e *ReviewAdmit) { e.SelfAdmission = map[ID]SelfAdmissionState{} }},
		{"missing packet", func(e *ReviewAdmit) { delete(e.SelfAdmission, schemaID(2)) }},
		{"extra packet", func(e *ReviewAdmit) { e.SelfAdmission[schemaID(4)] = SelfAdmissionTrue }},
		{"wrong packet", func(e *ReviewAdmit) {
			delete(e.SelfAdmission, schemaID(2))
			e.SelfAdmission[schemaID(4)] = SelfAdmissionFalse
		}},
		{"invalid state", func(e *ReviewAdmit) { e.SelfAdmission[schemaID(1)] = "yes" }},
		{"empty state", func(e *ReviewAdmit) { e.SelfAdmission[schemaID(1)] = "" }},
		{"unknown admitter true", func(e *ReviewAdmit) { e.Actor = Actor{UnknownReason: "not recorded"} }},
		{"unknown admitter false", func(e *ReviewAdmit) {
			e.Actor = Actor{UnknownReason: "not recorded"}
			e.SelfAdmission[schemaID(1)] = SelfAdmissionUnknown
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := selfAdmissionReview()
			tc.edit(e)
			if _, err := EncodeEvent(e); err == nil {
				t.Fatal("encoder accepted invalid structured self-admission")
			}
			// Marshal without validation so the decoder is independently exercised.
			data, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "empty map" {
				// omitempty hides an empty map; put the invalid explicit field back.
				data = append(data[:len(data)-1], []byte(`,"self_admission":{}}`)...)
			}
			if _, err := DecodeEvent(Event{Type: "review.admit", Data: data}); err == nil {
				t.Fatal("decoder accepted invalid structured self-admission")
			}
		})
	}
}

func TestReviewSelfAdmissionStrictWire(t *testing.T) {
	raw, err := EncodeEvent(selfAdmissionReview())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, before, after string }{
		{"unknown field", `"self_admission":`, `"self_admitted":`},
		{"case alias", `"self_admission":`, `"Self_Admission":`},
		{"boolean", `"true"`, `true`},
		{"null state", `"true"`, `null`},
		{"uppercase state", `"true"`, `"TRUE"`},
		{"duplicate packet", `"self_admission":{`, `"self_admission":{"` + string(schemaID(1)) + `":"false",`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := strings.Replace(string(raw.Data), tc.before, tc.after, 1)
			if data == string(raw.Data) {
				t.Fatal("fixture replacement did not apply")
			}
			if _, err := DecodeEvent(Event{Type: raw.Type, Data: []byte(data)}); err == nil {
				t.Fatal("strict decoder accepted malformed self-admission")
			}
		})
	}
	legacy := selfAdmissionReview()
	legacy.SelfAdmission = nil
	raw, err = EncodeEvent(legacy)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"null", "[]", "true", `"unknown"`} {
		data := append(append([]byte{}, raw.Data[:len(raw.Data)-1]...), []byte(`,"self_admission":`+value+`}`)...)
		if _, err := DecodeEvent(Event{Type: raw.Type, Data: data}); err == nil {
			t.Errorf("self_admission=%s accepted", value)
		}
	}
}

func TestReviewSelfAdmissionUnknownAndLegacy(t *testing.T) {
	e := selfAdmissionReview()
	e.Actor = Actor{UnknownReason: "identity unavailable"}
	for id := range e.SelfAdmission {
		e.SelfAdmission[id] = SelfAdmissionUnknown
	}
	if _, err := EncodeEvent(e); err != nil {
		t.Fatalf("unknown comparison must be representable: %v", err)
	}
	e.SelfAdmission = nil
	raw, err := EncodeEvent(e)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw.Data), "self_admission") {
		t.Fatal("legacy round trip invented a structured fact")
	}
	decoded, err := DecodeEvent(raw)
	if err != nil || decoded.(*ReviewAdmit).SelfAdmission != nil {
		t.Fatalf("legacy omission was not retained: %+v, %v", decoded, err)
	}
}

func TestReviewSelfAdmissionDoesNotRelaxRequiredFields(t *testing.T) {
	raw, err := EncodeEvent(selfAdmissionReview())
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"packets", "actor", "outcome", "reason"} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw.Data, &fields); err != nil {
			t.Fatal(err)
		}
		delete(fields, field)
		data, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeEvent(Event{Type: raw.Type, Data: data}); err == nil {
			t.Errorf("decoder accepted review without %s", field)
		}
	}
}

func TestReviewSelfAdmissionCommittedSequenceOne(t *testing.T) {
	// Read the actual committed history, not a recreated fixture or a prose guess.
	data, err := os.ReadFile("../../record/events/00000001-01M3408ER2RFD597S5KPXMYP4P.json")
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
		if review.SelfAdmission != nil || !strings.Contains(review.Reason, "Self-admitted: true.") {
			t.Fatalf("historical fact or reason changed: %+v", review)
		}
		roundTrip, err := EncodeEvent(review)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeEvent(roundTrip)
		if err != nil || !reflect.DeepEqual(decoded, review) {
			t.Fatalf("legacy review round trip changed: %+v, %v", decoded, err)
		}
	}
	if count != 1 {
		t.Fatalf("expected the committed review, got %d", count)
	}
}
