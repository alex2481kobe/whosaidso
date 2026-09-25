package write

// Admission and the snapshot cache through the public write API: the ledger
// is published first, a cache that cannot be written never fails or undoes an
// admission, a crash between the two costs catch-up, and retries answer from
// the ledger.

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/store"
)

func requireCacheParity(t *testing.T, p store.Project, restored int) {
	t.Helper()
	loaded, err := store.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	full, err := store.Replayed(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Snapshot(), full.Snapshot()) {
		t.Fatal("the cached state differs from full replay")
	}
	if loaded.Restored() != restored {
		t.Fatalf("restored %d bundles from the cache, want %d", loaded.Restored(), restored)
	}
}

func TestAdmitPublishesTheLedgerFirstAndTheCacheNeverDecidesIt(t *testing.T) {
	f := newAdmissionFixture(t)
	f.accept(f.capture(nil, f.task()))
	requireCacheParity(t, f.project, 1) // control: admission left the image current
	image := filepath.Join(f.project.CacheDir(), "snapshot")

	// Crash between the ledger publication and the cache publication: the
	// ledger holds the bundle and the image is the one from before.
	before, err := os.ReadFile(image)
	if err != nil {
		t.Fatal(err)
	}
	request := f.request(f.capture(nil, f.task()))
	first, err := Admit(context.Background(), f.project, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(image, before, 0o600); err != nil {
		t.Fatal(err)
	}
	// The identical retry recognises the published bundle, publishes nothing.
	retry, err := Admit(context.Background(), f.project, request)
	if err != nil || retry.Sequence != first.Sequence || retry.CommandID != first.CommandID || retry.RequestDigest != first.RequestDigest || !retry.RecordedAt.Equal(first.RecordedAt) {
		t.Fatalf("an identical retry over a stale image must return the published bundle: %+v, %v", retry, err)
	}
	requireCacheParity(t, f.project, 2)

	// A cache that cannot be written at all: admissions still succeed.
	if err := os.RemoveAll(f.project.CacheDir()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.project.CacheDir(), []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}
	third := f.accept(f.capture(nil, f.task()))
	if third.Sequence != 3 {
		t.Fatalf("admission with an unwritable cache landed at %d", third.Sequence)
	}
	requireCacheParity(t, f.project, 0)
	if err := os.Remove(f.project.CacheDir()); err != nil {
		t.Fatal(err)
	}
	requireCacheParity(t, f.project, 0)
	requireCacheParity(t, f.project, 3)

	// A corrupt image under the lock: the admission reads the ledger's truth.
	if err := os.WriteFile(image, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	fourth := f.accept(f.capture(nil, f.task()))
	if fourth.Sequence != 4 || fourth.Predecessor != third.CommandID {
		t.Fatalf("admission over a corrupt image chained to %s at %d", fourth.Predecessor, fourth.Sequence)
	}
	requireCacheParity(t, f.project, 4)
}
