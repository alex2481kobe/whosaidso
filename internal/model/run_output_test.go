package model

// A run output's recorded form: a canonical name, unique within its run, and a
// content pin with no locator. Where the store keeps the bytes is not here.

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRunOutputNamesAreCanonicalAndUniqueWithinARun(t *testing.T) {
	sealWith := func(names ...string) *InvocationSeal {
		env := schemaEnvelope()
		env.ObservedAt = schemaKnown(env.StartedAt.Add(time.Second))
		env.Outcome = schemaKnown(ProcessOutcome{Kind: "exit", ExitCode: ptr(0)})
		env.ConfigEffective = schemaKnown(map[string]Availability[Scalar]{"sample_count": schemaKnown(schemaNumber("0"))})
		env.ConditionsObserved = schemaKnown(map[string]Availability[Scalar]{})
		outs := []RunOutput{}
		for _, n := range names {
			outs = append(outs, RunOutput{Name: n, SHA256: HashBytes([]byte(n)), Length: uint64(len(n)), MediaType: "application/json"})
		}
		env.Outputs = schemaKnown(outs)
		return &InvocationSeal{StartRef: InvocationRef{Project: env.ExecutionSourceIdentity.Project, InvocationID: env.InvocationID}, Envelope: env}
	}
	raw := requireSchemaGood(t, sealWith("stdout", "out/result.json"))
	for _, tc := range []struct{ name string }{{"out/./result.json"}, {"out//result.json"}, {"out/result.json/"}, {"./out/result.json"}, {"../result.json"}, {"/abs"}, {" "}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := EncodeEvent(sealWith("stdout", tc.name)); err == nil {
				t.Fatalf("encoded an output named %q", tc.name)
			}
			requireSchemaRefusal(t, withOutputName(t, raw, 1, tc.name), "invalid-field")
		})
	}
	t.Run("one name twice", func(t *testing.T) {
		if _, err := EncodeEvent(sealWith("stdout", "stdout")); err == nil {
			t.Fatal("encoded two outputs named stdout")
		}
		requireSchemaRefusal(t, withOutputName(t, raw, 1, "stdout"), "invalid-field")
	})
}

// withOutputName rewrites output i's name in encoded seal bytes, so the decoder,
// not the encoder, is the entrance under test.
func withOutputName(t *testing.T, raw Event, i int, name string) Event {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal(raw.Data, &data); err != nil {
		t.Fatal(err)
	}
	outs := data["envelope"].(map[string]any)["outputs"].(map[string]any)["value"].([]any)
	outs[i].(map[string]any)["name"] = name
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return Event{Type: raw.Type, Data: b}
}
