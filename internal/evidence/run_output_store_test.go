package evidence

// Run outputs as stored: a sealed run names each output inside its run and
// pins its bytes, which the project's content store holds by digest; and
// admission's held run-output bytes are verified without a second read.
// Observation semantics over synthetic runs are in observations_test.go and
// output_name_test.go.

import (
	"context"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// Every output of a sealed run resolves from the configured content store by
// its digest alone: the name recorded inside the run takes no part, even when
// a different file sits at that name in the project.
func TestRunOutputsResolveFromTheStoreAlone(t *testing.T) {
	root := t.TempDir()
	const store = "records/artifacts" // a configured store, not the default one
	outputs := []model.RunOutput{
		runOutput("stdout", "ok  \texample/bench\t1.2s\n", "text/plain"),
		runOutput("stderr", "", "text/plain"),
		runOutput("out/result.json", `{"actual":8}`, "application/json"),
	}
	bodies := []string{"ok  \texample/bench\t1.2s\n", "", `{"actual":8}`}
	for i, out := range outputs {
		writeFile(t, root, store+"/"+string(out.SHA256), bodies[i])
	}
	// A decoy at the recorded name: resolving by name would read it.
	writeFile(t, root, "out/result.json", `{"actual":9}`)
	// The outputs are read back from a sealed run's stored event, as a reader
	// of the ledger would see them.
	env := testEnvelope(t, invocationA)
	env.Outputs = knownOutputs(outputs...)
	env.InputRefs = []model.ArtifactRef{}
	env.Isolation = model.Availability[model.Isolation]{State: model.Unknown, Reason: "not observed in this fixture"}
	observed := env.StartedAt.Add(time.Second)
	env.ObservedAt = model.Availability[time.Time]{State: model.Known, Value: &observed}
	raw, err := model.EncodeEvent(&model.InvocationSeal{StartRef: model.InvocationRef{Project: env.ExecutionSourceIdentity.Project, InvocationID: env.InvocationID}, Envelope: env})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := model.DecodeEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	sealed := *decoded.(*model.InvocationSeal).Envelope.Outputs.Value
	if len(sealed) != len(outputs) {
		t.Fatalf("control: the seal must carry %d outputs, got %d", len(outputs), len(sealed))
	}
	r := NewResolverAt(root, store)
	for i, out := range sealed {
		got, err := r.Resolve(context.Background(), out.Ref())
		if err != nil || got.Origin != OriginArtifactStore || got.SHA256 != out.SHA256 || string(got.Bytes) != bodies[i] {
			t.Fatalf("output %s does not resolve from the content store: origin %q, %v", out.Name, got.Origin, err)
		}
	}
	if _, err := NewResolver(root).Resolve(context.Background(), outputs[2].Ref()); err == nil {
		t.Fatal("control: the default store holds nothing here, so resolution must use the configured one")
	}
}

// RunOutput verifies bytes admission already holds, with the checks a read
// would apply, and never reaches for another copy on disk.
func TestRunOutputVerifiesHeldBytesOnly(t *testing.T) {
	root := t.TempDir()
	body := `{"actual":8}`
	out := runOutput("out/result.json", body, "application/json")
	r := NewResolver(root)
	got, err := r.RunOutput(out, []byte(body))
	if err != nil || got.Origin != OriginRunOutput || string(got.Bytes) != body || got.DeclaredPath != out.Name {
		t.Fatalf("control: %+v, %v", got, err)
	}
	// A matching copy in the store cannot stand in for the held bytes.
	writeFile(t, root, storeCopy(body), body)
	lfs := "version https://git-lfs.github.com/spec/v1\noid sha256:" + string(model.HashBytes([]byte(body))) + "\nsize 12\n"
	small := NewResolver(root)
	small.MaxBytes = 4
	for _, tc := range []struct {
		name string
		r    *Resolver
		out  model.RunOutput
		held string
		code string
	}{
		{"held bytes differ from the pin", r, out, `{"actual":9}`, "conflict"},
		{"held bytes fail the media-type check", r, runOutput("out/result.json", "not json", "application/json"), "not json", "conflict"},
		{"held bytes are an LFS pointer", r, runOutput("out/result.json", lfs, "text/plain"), lfs, "unavailable"},
		{"held bytes exceed the byte limit", small, out, body, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.r.RunOutput(tc.out, []byte(tc.held))
			if code := faultCode(err); code != tc.code {
				t.Fatalf("want %s, got %v", tc.code, err)
			}
		})
	}
}
