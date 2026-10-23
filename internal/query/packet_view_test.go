// These tests cover the packet inventory's verified projection and explicit
// local absence. CLI parsing and subprocess behavior belong to cmd tests.
package query

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

// The selected payload must be the exact bytes the ledger reviewed, even when
// its own request digest and blob hashes are internally valid.
func TestSelectedPacketRefMustMatchReview(t *testing.T) {
	p := testProject(t)
	ref := capturePacket(t, p, 1)
	verified, err := store.ReadVerifiedIntake(p, []model.ID{ref.CommandID})
	if err != nil {
		t.Fatal(err)
	}
	wrong := ref
	wrong.Digest = model.HashBytes([]byte("another internally valid packet.json"))
	packet := Packet{CommandID: ref.CommandID, Review: &Review{Review: reduce.Review{Packet: wrong}}}
	if err := attachVerifiedPacket(&packet, verified[0]); err == nil || !strings.Contains(err.Error(), "no longer matches its reviewed bytes") {
		t.Fatalf("a different reviewed digest must refuse the selected payload, got %v", err)
	}
	if packet.Packet != nil {
		t.Fatal("mismatched payload was attached")
	}
}

// A selected packet does not depend on the bytes of another local packet.
func TestSelectedPacketSkipsUnrelatedCorruptPayload(t *testing.T) {
	p, trio := intakeTrio(t)
	t.Setenv("WHOSAIDSO_NO_CACHE", "1")
	tamper(t, trio["pending"].blob)
	a, err := ReadView(p, ViewRequest{View: "packets", ID: testID(1001)})
	if err != nil {
		t.Fatalf("unrelated corrupt payload was opened: %v", err)
	}
	selected := a.(*PacketsAnswer)
	if selected.Packet == nil || selected.Selected == nil || selected.Selected.CommandID != testID(1001) {
		t.Fatalf("selected reviewed packet is missing: %+v", selected)
	}
}

