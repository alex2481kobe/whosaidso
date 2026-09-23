package evidence

// Run outputs as stored: Datum's own committed runs, recorded before outputs
// were published only once, still resolve; and admission's held run-output
// bytes are verified without a second read. Observation semantics over
// synthetic runs are in observations_test.go and run_dir_test.go.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"datum/internal/model"
)

// ownLedgerPrefix is the fixed committed prefix this test is about. Bundles
// 15-17, 23-25 and 35-37 admitted runs whose outputs were committed twice: in
// .datum/artifacts/runs/<invocation>/ and by digest.
const ownLedgerPrefix = 43

// Every output of every sealed run in that prefix resolves as authored (its
// committed run-dir copy is read first) AND from the content store alone, so a
// later migration may delete .datum/artifacts/runs/ without losing a reading.
func TestOwnLedgerRunOutputsResolveFromTheStoreAlone(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(root, ".datum", "events", "*.json"))
	if err != nil || len(paths) < ownLedgerPrefix {
		t.Fatalf("committed history missing: %d bundles, %v", len(paths), err)
	}
	r := NewResolverAt(root, DefaultArtifactDir)
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
			if !ok || seal.Envelope.OutputRefs.Value == nil {
				continue
			}
			seals++
			for _, ref := range *seal.Envelope.OutputRefs.Value {
				outputs++
				authored, err := r.Resolve(context.Background(), ref)
				if err != nil || authored.Origin != OriginLocator {
					t.Fatalf("bundle %d output %v no longer resolves as authored from its committed run-dir copy: origin %q, %v", bundle.Sequence, ref.Content.Locators, authored.Origin, err)
				}
				storeOnly := ref
				pin := *ref.Content
				pin.Locators = []model.Locator{}
				storeOnly.Content = &pin
				stored, err := r.Resolve(context.Background(), storeOnly)
				if err != nil || stored.Origin != OriginArtifactStore {
					t.Fatalf("bundle %d output %v must resolve from the content store alone: origin %q, %v", bundle.Sequence, ref.Content.Locators, stored.Origin, err)
				}
				if string(stored.Bytes) != string(authored.Bytes) {
					t.Fatalf("bundle %d output %v: store and run-dir copies differ", bundle.Sequence, ref.Content.Locators)
				}
			}
		}
	}
	if seals < 9 || outputs < 2*seals {
		t.Fatalf("expected the nine committed runs and their streams, found %d seals with %d outputs", seals, outputs)
	}
}

// RunOutput verifies bytes admission already holds, with the checks a read
// would apply, and never reaches for another copy on disk.
func TestRunOutputVerifiesHeldBytesOnly(t *testing.T) {
	root := t.TempDir()
	at := RunDir(invocationA) + "/out/result.json"
	body := `{"actual":8}`
	r := NewResolver(root)
	got, err := r.RunOutput(contentRef(body, "application/json", []string{at}, "whole", ""), []byte(body))
	if err != nil || got.Origin != OriginRunOutput || string(got.Bytes) != body || got.DeclaredPath != at {
		t.Fatalf("control: %+v, %v", got, err)
	}
	// A matching file on disk cannot stand in for the held bytes.
	writeFile(t, root, at, body)
	lfs := "version https://git-lfs.github.com/spec/v1\noid sha256:" + string(model.HashBytes([]byte(body))) + "\nsize 12\n"
	gitPinned := contentRef(body, "application/json", []string{at}, "whole", "")
	gitPinned.Git = &model.GitPin{ObjectFormat: "sha1", Commit: "0123456789abcdef0123456789abcdef01234567", Path: at}
	small := NewResolver(root)
	small.MaxBytes = 4
	for _, tc := range []struct {
		name string
		r    *Resolver
		ref  model.ArtifactRef
		held string
		code string
	}{
		{"held bytes differ from the pin", r, contentRef(body, "application/json", []string{at}, "whole", ""), `{"actual":9}`, "conflict"},
		{"held bytes fail the media-type check", r, contentRef("not json", "application/json", []string{at}, "whole", ""), "not json", "conflict"},
		{"held bytes are an LFS pointer", r, contentRef(lfs, "text/plain", []string{at}, "whole", ""), lfs, "unavailable"},
		{"held bytes exceed the byte limit", small, contentRef(body, "application/json", []string{at}, "whole", ""), body, "unavailable"},
		{"a git-pinned output needs corroboration", r, gitPinned, body, "invalid-field"},
		{"an escaping locator", r, contentRef(body, "application/json", []string{"../" + at}, "whole", ""), body, "invalid-field"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.r.RunOutput(tc.ref, []byte(tc.held))
			if code := faultCode(err); code != tc.code {
				t.Fatalf("want %s, got %v", tc.code, err)
			}
		})
	}
}
