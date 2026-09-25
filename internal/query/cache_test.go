package query

// The snapshot cache under the rich fixture: whole reducer state and every
// rendered answer equal a full replay's, with the cache cold, warm, deleted,
// damaged, and after 1 and 100 appended bundles.

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/store"
)

func requireCachedParity(t *testing.T, p store.Project, state string, restored int) {
	t.Helper()
	loaded, err := store.Load(p)
	if err != nil {
		t.Fatalf("%s: load: %v", state, err)
	}
	full, err := store.Replayed(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Snapshot(), full.Snapshot()) {
		t.Fatalf("%s: the cached path's whole state differs from full replay", state)
	}
	if loaded.Restored() != restored {
		t.Fatalf("%s: restored %d bundles from the cache, want %d", state, loaded.Restored(), restored)
	}
	for _, r := range richRequests() {
		want, err := ReadViewFrom(p, r, full)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ReadView(p, r)
		if err != nil {
			t.Fatalf("%s: %s: %v", state, r.View, err)
		}
		if rendered(t, got) != rendered(t, want) {
			t.Fatalf("%s: %s %s answers differently through the cache", state, r.View, r.ID)
		}
	}
}

func TestCachedReadsEqualFullReplayOnTheRichFixture(t *testing.T) {
	p := testProject(t)
	richWorld(t, p)
	image := filepath.Join(p.CacheDir(), "snapshot")
	if err := os.RemoveAll(p.CacheDir()); err != nil {
		t.Fatal(err)
	}
	n := int(view_(t, p, ViewRequest{View: "show"}).Header().Watermark.Sequence) // cold: rebuilds and publishes
	requireCachedParity(t, p, "warm", n)
	if err := os.Remove(image); err != nil {
		t.Fatal(err)
	}
	requireCachedParity(t, p, "deleted", 0)
	data, err := os.ReadFile(image)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 0x40
	if err := os.WriteFile(image, data, 0o600); err != nil {
		t.Fatal(err)
	}
	requireCachedParity(t, p, "corrupted", 0)
	// Admissions keep the image current; putting the older image back leaves
	// it behind the ledger, as bundles from a pull would, so reads catch up.
	behind := func(add func()) {
		t.Helper()
		kept, err := os.ReadFile(image)
		if err != nil {
			t.Fatal(err)
		}
		add()
		if err := os.WriteFile(image, kept, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	behind(func() { appendEvents(t, p, 200, testTask(200)) })
	requireCachedParity(t, p, "+1", n)
	behind(func() {
		for i := 0; i < 100; i++ {
			appendEvents(t, p, 300+i, testTask(300+i))
		}
	})
	requireCachedParity(t, p, "+100", n+1)
	requireCachedParity(t, p, "warm again", n+101)
	appendEvents(t, p, 500, testTask(500))
	requireCachedParity(t, p, "admitted", n+102)
}
