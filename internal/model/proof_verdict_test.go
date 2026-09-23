package model

// R14.1 proof verdict decoding: supports and refutes round-trip, an absent
// verdict is the legacy shape and decodes, and any other spelling, an
// explicit blank included, is refused.

import "testing"

func TestProofVerdictDecoding(t *testing.T) {
	var proof *ProofAdmit
	for _, e := range schemaEvents() {
		if p, ok := e.(*ProofAdmit); ok {
			proof = p
		}
	}
	legacy := requireSchemaGood(t, proof)
	for _, verdict := range []string{VerdictSupports, VerdictRefutes} {
		p := *proof
		p.Verdict = verdict
		requireSchemaGood(t, &p)
	}
	decoded, err := DecodeEvent(legacy)
	if err != nil || decoded.(*ProofAdmit).Verdict != "" || decoded.(*ProofAdmit).Refutes() {
		t.Fatalf("a legacy proof without a verdict must decode as a supports proof: %v", err)
	}
	for _, bad := range []any{"", " ", "Refutes", "proven", true} {
		requireSchemaRefusal(t, mutateSchema(t, legacy, "verdict", bad, false), "invalid-field")
	}
}
