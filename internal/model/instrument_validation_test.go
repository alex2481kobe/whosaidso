package model

// Schema tests for the authored validation field of an instrument, on both
// instrument.declare and instrument.revise. Whether authored KNOWN validation
// is legal is not decided here: the acceptance suite currently requires it.

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestInstrumentUnknownValidationSchemaOnDeclareAndRevise(t *testing.T) {
	for _, revision := range []bool{false, true} {
		name := "declare"
		if revision {
			name = "revise"
		}
		t.Run(name, func(t *testing.T) {
			makeEvent := func(spec InstrumentSpec) TypedEvent {
				if revision {
					return &InstrumentRevise{Target: schemaRef(1), Provenance: schemaProvenance(), Replacement: spec}
				}
				return &InstrumentDeclare{ID: schemaID(1), Provenance: schemaProvenance(), Spec: spec}
			}
			spec := schemaInstrument()
			spec.Validation.Reason = "  nobody checked a known-answer case\n"
			goodSchemaValue(t, spec)
			raw := requireSchemaGood(t, makeEvent(spec))
			decoded, err := DecodeEvent(raw)
			if err != nil || !reflect.DeepEqual(decoded, makeEvent(spec)) {
				t.Fatalf("honest UNKNOWN validation changed on the wire: %+v, %v", decoded, err)
			}
			for _, validation := range []Availability[InstrumentValidation]{
				{State: Unknown},
				{State: Unknown, Reason: "​"},
				{State: Unknown, Reason: "unchecked", Value: ptr(InstrumentValidation{Ref: schemaArtifact(), Version: "1"})},
				{State: "assumed", Reason: "unchecked"},
			} {
				spec.Validation = validation
				badSchemaValue(t, spec)
				e := makeEvent(spec)
				if _, err := EncodeEvent(e); err == nil {
					t.Errorf("encoder accepted validation %+v", validation)
				}
				// Bypass the encoder to test the untrusted-byte boundary independently.
				data, err := json.Marshal(e)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := DecodeEvent(Event{Type: e.EventType(), Data: data}); err == nil {
					t.Errorf("decoder accepted validation %+v", validation)
				}
			}
		})
	}
}
