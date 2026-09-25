package write

// Extracting the invocation facts a rejected or correction-requested review
// records lives here. Whether a proof accounts for them, and the
// byte-identity rule against an admitted start or seal, are the reducer's proof
// family checker (internal/reduce/proof_family.go); pending intake and artifact
// evaluation stay in gate_family.go.

import (
	"github.com/alex2481kobe/whosaidso/internal/model"
)

// reviewedInvocations extracts, for a packet set that is not accepted, every
// invocation.start/seal it carried. An event that does not decode as a typed
// event is not an invocation the closed schema can name, so it records nothing.
func reviewedInvocations(outcome string, packets []model.Packet) ([]model.ReviewedInvocation, error) {
	if outcome == "accepted" {
		return nil, nil
	}
	var out []model.ReviewedInvocation
	for _, packet := range packets {
		for _, raw := range packet.Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				continue
			}
			env := invocationEnvelope(event)
			if env == nil {
				continue
			}
			digest, err := model.EnvelopeDigest(*env)
			if err != nil {
				return nil, err
			}
			out = append(out, model.ReviewedInvocation{Packet: packet.CommandID, Event: string(event.EventType()),
				InvocationID: env.InvocationID, CriterionRef: env.CriterionRef, EnvelopeDigest: digest})
		}
	}
	return out, nil
}