func TestSelectedPacketSkipsUnrelatedMalformedEntry(t *testing.T) {
	p := testProject(t)
	ref := capturePacket(t, p, 1)
	inbox, err := store.IntakeDir(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "unrelated-invalid-entry"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := ReadView(p, ViewRequest{View: "packets", ID: ref.CommandID})
	if err != nil {
		t.Fatalf("unrelated malformed entry blocked selected packet: %v", err)
	}
	if selected := a.(*PacketsAnswer); selected.Packet == nil || selected.Packet.CommandID != ref.CommandID {
		t.Fatalf("selected packet is missing: %+v", selected)
	}
}

func TestSelectedPacketReadHasSizeBudget(t *testing.T) {
	p, trio := intakeTrio(t)
	if err := os.Truncate(trio["pending"].blob, selectedPacketReadBudget+1); err != nil {
		t.Fatal(err)
	}
	_, err := ReadView(p, ViewRequest{View: "packets", ID: testID(1002)})
	if err == nil || !strings.Contains(err.Error(), "read budget") {
		t.Fatalf("oversized selected packet must fail at the read budget, got %v", err)
	}
}

func TestPacketsIncludeReviewedAndLocallyAbsentPayloads(t *testing.T) {
	p := testProject(t)
	t.Setenv("WHOSAIDSO_NO_CACHE", "1")
	pendingRef := capturePacket(t, p, 1)
	rejectedRef := capturePacket(t, p, 2)
	acceptedRef := capturePacket(t, p, 3)
	absentRef := capturePacket(t, p, 4)
	reviewPacket(t, p, 101, rejectedRef, "rejected")
	reviewPacket(t, p, 102, acceptedRef, "accepted")
	reviewPacket(t, p, 103, absentRef, "correction-requested")
	intake, err := store.IntakeDir(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(intake, string(absentRef.CommandID)), filepath.Join(t.TempDir(), "moved-packet")); err != nil {
		t.Fatal(err)
	}
	before := treeBytes(t, p.Root)
	a, err := ReadView(p, ViewRequest{View: "packets"})
	if err != nil {
		t.Fatal(err)
	}
	list := a.(*PacketsAnswer)
	if len(list.Packets) != 4 || list.Packets[0].CommandID != pendingRef.CommandID ||
		list.Packets[1].Disposition != "rejected" || list.Packets[2].Disposition != "accepted" ||
		list.Packets[3].Disposition != "correction-requested" || list.Packets[3].Payload.State != model.Unknown ||
		list.Packets[3].EventCount.State != model.Unknown || list.Packets[3].EventKinds.State != model.Unknown ||
		list.Packets[3].Author.ID != "author" || list.Packets[3].Review == nil ||
		list.Packets[0].Review != nil || list.Packets[0].Payload.State != model.Known {
		t.Fatalf("packet inventory lost a disposition or availability: %+v", list.Packets)
	}
	var encoded bytes.Buffer
	if err := RenderViewJSON(&encoded, a); err != nil || strings.Contains(encoded.String(), `"events": [`) {
		t.Fatalf("inventory must contain headers only: %v %s", err, encoded.String())
	}
	selected, err := ReadView(p, ViewRequest{View: "packets", ID: absentRef.CommandID})
	if err != nil {
		t.Fatal(err)
	}
	one := selected.(*PacketsAnswer)
	if one.Result != "UNKNOWN" || one.Reason == "" || one.Packet != nil || one.Selected == nil || one.Selected.Payload.State != model.Unknown {
		t.Fatalf("absent local payload must stay explicit: %+v", one)
	}
	if after := treeBytes(t, p.Root); !reflect.DeepEqual(after, before) {
		t.Fatal("read changed project files")
	}
}

func TestPacketsRefuseTamperedLocalBytes(t *testing.T) {
	for _, name := range []string{"pending", "rejected", "accepted"} {
		t.Run(name, func(t *testing.T) {
			p, trio := intakeTrio(t)
			t.Setenv("WHOSAIDSO_NO_CACHE", "1")
			tamper(t, trio[name].blob)
			if _, err := ReadView(p, ViewRequest{View: "packets"}); err == nil || !strings.Contains(err.Error(), "blob bytes do not match their identity") {
				t.Fatalf("inventory must reject the tampered %s blob, got %v", name, err)
			}
		})
	}
}

func TestPacketInventoryBytesOnSyntheticIntake(t *testing.T) {
	p := testProject(t)
	for i := 20; i < 220; i++ {
		capturePacket(t, p, i)
	}
	full, err := ReadView(p, ViewRequest{View: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := ReadView(p, ViewRequest{View: "packets"})
	if err != nil {
		t.Fatal(err)
	}
	var before, after bytes.Buffer
	if err := RenderViewJSON(&before, full); err != nil {
		t.Fatal(err)
	}
	if err := RenderViewJSON(&after, list); err != nil {
		t.Fatal(err)
	}
	if after.Len() >= before.Len() || strings.Contains(after.String(), `"events": [`) {
		t.Fatalf("header inventory did not omit packet bodies: todo %d bytes, packets %d bytes", before.Len(), after.Len())
	}
	t.Logf("200 synthetic pending packets: todo --json %d bytes; packets --json %d bytes", before.Len(), after.Len())
}

func TestPacketHeadersSummarizeRepeatedEventKinds(t *testing.T) {
	p := testProject(t)
	event, err := model.EncodeEvent(testTask(1))
	if err != nil {
		t.Fatal(err)
	}
	events := make([]model.Event, 80)
	for i := range events {
		events[i] = event
	}
	if _, err := store.WriteIntake(context.Background(), p, store.IntakeRequest{
		CommandID: testID(1001), Author: model.Actor{ID: "author"}, Events: events,
	}); err != nil {
		t.Fatal(err)
	}
	a, err := ReadView(p, ViewRequest{View: "packets"})
	if err != nil {
		t.Fatal(err)
	}
	header := a.(*PacketsAnswer).Packets[0]
	if header.EventCount.State != model.Known || header.EventCount.Value == nil || *header.EventCount.Value != 80 ||
		header.EventKinds.State != model.Known || header.EventKinds.Value == nil || len(*header.EventKinds.Value) != 1 || (*header.EventKinds.Value)[0] != event.Type {
		t.Fatalf("a header needs the actual event count and distinct kinds: %+v", header)
	}
	todo, err := ReadView(p, ViewRequest{View: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	var text bytes.Buffer
	if err := RenderViewBrief(&text, todo); err != nil || strings.Count(text.String(), string(event.Type)) != 1 || !strings.Contains(text.String(), "events 80 "+string(event.Type)) {
		t.Fatalf("todo's compact header must summarize the repeated kind: %v %s", err, text.String())
	}
}
