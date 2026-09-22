package write

// Admission-time checks of a run's own envelope live here: that each declared
// output is this run's (read from its run directory or its own captured blobs,
// verified, materialized), and that its configuration names are the ones the
// exact instrument revision declares. Generic artifact resolution, proof
// families and the admission transaction do not.

import (
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"

	"datum/internal/evidence"
	"datum/internal/model"
	"datum/internal/reduce"
)

// runAdmitOutputs proves each output a seal declares existed as THIS run's
// output when its seal is admitted; materializeAdmission then stores it so
// evaluation can read it after the run directory is gone. R9 gives one output form: every locator
// lies in record/artifacts/runs/<invocation-id>/. The bytes must come from
// this packet's own captured blob or from the run's directory, walked without
// symlinks. Never from the content store or any other packet: a same-digest
// copy there (the criterion's example, say) was not produced by this run.
func runAdmitOutputs(root, inbox string, packet model.Packet, env model.InvocationEnvelope) error {
	if env.OutputRefs.State != model.Known || env.OutputRefs.Value == nil {
		return nil
	}
	runDir := evidence.RunDir(env.InvocationID)
	for i, ref := range *env.OutputRefs.Value {
		at := fmt.Sprintf("envelope.output_refs[%d]", i)
		if ref.Content == nil || len(ref.Content.Locators) == 0 {
			return admissionFault("invalid-field", at, "a run output is captured bytes at a path in the run's own directory; it needs a content pin with a locator")
		}
		for j, l := range ref.Content.Locators {
			if path.Clean(l.Path) != l.Path || !strings.HasPrefix(l.Path, runDir+"/") {
				return admissionFault("invalid-field", fmt.Sprintf("%s.content.locators[%d]", at, j),
					fmt.Sprintf("run outputs are declared in the run's own directory %s/, not at %q", runDir, l.Path))
			}
		}
		// materializeAdmission preserves the verified bytes right after this
		// check, from the same blob or run-dir file.
		if blob, why := runOwnBytes(root, inbox, packet.CommandID, *ref.Content); blob == nil {
			return admissionFault("unavailable", at, "this run's output is neither in its captured blobs nor in its run directory: "+why)
		}
	}
	return nil
}

// runOwnBytes returns verified bytes for the pin from this packet's blobs or a
// run-dir locator, or nil and every reason a candidate was not them.
func runOwnBytes(root, inbox string, packet model.ID, pin model.ContentPin) ([]byte, string) {
	var notes []string
	try := func(label string, read func() ([]byte, error)) []byte {
		b, err := read()
		switch {
		case err != nil:
			notes = append(notes, label+": "+err.Error())
		case uint64(len(b)) != pin.Length || model.HashBytes(b) != pin.SHA256:
			notes = append(notes, label+": bytes do not match the pin")
		default:
			return b
		}
		return nil
	}
	own := filepath.Join(inbox, string(packet), "blobs", string(pin.SHA256))
	if b := try("captured blob", func() ([]byte, error) { return admissionBlob(own) }); b != nil {
		return b, ""
	}
	for _, l := range pin.Locators {
		if b := try(l.Path, func() ([]byte, error) { return runReadOwnFile(root, l.Path) }); b != nil {
			return b, ""
		}
	}
	return nil, strings.Join(notes, " | ")
}

func runReadOwnFile(root, rel string) ([]byte, error) {
	f, err := runOutputFile(root, rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, evidence.DefaultMaxBytes+1))
	if err == nil && int64(len(b)) > evidence.DefaultMaxBytes {
		err = fmt.Errorf("exceeds the resolver byte limit")
	}
	return b, err
}

// runAdmissionConfig applies model.ValidateInvocationConfig to a start or seal
// whenever the exact instrument revision it names is resolvable in the snapshot,
// so a hand-captured envelope cannot use knob names `datum run` would refuse.
// An unresolvable revision is left to the reference gates, which refuse it.
func runAdmissionConfig(snapshot reduce.Snapshot, event model.TypedEvent) error {
	var env model.InvocationEnvelope
	switch e := event.(type) {
	case *model.InvocationStart:
		env = e.Envelope
	case *model.InvocationSeal:
		env = e.Envelope
	default:
		return nil
	}
	record, ok := snapshot.Record(env.InstrumentRef)
	if !ok || record.Instrument == nil {
		return nil
	}
	if err := model.ValidateInvocationConfig(env, *record.Instrument); err != nil {
		return admissionFault("invalid-field", "envelope", "configuration names differ from the instrument's declared config surface: "+err.Error())
	}
	return nil
}
