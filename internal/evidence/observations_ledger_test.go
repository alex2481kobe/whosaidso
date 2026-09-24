package evidence

// Run outputs as stored: WhoSaidSo's own committed runs name each output inside
// its run and pin its bytes, which the content store holds by digest; and
// admission's held run-output bytes are verified without a second read.
// Observation semantics over synthetic runs are in observations_test.go and
// output_name_test.go.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"whosaidso/internal/model"
)

// ownLedgerPrefix is the fixed committed prefix this test is about. Bundles
// 9, 15-17, 23-25 and 35-37 admitted runs with their stdout and stderr.
const ownLedgerPrefix = 43

// Every output of every sealed run in that prefix resolves from the content
// store by its digest alone: no recorded path takes part.
func TestOwnLedgerRunOutputsResolveFromTheStoreAlone(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(root, ".whosaidso", "events", "*.json"))
	if err != nil || len(paths) < ownLedgerPrefix {
		t.Fatalf("committed history missing: %d bundles, %v", len(paths), err)
	}
	r := NewResolverAt(root, ".whosaidso/artifacts") // this repository's store; see whosaidso.toml
	seals, outputs := 0, 0
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		bundle, err := model.DecodeBundle(data)
		if err != nil {
			t.Fatal(err)
		}
		if bundle.Sequence > ownLedgerPrefix {
			continue
		}
		for _, raw := range bundle.Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				t.Fatal(err)
			}
			seal, ok := event.(*model.InvocationSeal)
			if !ok || seal.Envelope.Outputs.Value == nil {
				continue
			}
			seals++
			for _, out := range *seal.Envelope.Outputs.Value {
				outputs++
				got, err := r.Resolve(context.Background(), out.Ref())
				if err != nil || got.Origin != OriginArtifactStore || got.SHA256 != out.SHA256 {
					t.Fatalf("bundle %d output %s does not resolve from the content store: origin %q, %v", bundle.Sequence, out.Name, got.Origin, err)
				}
			}
		}
	}
	if seals < 10 || outputs < 2*seals {
		t.Fatalf("expected the ten committed runs and their streams, found %d seals with %d outputs", seals, outputs)
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
