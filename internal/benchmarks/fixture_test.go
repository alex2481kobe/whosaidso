// This file builds isolated, replay-validated performance fixtures and owns their
// lifecycle. Measurement loops and domain-specific event constructors live elsewhere.
package benchmarks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
	"datum/internal/write"
)

// Run with -run '^$' -bench BenchmarkCommands -benchmem -benchtime=1x.
// DATUM_BENCH_ROOT optionally retains fixtures across processes; use an empty,
// lane-prefixed scratch directory. HOME is isolated inside this test binary only;
// the invoking Go tool uses its configured shared cache, without a GOCACHE override.
// Large fixtures use Encode/DecodeBundle, ReadVerifiedIntake and Replay, then write
// validated immutable files in bulk. They do not pay O(N²) historical admission
// setup. TestFixtureAdmissionControl exercises the same ten-bundle cycle through
// real write.Admit. Only timed writes claim admission/durability performance.
var benchRoot string

func TestMain(m *testing.M) {
	benchRoot = os.Getenv("DATUM_BENCH_ROOT")
	remove := benchRoot == ""
	var err error
	if remove {
		benchRoot, err = os.MkdirTemp("", "astraeff-bench-")
	}
	if err == nil {
		err = os.MkdirAll(filepath.Join(benchRoot, "home"), 0700)
	}
	if err == nil {
		err = os.Setenv("HOME", filepath.Join(benchRoot, "home"))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	if remove {
		_ = os.RemoveAll(benchRoot)
	}
	os.Exit(code)
}

type fixture struct {
	Project       store.Project
	Task          model.RecordRef
	Claim         model.RecordRef
	Instrument    model.RecordRef
	Criterion     model.CriterionRef
	Attempt       model.ID
	Proof         model.ProofAdmit
	Next          int
	Bundles       int
	Events        int
	LedgerBytes   int
	IntakePackets int
	BlobBytes     int
	t             testing.TB
	prefix        []model.Bundle
	realAdmission bool
	building      bool
}

var author = model.Actor{ID: "measurement-agent"}
var reviewer = model.Actor{ID: "independent-reviewer"}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) id() model.ID {
	f.Next++
	return model.ID(fmt.Sprintf("%026d", f.Next))
}

func (f *fixture) ref(id model.ID) model.RecordRef {
	return model.RecordRef{Project: f.Project.ID, RecordID: id, Revision: 1}
}

func (f *fixture) capture(blobs [][]byte, events ...model.TypedEvent) model.PacketRef {
	f.t.Helper()
	raw := make([]model.Event, len(events))
	for i, event := range events {
		var err error
		raw[i], err = model.EncodeEvent(event)
		must(f.t, err)
	}
	if f.building && !f.realAdmission {
		return f.bulkCapture(blobs, raw)
	}
	readers := make([]io.Reader, len(blobs))
	for i, blob := range blobs {
		readers[i] = bytes.NewReader(blob)
	}
	ref, err := store.WriteIntake(context.Background(), f.Project, store.IntakeRequest{
		CommandID: f.id(), Author: author, Events: raw, Blobs: readers,
	})
	must(f.t, err)
	return ref
}

func (f *fixture) request(ref model.PacketRef) write.AdmitRequest {
	return write.AdmitRequest{CommandID: f.id(), PacketIDs: []model.ID{ref.CommandID},
		Admitter: reviewer, Outcome: "accepted", Reason: "reviewed full measurement and its stated limitations"}
}

