package write

// Admission-time checks of a run's own envelope live here: that each declared
// output is this run's (read from its own captured blobs or its run directory,
// verified, handed to publication). Generic artifact resolution, publication,
// proof families and the admission transaction do not.

import (
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"

	"datum/internal/evidence"
	"datum/internal/model"
)

// runOwnOutput is one declared output with the bytes proven to be this run's.
type runOwnOutput struct {
	ref   model.ArtifactRef
	bytes []byte
}

// runAdmitOutputs proves each output a seal declares existed as THIS run's
// output when its seal is admitted, and returns those verified bytes so
// materializeAdmission publishes exactly them, without reading them again.
// R9 gives one output form: every locator names a path in
// <artifacts>/runs/<invocation-id>/, the output's logical name. The bytes must
// come from this packet's own captured blob or from a file hand-placed at that
// path, walked without symlinks. Never from the content store or any other
// packet: a same-digest copy there (the criterion's example, say) was not
// produced by this run. write.Run's staging is not a source either: a run's
// seal carries its outputs in its own blobs.
func runAdmitOutputs(root, artifactDir, inbox string, packet model.Packet, env model.InvocationEnvelope) ([]runOwnOutput, error) {
	if env.OutputRefs.State != model.Known || env.OutputRefs.Value == nil {
		return nil, nil
	}
	runDir := evidence.RunDirIn(artifactDir, env.InvocationID)
	own := make([]runOwnOutput, 0, len(*env.OutputRefs.Value))
	for i, ref := range *env.OutputRefs.Value {
		at := fmt.Sprintf("envelope.output_refs[%d]", i)
		if ref.Content == nil || len(ref.Content.Locators) == 0 {
			return nil, admissionFault("invalid-field", at, "a run output is captured bytes at a path in the run's own directory; it needs a content pin with a locator")
		}
		for j, l := range ref.Content.Locators {
			if path.Clean(l.Path) != l.Path || !strings.HasPrefix(l.Path, runDir+"/") {
				return nil, admissionFault("invalid-field", fmt.Sprintf("%s.content.locators[%d]", at, j),
					fmt.Sprintf("run outputs are declared in the run's own directory %s/, not at %q", runDir, l.Path))
			}
		}
		blob, why := runOwnBytes(root, inbox, packet.CommandID, *ref.Content)
		if blob == nil {
			return nil, admissionFault("unavailable", at, "this run's output is neither in its captured blobs nor in its run directory: "+why)
		}
		own = append(own, runOwnOutput{ref: ref, bytes: blob})
	}
	return own, nil
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
