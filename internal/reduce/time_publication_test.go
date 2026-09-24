package reduce_test

// UTC-only timestamps through durable ledger publication live here. It is an
// external test because it publishes through store, which imports reduce.

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

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
)

// This test was first written to PROVE a hole: a +00:37
// invocation timestamp survived packet JSON, durable store.Transact
// publication, ledger reads and Replay, and assigning through its private
// decoded Location() rewrote it in every snapshot at once. The wire now
// carries only UTC. EncodeEvent writes an in-process timestamp as UTC, and
// decoding refuses any other offset, so no snapshot can hold a private
// location. This is a spec change, not a test bent to fit code.
func TestInvocationTimestampIsUTCThroughLedgerPublication(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate the real intake publisher
	root := t.TempDir()
	project := store.Project{ID: reduce.TestProject, Root: root, Ledger: filepath.Join(root, ".whosaidso", "events")}
	l, env, _ := reduce.SealStart(t)
	env.StartedAt = time.Date(2026, 9, 22, 12, 0, 0, 0, time.FixedZone("fixture", 37*60))
	// This one bundle also fixes the claim, so the run names no criterion:
	// freezing is tested in freeze_test.go, UTC publication here.
	env.CriterionRef = reduce.ProofUnknownCriterion()
	instant := env.StartedAt
	// Reuse only the prerequisite events, never the in-memory snapshot.
	events := []model.Event{}
	for _, b := range l.Bundles()[:3] {
		for _, e := range b.Events {
			if e.Type != "review.admit" { // this bundle's own review attributes them
				events = append(events, e)
			}
		}
	}
	for _, event := range []model.TypedEvent{&model.InvocationStart{Envelope: env}, reduce.SealProof(env, 0)} {
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
		Version: model.WireVersion, Project: project.ID, CommandID: reduce.NewID("PKT1"),
		RequestDigest: reduce.NewDigest("zone packet"), Author: model.Actor{ID: "agent-a"}, // it carries agent-a's criterion
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
	bundle, err := store.Transact(context.Background(), project, reduce.NewID("CMD1"), reduce.NewDigest("zone admission"),
		func(prefix []model.Bundle) (model.Bundle, error) {
			base, err := reduce.Replay(prefix)
			if err != nil {
				return model.Bundle{}, err
			}
			// The review attributes every event to the packet. Its capture
			// time is the fixture's, not this machine's clock, so the start
			// stays bounded by it whenever the test runs.
			captured := instant.Add(time.Minute).UTC()
			carried := make([]model.ID, len(packet.Events))
			for i := range carried {
				carried[i] = packetRef.CommandID
			}
			review, err := model.EncodeEvent(&model.ReviewAdmit{Packets: []model.PacketRef{packetRef}, Outcome: "accepted",
				Actor: model.Actor{ID: "reviewer"}, Reason: "zone publication", EventPackets: carried, Authors: map[model.ID]model.Actor{packetRef.CommandID: packet.Author},
				CapturedAt: map[model.ID]model.Availability[time.Time]{packetRef.CommandID: {State: model.Known, Value: &captured}}})
			if err != nil {
				return model.Bundle{}, err
			}
			proposal := model.Bundle{Admitter: model.Actor{ID: "reviewer"},
				Packets: []model.PacketRef{packetRef},
				Events:  append(append([]model.Event{}, packet.Events...), review)}
			check := proposal
			check.Version, check.Project = model.WireVersion, project.ID
			check.CommandID, check.RequestDigest = reduce.NewID("CMD1"), reduce.NewDigest("zone admission")
			check.Sequence, check.RecordedAt = 1, reduce.BaseTime
			_, err = reduce.Apply(base, check)
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
	before, err := reduce.Replay(prefix)
	if err != nil {
		t.Fatal(err)
	}
	got := before.Invocations()[0]
	for field, stamp := range map[string]time.Time{
		"start.started_at": got.Start.StartedAt, "seal.started_at": got.Seal.StartedAt,
		"seal.observed_at": got.Seal.ObservedAt.Value.Add(-time.Second),
	} {
		if stamp.Location() != time.UTC || !stamp.Equal(instant) {
			t.Fatalf("%s must come back as the same instant in UTC, got %s", field, stamp.Format(time.RFC3339Nano))
		}
	}
	fork := reduce.NewFixtureLedger()
	fork.After(bundle)
	after, err := reduce.Apply(before, fork.Add(t, &model.TaskCreate{
		ID: reduce.NewID("TSKB"), Provenance: reduce.FixtureProvenance("agent-b"), Spec: reduce.FixtureTaskSpec(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	// Both forks hold time.UTC, never a private location a caller could
	// reassign. Never assign through time.UTC itself: that is process-wide.
	for name, snapshot := range map[string]reduce.Snapshot{"earlier": before, "later": after} {
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
	if _, err := reduce.Replay([]model.Bundle{decoded}); !errors.As(err, &fault) || !strings.HasSuffix(fault.Path, "started_at") {
		t.Fatalf("a ledger offset must be refused at started_at, got %v", err)
	}
}
