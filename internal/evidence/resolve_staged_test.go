package evidence

// Resolver tests for Staged: bytes an admission dry run holds in memory as the
// artifact store's copy. Content pins in general live in resolve_content_test.go.

import (
	"context"
	"strings"
	"testing"

	"whosaidso/internal/model"
)

func TestStagedBytesStandInForTheStoreCopyOnly(t *testing.T) {
	const body = `{"value":1}`
	ref := contentRef(body, "application/json", []string{"gone/example.json"}, "whole", "")
	digest := model.HashBytes([]byte(body))

	// Good control: nothing on disk, the staged copy resolves as the store's.
	r := NewResolverAt(newRepo(t), DefaultArtifactDir)
	r.Staged = map[model.Digest][]byte{digest: []byte(body)}
	got, err := r.Resolve(context.Background(), ref)
	if err != nil || got.Origin != OriginArtifactStore || string(got.Bytes) != body {
		t.Fatalf("staged bytes must resolve as the store copy: %+v, %v", got, err)
	}

	// The authored locator is still tried first.
	root := newRepo(t)
	writeFile(t, root, "gone/example.json", body)
	r = NewResolverAt(root, DefaultArtifactDir)
	r.Staged = map[model.Digest][]byte{digest: []byte(body)}
	if got, err := r.Resolve(context.Background(), ref); err != nil || got.Origin != OriginLocator {
		t.Fatalf("a locator holding the bytes comes before the store: %+v, %v", got, err)
	}

	// Without staging, the same pin does not resolve.
	r = NewResolverAt(newRepo(t), DefaultArtifactDir)
	_, err = r.Resolve(context.Background(), ref)
	wantFault(t, err, "unavailable")

	// Staged bytes are verified like any copy: filed under the pin's digest,
	// different bytes are not the artifact.
	r.Staged = map[model.Digest][]byte{digest: []byte(`{"value":2}`)}
	_, err = r.Resolve(context.Background(), ref)
	wantFault(t, err, "unavailable")
	if !strings.Contains(err.Error(), "hashes to") {
		t.Fatalf("the staged copy must be refused for its digest: %v", err)
	}

	// And held to the resolver's size limit, as a file in the store is.
	r.Staged = map[model.Digest][]byte{digest: []byte(body)}
	r.MaxBytes = int64(len(body)) - 1
	_, err = r.Resolve(context.Background(), ref)
	wantFault(t, err, "unavailable")
	if !strings.Contains(err.Error(), "exceeds the resolver limit") {
		t.Fatalf("a staged copy over the limit must be refused for its size: %v", err)
	}
}
