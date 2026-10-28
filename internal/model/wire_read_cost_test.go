// Envelope checks validate raw JSON without allocating one diagnostic path per
// payload byte. Typed payload validation remains a separate boundary.
package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEnvelopeReadAllocationsDoNotScaleWithTextBytes(t *testing.T) {
	allocations := func(length int) float64 {
		p := samplePacket(t, mustID(t, 1))
		payload, err := json.Marshal(map[string]string{"intent": strings.Repeat("x", length)})
		if err != nil {
			t.Fatal(err)
		}
		p.Events = []Event{{Type: "task.create", Data: payload}}
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodePacket(raw); err != nil {
			t.Fatalf("valid envelope: %v", err)
		}
		return testing.AllocsPerRun(1, func() {
			if _, err := DecodePacket(raw); err != nil {
				t.Fatal(err)
			}
		})
	}
	small, large := allocations(64), allocations(64000)
	if large > small*2+100 {
		t.Fatalf("envelope walked each raw byte: %.0f -> %.0f allocations", small, large)
	}
}
