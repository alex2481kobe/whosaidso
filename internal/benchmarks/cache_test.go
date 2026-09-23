// This file checks and measures the snapshot cache on the synthetic fixtures:
// whole-state and answer parity with full replay, and the cost of a warm
// load, a +1 catch-up and a cold rebuild. Command pipelines are measured in
// commands_test.go; fixture construction lives in fixture_test.go.
package benchmarks

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/query"
	"datum/internal/store"
)

// privateCopy copies the fixture's config and ledger under a fresh root, so
// appended bundles and cache images never touch the shared fixture. The copy
// keeps the project id, so it reads the same intake.
func privateCopy(t testing.TB, f *fixture, root string) *fixture {
	t.Helper()
	g := *f
	g.t = t
	g.Project.Root, g.Project.Ledger = root, filepath.Join(root, ".datum", "events")
	config, err := os.ReadFile(filepath.Join(f.Project.Root, "datum.toml"))
	must(t, err)
	put(t, filepath.Join(root, "datum.toml"), config)
	entries, err := os.ReadDir(f.Project.Ledger)
	must(t, err)
	must(t, os.MkdirAll(g.Project.Ledger, 0o755))
	for _, e := range entries {
		if e.Name()[0] == '.' {
			continue
		}
		data, err := os.ReadFile(filepath.Join(f.Project.Ledger, e.Name()))
		must(t, err)
		must(t, os.WriteFile(filepath.Join(g.Project.Ledger, e.Name()), data, 0o644))
	}
	return &g
}

// appendOrdinary publishes count ordinary one-task bundles after the head,
// written directly: a synthetic tail, not a measured admission.
func appendOrdinary(t testing.TB, f *fixture, count int) {
	t.Helper()
	head, err := store.Replayed(f.Project)
	must(t, err)
	w := head.Snapshot().Watermark()
	prev, seq := w.CommandID, w.Sequence
	for i := 0; i < count; i++ {
		event, err := model.EncodeEvent(f.task())
		must(t, err)
		seq++
		b := model.Bundle{Version: model.WireVersion, Project: f.Project.ID, Sequence: seq, CommandID: f.id(), Predecessor: prev,
			RequestDigest: model.HashBytes([]byte(fmt.Sprint("tail", seq))), RecordedAt: time.Now().UTC(), Admitter: reviewer, Events: []model.Event{event}}
		data, err := model.Encode(b)
		must(t, err)
		name, err := model.BundleName(b.Sequence, b.CommandID)
		must(t, err)
		must(t, os.WriteFile(filepath.Join(f.Project.Ledger, name), data, 0o644))
		prev = b.CommandID
	}
}

func cachedParity(t *testing.T, f *fixture, state string, restored int) {
	t.Helper()
	loaded, err := store.Load(f.Project)
	must(t, err)
	full, err := store.Replayed(f.Project)
	must(t, err)
	if !reflect.DeepEqual(loaded.Snapshot(), full.Snapshot()) {
		t.Fatalf("%s: the cached path's whole state differs from full replay", state)
	}
	if loaded.Restored() != restored {
		t.Fatalf("%s: restored %d bundles, want %d", state, loaded.Restored(), restored)
	}
	at := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	observed := query.Observation{ObservedAt: known(at), Head: unknown[model.GitHead](), Dirty: unknown[bool]()}
	for _, c := range readCases(f) {
		request := c.request
		if request.View == "continue" {
			request.Observed = &observed
		}
		want, err := query.ReadViewFrom(f.Project, request, full)
		must(t, err)
		got, err := query.ReadView(f.Project, request)
		must(t, err)
		var a, b bytes.Buffer
		must(t, query.RenderViewJSON(&a, want))
		must(t, query.RenderViewJSON(&b, got))
		if !bytes.Equal(a.Bytes(), b.Bytes()) {
			t.Fatalf("%s: %s answers differently through the cache", state, c.name)
		}
	}
}

// Whole reducer state and every read answer equal full replay on the 1k
// fixture, cold, warm, and after 1 and 100 appended bundles.
func TestCachedStateEqualsFullReplayOnTheFixture(t *testing.T) {
	f := privateCopy(t, getFixture(t, 1000), t.TempDir())
	cachedParity(t, f, "cold", 0)
	cachedParity(t, f, "warm", 1000)
	appendOrdinary(t, f, 1)
	cachedParity(t, f, "+1", 1000)
	appendOrdinary(t, f, 100)
	cachedParity(t, f, "+100", 1001)
	cachedParity(t, f, "warm again", 1101)
}

// BenchmarkCache measures the loader alone on a private copy of each fixture:
// Warm restores a matching image; CatchUp1 restores an image one bundle
// behind, folds that bundle and publishes a new image; ColdRebuild has no
// image, replays everything and publishes one. Every case still reads and
// hashes the whole ledger. Restoring the starting image is untimed.
func BenchmarkCache(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("N%d", n), func(b *testing.B) {
			f := privateCopy(b, getFixture(b, n), b.TempDir())
			image := filepath.Join(f.Project.CacheDir(), "snapshot")
			_, err := store.Load(f.Project)
			must(b, err)
			base, err := os.ReadFile(image)
			must(b, err)
			b.ReportMetric(float64(len(base)), "image-B")
			load := func(b *testing.B, restored int) {
				s, err := store.Load(f.Project)
				must(b, err)
				if s.Restored() != restored {
					b.Fatalf("restored %d bundles, want %d", s.Restored(), restored)
				}
			}
			b.Run("Warm", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					load(b, n)
				}
			})
			appendOrdinary(b, f, 1)
			b.Run("CatchUp1", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					must(b, os.WriteFile(image, base, 0o600))
					b.StartTimer()
					load(b, n)
				}
			})
			b.Run("ColdRebuild", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					must(b, os.Remove(image))
					b.StartTimer()
					load(b, 0)
				}
			})
		})
	}
}
