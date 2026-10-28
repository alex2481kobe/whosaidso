// Capture times belong to the exact reviewed packet. An unattributed event
// stays UNKNOWN even when its bundle has a recorded time or a known neighbor.
package query

import (
	"context"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func TestRecordTimesNeverUseAnotherPacketsTimestamp(t *testing.T) {
	p := screenProject(t)
	appendEvents(t, p, 1001, testTask(1))
	packet := testID(2001)
	ref := model.PacketRef{CommandID: packet, Digest: model.HashBytes([]byte("fixture"))}
	captured := time.Unix(1700000000, 0).UTC()
	task, err := model.EncodeEvent(testTask(2))
	if err != nil {
		t.Fatal(err)
	}
	review, err := model.EncodeEvent(&model.ReviewAdmit{Packets: []model.PacketRef{ref}, Outcome: "accepted", Actor: model.Actor{ID: "reviewer"}, Reason: "fixture reviewed", EventPackets: []model.ID{packet}, Authors: map[model.ID]model.Actor{packet: {ID: "contributor"}}, CapturedAt: map[model.ID]model.Availability[time.Time]{packet: {State: model.Known, Value: &captured}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Transact(context.Background(), p, testID(1002), model.HashBytes([]byte("second fixture")), func([]model.Bundle) (model.Bundle, error) {
		return model.Bundle{Admitter: model.Actor{ID: "reviewer"}, Packets: []model.PacketRef{ref}, Events: []model.Event{task, review}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	known, err := ReadRecordTimes(p, testID(2))
	if err != nil {
		t.Fatal(err)
	}
	at := known.Times["2:0"]
	if len(known.Times) != 1 || known.Watermark.Sequence != 2 || at.State != model.Known || at.Value == nil || !at.Value.Equal(captured) {
		t.Fatalf("exact capture: %+v", known)
	}
	unknown, err := ReadRecordTimes(p, testID(1))
	if err != nil {
		t.Fatal(err)
	}
	at = unknown.Times["1:0"]
	if len(unknown.Times) != 1 || at.State != model.Unknown || at.Value != nil || at.Reason == "" {
		t.Fatalf("invented timestamp: %+v", unknown)
	}
	if _, err := ReadRecordTimes(p, ""); err == nil {
		t.Fatal("unscoped time request accepted")
	}
}
