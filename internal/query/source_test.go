package query

// Tests for the Source seam (source.go): an answer read from a provided
// snapshot is the answer ReadView gives, and only history needs raw bundles.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

// replayed is a Source over any prefix, so a test can hand ReadViewFrom one older
// than the ledger's.
type replayed struct {
	snapshot reduce.Snapshot
	bundles  []model.Bundle
}

func (r replayed) Snapshot() reduce.Snapshot        { return r.snapshot }
func (r replayed) Bundles() ([]model.Bundle, error) { return r.bundles, nil }

func replay(p store.Project) (Source, error) { return store.Replayed(p) }

// countingSource records whether a view asked for raw bundles.
type countingSource struct {
	Source
	calls *int
}

func (c countingSource) Bundles() ([]model.Bundle, error) {
	*c.calls++
	return c.Source.Bundles()
}

func richRequests() []ViewRequest {
	return []ViewRequest{{View: "show"}, {View: "show", ID: testID(1)}, {View: "show", ID: testID(999)}, {View: "history"},
		{View: "history", ID: testID(31)}, {View: "history", SelfAdmitted: model.SelfAdmissionUnknown},
		{View: "show", Kind: "instrument"}, {View: "show", Kind: "claim"}, {View: "todo"}, {View: "todo", Limit: 1},
		{View: "continue", ID: testID(1), Observed: richObservation()}, {View: "continue", ID: testID(22), Limit: 1}}
}

func rendered(t *testing.T, a ViewAnswer) string {
	t.Helper()
	var j, x bytes.Buffer
	if err := RenderViewJSON(&j, a); err != nil {
		t.Fatal(err)
	}
	if err := RenderViewBrief(&x, a); err != nil {
		t.Fatal(err)
	}
	return j.String() + "\n----\n" + x.String()
}

func TestReadViewFromAProvidedSnapshotAnswersExactlyAsReadView(t *testing.T) {
	p := testProject(t)
	richWorld(t, p)
	for _, r := range richRequests() {
		want, err := ReadView(p, r)
		if err != nil {
			t.Fatalf("control %s view must succeed: %v", r.View, err)
		}
		source, err := replay(p)
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		got, err := ReadViewFrom(p, r, countingSource{source, &calls})
		if err != nil {
			t.Fatalf("%s from a provided snapshot: %v", r.View, err)
		}
		if rendered(t, got) != rendered(t, want) {
			t.Errorf("%s %s from a provided snapshot differs from ReadView", r.View, r.ID)
		}
		if needs := r.View == "history" && r.SelfAdmitted == ""; needs != (calls > 0) {
			t.Errorf("%s asked for raw bundles %d times; only history prints them", r.View, calls)
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
	v, err := ReadViewFrom(p, ViewRequest{View: "history"}, replayed{older, prefix[:2]})
	if err != nil {
		t.Fatal(err)
	}
	if a := v.(*HistoryAnswer); a.Watermark.Sequence != 2 || len(a.Events) != older.Watermark().Events {
		t.Fatalf("an older provided prefix must answer at its own watermark, got %d with %d events", a.Watermark.Sequence, len(a.Events))
	}
	if _, err := ReadViewFrom(p, ViewRequest{View: "nonsense"}, replayed{older, nil}); err == nil {
		t.Fatal("ReadViewFrom must check the request as ReadView does")
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
	if a := todoOf(t, p); len(a.IntakePending) != 2 || a.IntakePending[0].CommandID != early.CommandID {
		t.Fatalf("control: both rejected packets pend, sorted by id, got %+v", a.IntakePending)
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
	_, err = ReadView(p, ViewRequest{View: "todo"})
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
	if a := todoOf(t, p); len(a.IntakePending) != 2 {
		t.Fatalf("control: both rejected packets pend, got %+v", a.IntakePending)
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
		_, err = ReadView(p, ViewRequest{View: "todo"})
		if err == nil || !strings.Contains(err.Error(), string(high.CommandID)) {
			t.Fatalf("the event lists %s first, got %v", high.CommandID, err)
		}
	}
}
