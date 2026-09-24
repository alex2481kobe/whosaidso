package write

// Admission-time checks of a run's own envelope live here: that each declared
// output is this run's (read from its own packet's captured blobs, verified,
// handed to publication). Generic artifact resolution, publication, proof
// families and the admission transaction do not.

import (
	"fmt"

	"whosaidso/internal/model"
)

// runOwnOutput is one declared output with the bytes proven to be this run's.
type runOwnOutput struct {
	out   model.RunOutput
	bytes []byte
}

// runAdmitOutputs proves each output a seal declares existed as THIS run's
// output when its seal is admitted, and returns those verified bytes so
// materializeAdmission publishes exactly them, without reading them again.
// An output is a name inside the run and a content pin (R9); its bytes must
// be in the seal packet's own captured blobs. Never from the content store,
// any other packet or any path: a same-digest copy there (the criterion's
// example, say) was not produced by this run.
func runAdmitOutputs(inbox string, packet model.Packet, env model.InvocationEnvelope, dry *dryRun) ([]runOwnOutput, error) {
	if env.Outputs.State != model.Known || env.Outputs.Value == nil {
		return nil, nil
	}
	own := make([]runOwnOutput, 0, len(*env.Outputs.Value))
	for i, out := range *env.Outputs.Value {
		// A captured blob is named by its verified digest; the length and
		// media type are checked against these bytes by Resolver.RunOutput.
		b, err := dry.packetBlob(inbox, packet.CommandID, out.SHA256)
		if err != nil {
			return nil, admissionFault("unavailable", fmt.Sprintf("envelope.outputs[%d]", i),
				"this run's output "+out.Name+" is not in its own packet's captured blobs: "+err.Error())
		}
		own = append(own, runOwnOutput{out: out, bytes: b})
	}
	return own, nil
}
