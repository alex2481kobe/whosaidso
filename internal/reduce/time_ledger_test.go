package reduce

// UTC-only timestamps through durable ledger publication, and copy costs, live here.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/store"
)

// Ruling R8.4 flipped this test. It was written to PROVE the hole: a +00:37
// invocation timestamp survived packet JSON, durable store.Transact
// publication, ledger reads and Replay, and assigning through its private
// decoded Location() rewrote it in every snapshot at once. The wire now
// carries only UTC. EncodeEvent writes an in-process timestamp as UTC, and
// decoding refuses any other offset, so no snapshot can hold a private
// location. This is a spec change, not a test bent to fit code.
func TestInvocationTimestampIsUTCThroughLedgerPublication(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate the real intake publisher
	root := t.TempDir()
	project := store.Project{ID: testProject, Root: root, Ledger: filepath.Join(root, "record", "events")}
	l, env, _ := sealStart(t)
	env.StartedAt = time.Date(2026, 9, 22, 12, 0, 0, 0, time.FixedZone("fixture", 37*60))
	instant := env.StartedAt
	// Reuse only the prerequisite events, never the in-memory snapshot.
	events := []model.Event{}
	for _, b := range l.out[:3] {
		events = append(events, b.Events...)
	}
	for _, event := range []model.TypedEvent{&model.InvocationStart{Envelope: env}, sealProof(env, 0)} {
		raw, err := model.EncodeEvent(event)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, raw)
	}
	if env.StartedAt.Location().String() != "fixture" {
		t.Fatal("EncodeEvent must normalise a private copy, not the caller's value")
	}
	// Exercise packet encoding/decoding as well as bundle encoding/decoding.
	packetBytes, err := model.Encode(model.Packet{
		Version: model.WireVersion, Project: project.ID, CommandID: newID("PKT1"),
		RequestDigest: newDigest("zone packet"), Author: model.Actor{ID: "runner"},
		CapturedAt: env.StartedAt, Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := model.DecodePacket(packetBytes)
	if err != nil {
		t.Fatal(err)
	}
	if packet.CapturedAt.Location() != time.UTC {
		t.Fatal("packet's own timestamp was not normalized to UTC")
	}
	packetRef, err := store.WriteIntake(context.Background(), project, store.IntakeRequest{
		CommandID: packet.CommandID, Author: packet.Author, Events: packet.Events,
	})
	if err != nil {
		t.Fatal(err)
	}
	captured, err := store.ReadIntake(project, []model.ID{packetRef.CommandID})
	if err != nil {
		t.Fatal(err)
	}
	packet = captured[0]
	// Transact is the real durable publisher; write.Admit's narrower operation
	// gate is tested separately. Do not confuse publication with that gate.
	bundle, err := store.Transact(context.Background(), project, newID("CMD1"), newDigest("zone admission"),
		func(prefix []model.Bundle) (model.Bundle, error) {
			base, err := Replay(prefix)
			if err != nil {
				return model.Bundle{}, err
			}
			proposal := model.Bundle{Admitter: model.Actor{ID: "reviewer"},
				Packets: []model.PacketRef{packetRef},
				Events:  packet.Events}
			check := proposal
			check.Version, check.Project = model.WireVersion, project.ID
			check.CommandID, check.RequestDigest = newID("CMD1"), newDigest("zone admission")
			check.Sequence, check.RecordedAt = 1, baseTime
			_, err = Apply(base, check)
			return proposal, err
		})
	if err != nil {
		t.Fatal(err)
	}
	name, err := model.BundleName(bundle.Sequence, bundle.CommandID)
	if err != nil {
		t.Fatal(err)
	}
	ledgerBytes, err := os.ReadFile(filepath.Join(project.Ledger, name))
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, ledgerBytes); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(compact.Bytes(), []byte(`"started_at":"2026-09-22T11:23:00Z"`)) || bytes.Contains(ledgerBytes, []byte("+00:37")) {
		t.Fatalf("ledger must carry the invocation instant in UTC only: %s", compact.Bytes())
	}
	prefix, err := store.ReadPrefix(project)
	if err != nil {
		t.Fatal(err)
	}
	before := mustReplay(t, prefix)
	got := before.Invocations()[0]
	for field, stamp := range map[string]time.Time{
		"start.started_at": got.Start.StartedAt, "seal.started_at": got.Seal.StartedAt,
		"seal.observed_at": got.Seal.ObservedAt.Value.Add(-time.Second),
	} {
		if stamp.Location() != time.UTC || !stamp.Equal(instant) {
			t.Fatalf("%s must come back as the same instant in UTC, got %s", field, stamp.Format(time.RFC3339Nano))
		}
	}
	fork := newLedger()
	fork.seq, fork.prev = bundle.Sequence, bundle.CommandID
	after, err := Apply(before, fork.add(t, &model.TaskCreate{
		ID: newID("TSKB"), Provenance: provenance("lane-b"), Spec: taskSpec(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	// Both forks hold time.UTC, never a private location a caller could
	// reassign. Never assign through time.UTC itself: that is process-wide.
	for name, snapshot := range map[string]Snapshot{"earlier": before, "later": after} {
		if snapshot.Invocations()[0].Start.StartedAt.Location() != time.UTC {
			t.Fatalf("the %s snapshot holds a non-UTC location", name)
		}
	}
	// Ledger bytes carrying an offset are refused on replay, not normalised.
	tampered := bytes.Replace(ledgerBytes, []byte("2026-09-22T11:23:00Z"), []byte("2026-09-22T12:00:00+00:37"), 1)
	decoded, err := model.DecodeBundle(tampered)
	if err != nil {
		t.Fatal(err)
	}
	var fault *model.Fault
	if _, err := Replay([]model.Bundle{decoded}); !errors.As(err, &fault) || !strings.HasSuffix(fault.Path, "started_at") {
		t.Fatalf("a ledger offset must be refused at started_at, got %v", err)
	}
}

// A test-only candidate measures the cost of a detached Location struct. Its
// private tables remain shared, but ordinary Go cannot overwrite those tables.
// This deliberately does not change production copy semantics.
func detachedTime(stamp time.Time) time.Time {
	location := *stamp.Location()
	return stamp.In(&location)
}

func detachInvocationTimes(inv Invocation) Invocation {
	inv.Start.StartedAt = detachedTime(inv.Start.StartedAt)
	if inv.Start.ObservedAt.Value != nil {
		*inv.Start.ObservedAt.Value = detachedTime(*inv.Start.ObservedAt.Value)
	}
	if inv.Seal != nil {
		inv.Seal.StartedAt = detachedTime(inv.Seal.StartedAt)
		if inv.Seal.ObservedAt.Value != nil {
			*inv.Seal.ObservedAt.Value = detachedTime(*inv.Seal.ObservedAt.Value)
		}
	}
	return inv
}

var timeCopySink Invocation

func timeCopySnapshot() Snapshot {
	env := proofEnvelope(ref(newID("CMA1"), 1), newID("RNA"))
	env.StartedAt = env.StartedAt.In(time.FixedZone("cost-fixture", 37*60))
	inv := Invocation{Start: env, Seal: &sealProof(env, 0).Envelope}
	st := newState()
	st.invocations[InvocationKey{Project: testProject, InvocationID: env.InvocationID}] = inv
	return Snapshot{st: st}
}

func TestDetachedLocationCandidateCost(t *testing.T) {
	utc := baseTime.UTC()
	copied := detachedTime(utc)
	if copied.Location() == time.UTC || copied == utc || !copied.Equal(utc) {
		t.Fatal("detached UTC must lose pointer/struct equality but retain instant equality")
	}
	snapshot := timeCopySnapshot()
	current := testing.AllocsPerRun(100, func() { timeCopySink = snapshot.Invocations()[0] })
	candidate := testing.AllocsPerRun(100, func() {
		timeCopySink = detachInvocationTimes(snapshot.Invocations()[0])
	})
	t.Logf("one sealed invocation, three timestamps: current=%.0f candidate=%.0f delta=%.0f allocations/read",
		current, candidate, candidate-current)
	if candidate-current != 3 {
		t.Fatalf("remeasure candidate cost: expected one extra allocation per timestamp, got %.0f", candidate-current)
	}
	original := snapshot.Invocations()[0].Start.StartedAt.Format(time.RFC3339Nano)
	*timeCopySink.Start.StartedAt.Location() = *time.FixedZone("candidate-mutation", 38*60)
	if snapshot.Invocations()[0].Start.StartedAt.Format(time.RFC3339Nano) != original {
		t.Fatal("candidate failed to detach the returned location")
	}
}

func BenchmarkInvocationLocationCopy(b *testing.B) {
	snapshot := timeCopySnapshot()
	b.Run("current", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			timeCopySink = snapshot.Invocations()[0]
		}
	})
	b.Run("detached-location-candidate", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			timeCopySink = detachInvocationTimes(snapshot.Invocations()[0])
		}
	})
}