func (f *fixture) append(blobs [][]byte, events ...model.TypedEvent) {
	f.t.Helper()
	ref := f.capture(blobs, events...)
	request := f.request(ref)
	var bundle model.Bundle
	if f.realAdmission {
		var err error
		bundle, err = write.Admit(context.Background(), f.Project, request)
		must(f.t, err)
	} else {
		verified, err := store.ReadVerifiedIntake(f.Project, []model.ID{ref.CommandID})
		must(f.t, err)
		packet := verified[0].Packet
		attribution := make([]model.ID, len(packet.Events))
		for i := range attribution {
			attribution[i] = packet.CommandID
		}
		review, err := model.EncodeEvent(&model.ReviewAdmit{Packets: []model.PacketRef{ref},
			Outcome: "accepted", Actor: reviewer, Reason: request.Reason,
			Authors:    map[model.ID]model.Actor{packet.CommandID: author},
			CapturedAt: map[model.ID]model.Availability[time.Time]{packet.CommandID: known(packet.CapturedAt)}, EventPackets: attribution})
		must(f.t, err)
		// Use the same request identity shape as write.admissionDigest.
		identity, err := model.Encode(struct {
			Project model.ProjectID   `json:"project"`
			Packets []model.PacketRef `json:"packets"`
			Actor   model.Actor       `json:"actor"`
			Outcome string            `json:"outcome"`
			Reason  string            `json:"reason"`
		}{f.Project.ID, []model.PacketRef{ref}, reviewer, "accepted", request.Reason})
		must(f.t, err)
		bundle = model.Bundle{Version: model.WireVersion, Project: f.Project.ID,
			Sequence: uint64(len(f.prefix) + 1), CommandID: request.CommandID,
			RequestDigest: model.HashBytes(identity), RecordedAt: time.Now().UTC(),
			Admitter: reviewer, Packets: []model.PacketRef{ref}, Events: append(packet.Events, review)}
		if len(f.prefix) > 0 {
			bundle.Predecessor = f.prefix[len(f.prefix)-1].CommandID
		}
	}
	f.prefix = append(f.prefix, bundle)
	f.Events += len(bundle.Events)
	f.IntakePackets++
	for _, blob := range blobs {
		f.BlobBytes += len(blob)
	}
}

func put(t testing.TB, path string, data []byte) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(path), 0700))
	must(t, os.WriteFile(path, data, 0600))
}

func getFixture(t testing.TB, n int) *fixture {
	t.Helper()
	dir := filepath.Join(benchRoot, fmt.Sprintf("n%d", n))
	meta := filepath.Join(dir, "fixture.json")
	if data, err := os.ReadFile(meta); err == nil {
		f := &fixture{t: t}
		must(t, json.Unmarshal(data, f))
		return f
	}
	f := buildFixture(t, dir, n, false)
	data, err := json.Marshal(f)
	must(t, err)
	put(t, meta, data)
	return f
}

func buildFixture(t testing.TB, dir string, n int, realAdmission bool) *fixture {
	t.Helper()
	f := &fixture{t: t, Bundles: n, realAdmission: realAdmission, building: true,
		Project: store.Project{ID: model.ProjectID("astraeff/" + filepath.Base(dir) + "/" + string(model.HashBytes([]byte(dir)))[:8]), Root: dir, Ledger: filepath.Join(dir, ".datum", "events")}}
	put(t, filepath.Join(dir, "datum.toml"), []byte(fmt.Sprintf("id = %q\nledger = %q\n", f.Project.ID, ".datum/events")))
	for i := 0; i < n/10; i++ {
		f.cycle(i)
	}
	// Validate the complete generated history with the public reducer before
	// publishing bulk fixture files. No production state is modified.
	s, err := reduce.Replay(f.prefix)
	must(t, err)
	if s.Watermark().Bundles != n {
		t.Fatalf("fixture has %d bundles, want %d", s.Watermark().Bundles, n)
	}
	for _, bundle := range f.prefix {
		data, err := model.Encode(bundle)
		must(t, err)
		_, err = model.DecodeBundle(data)
		must(t, err)
		name, err := model.BundleName(bundle.Sequence, bundle.CommandID)
		must(t, err)
		if !realAdmission {
			put(t, filepath.Join(f.Project.Ledger, name), data)
		}
		f.LedgerBytes += len(data)
	}
	f.prefix = nil
	f.building = false
	return f
}

func TestFixtureAdmissionControl(t *testing.T) {
	f := buildFixture(t, filepath.Join(t.TempDir(), "control"), 10, true)
	prefix, err := store.ReadPrefix(f.Project)
	must(t, err)
	s, err := reduce.Replay(prefix)
	must(t, err)
	claim, ok := s.ClaimAt(f.Claim)
	if !ok || claim.Status != reduce.StatusProven || len(claim.Observations) != 3 {
		t.Fatalf("control must have a PROVEN claim with three observations: %+v", claim)
	}
	// The independent bulk builder must preserve the same event mix/counts.
	bulk := buildFixture(t, filepath.Join(t.TempDir(), "bulk"), 10, false)
	if bulk.Events != f.Events {
		t.Fatal("bulk builder changed the workflow")
	}
}
