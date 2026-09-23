package query

// Tests for the Source seam (source.go): an answer read from a provided
// snapshot is the answer Read gives, and only history needs raw bundles.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

// countingSource records whether a view asked for raw bundles.
type countingSource struct {
	Source
	calls *int
}

func (c countingSource) Bundles() ([]model.Bundle, error) {
	*c.calls++
	return c.Source.Bundles()
}

func richRequests() []Request {
	at := presetStart.Add(3 * time.Hour)
	observed := &Observation{ObservedAt: model.Availability[time.Time]{State: model.Known, Value: &at},
		Head: notKnown[model.GitHead]("not a git checkout"), Dirty: notKnown[bool]("not a git checkout")}
	return []Request{{Command: "show"}, {Command: "show", ID: testID(1)}, {Command: "show", ID: testID(999)}, {Command: "history"},
		{Command: "history", ID: testID(31)}, {Command: "history", SelfAdmitted: model.SelfAdmissionUnknown}, {Command: "intake pending"},
		{Command: "instruments"}, {Command: "state"}, {Command: "now"}, {Command: "todo"}, {Command: "todo", Limit: 1},
		{Command: "context"}, {Command: "context", ID: testID(1)}, {Command: "continue", ID: testID(1), Observed: observed},
		{Command: "disposal-loss", Disposal: &DisposalTarget{Digest: testArtifact().Content.SHA256}}}
}

func rendered(t *testing.T, a Answer) string {
	t.Helper()
	var j, x bytes.Buffer
	if err := RenderJSON(&j, a); err != nil {
		t.Fatal(err)
	}
	if err := RenderText(&x, a); err != nil {
		t.Fatal(err)
	}
	return j.String() + "\n----\n" + x.String()
}

func TestReadFromAProvidedSnapshotAnswersExactlyAsRead(t *testing.T) {
	p := testProject(t)
	richWorld(t, p)
	for _, r := range richRequests() {
		want, err := Read(p, r)
		if err != nil {
			t.Fatalf("control %s read must succeed: %v", r.Command, err)
		}
		source, err := replay(p)
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		got, err := ReadFrom(p, r, countingSource{source, &calls})
		if err != nil {
			t.Fatalf("%s from a provided snapshot: %v", r.Command, err)
		}
		if rendered(t, got) != rendered(t, want) {
			t.Errorf("%s %s from a provided snapshot differs from Read", r.Command, r.ID)
		}
		if needs := r.Command == "history" && r.SelfAdmitted == ""; needs != (calls > 0) {
			t.Errorf("%s asked for raw bundles %d times; only history prints them", r.Command, calls)
		}
	}
	// The answer is the provided prefix's, not a fresh read of the ledger.
	prefix, err := store.ReadPrefix(p)
	if err != nil {
		t.Fatal(err)
	}
	older, err := reduce.Replay(prefix[:2])
	if err != nil {
		t.Fatal(err)
	}
	a, err := ReadFrom(p, Request{Command: "history"}, replayed{older, prefix[:2]})
	if err != nil || a.Watermark.Sequence != 2 || len(a.History) != older.Watermark().Events {
		t.Fatalf("an older provided prefix must answer at its own watermark, got %d with %d events: %v", a.Watermark.Sequence, len(a.History), err)
	}
	if _, err := ReadFrom(p, Request{Command: "nonsense"}, replayed{older, nil}); err == nil {
		t.Fatal("ReadFrom must check the request as Read does")
	}
}

// Pending walks reviews in ledger order, not packet-id order, so a mismatch
// names the review the ledger admitted first.
func TestPendingReportsTheFirstMismatchInLedgerOrder(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	late, early := capturePacket(t, p, 5), capturePacket(t, p, 3)
	reviewPacket(t, p, 101, late, "rejected")
	reviewPacket(t, p, 102, early, "rejected")
	if a := readAnswer(t, p, "intake pending", ""); len(a.Intake) != 2 || a.Intake[0].CommandID != early.CommandID {
		t.Fatalf("control: both rejected packets pend, sorted by id, got %+v", a.Intake)
	}
	dir, err := store.IntakeDir(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []model.PacketRef{late, early} {
		file := filepath.Join(dir, string(ref.CommandID), "packet.json")
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, append(data, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, err = Read(p, Request{Command: "intake pending"})
	if err == nil || !strings.Contains(err.Error(), string(late.CommandID)) {
		t.Fatalf("the first mismatch in ledger order is %s, got %v", late.CommandID, err)
	}
}

// Within one review event, the packets' listed order is the ledger order.
func TestPendingReportsTheFirstMismatchInEventOrder(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	low, high := capturePacket(t, p, 3), capturePacket(t, p, 4)
	appendEvents(t, p, 101, &model.ReviewAdmit{Packets: []model.PacketRef{high, low}, Outcome: "rejected",
		Actor: model.Actor{ID: "reviewer"}, Reason: "listed out of id order"})
	if a := readAnswer(t, p, "intake pending", ""); len(a.Intake) != 2 {
		t.Fatalf("control: both rejected packets pend, got %+v", a.Intake)
	}
	dir, err := store.IntakeDir(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []model.PacketRef{low, high} {
		file := filepath.Join(dir, string(ref.CommandID), "packet.json")
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, append(data, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 20; i++ {
		_, err = Read(p, Request{Command: "intake pending"})
		if err == nil || !strings.Contains(err.Error(), string(high.CommandID)) {
			t.Fatalf("the event lists %s first, got %v", high.CommandID, err)
		}
	}
}
