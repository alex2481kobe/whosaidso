package model

// R14.1 proof verdict decoding: supports and refutes round-trip; an absent
// verdict (R18.2) and any other spelling, an explicit blank included, are
// refused.

import "testing"

func TestProofVerdictDecoding(t *testing.T) {
	var proof *ProofAdmit
	for _, e := range schemaEvents() {
		if p, ok := e.(*ProofAdmit); ok {
			proof = p
		}
	}
	var good Event
	for _, verdict := range []string{VerdictSupports, VerdictRefutes} {
		p := *proof
		p.Verdict = verdict
		good = requireSchemaGood(t, &p)
	}
	requireSchemaRefusal(t, mutateSchema(t, good, "verdict", nil, true), "invalid-field")
	for _, bad := range []any{"", " ", "Refutes", "proven", true, nil} {
		requireSchemaRefusal(t, mutateSchema(t, good, "verdict", bad, false), "invalid-field")
	}
	p := *proof
	p.Verdict = ""
	if _, err := EncodeEvent(&p); err == nil {
		t.Fatal("encoder accepted a proof without a verdict")
	}
}
