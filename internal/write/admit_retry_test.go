package write

// Admission retries answered from the ledger after intake is gone. Retries
// with intake present are pinned in admit_test.go.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/store"
)

func TestAdmissionRetryNeedsNoIntake(t *testing.T) {
	f := newAdmissionFixture(t)
	f.goodControl()
	one, two := f.capture(nil, f.task()), f.capture(nil, f.task())
	request := f.request(one, two)
	want, err := Admit(context.Background(), f.project, request)
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := store.IntakeDir(f.project)
	if err != nil {
		t.Fatal(err)
	}
	// Losing one packet's intake, then all of it, must not change the answer.
	for _, gone := range []string{filepath.Join(inbox, string(one.CommandID)), filepath.Dir(filepath.Dir(inbox))} {
		if err := os.RemoveAll(gone); err != nil {
			t.Fatal(err)
		}
		got, err := Admit(context.Background(), f.project, request)
		if err != nil {
			t.Fatalf("identical retry after intake loss: %v", err)
		}
		a, errA := model.Encode(got)
		b, errB := model.Encode(want)
		if errA != nil || errB != nil || string(a) != string(b) {
			t.Fatalf("retry returned a different bundle:\n%s\nwant\n%s", a, b)
		}
	}
	changed := map[string]func(*AdmitRequest){
		"reason":     func(r *AdmitRequest) { r.Reason = "a different reason" },
		"admitter":   func(r *AdmitRequest) { r.Admitter = model.Actor{ID: "someone-else"} },
		"outcome":    func(r *AdmitRequest) { r.Outcome = "rejected" },
		"fewer":      func(r *AdmitRequest) { r.PacketIDs = r.PacketIDs[:1] },
		"other":      func(r *AdmitRequest) { r.PacketIDs = []model.ID{one.CommandID, f.id()} },
		"additional": func(r *AdmitRequest) { r.PacketIDs = append(append([]model.ID{}, r.PacketIDs...), f.id()) },
	}
	for name, change := range changed {
		t.Run(name, func(t *testing.T) {
			r := request
			r.PacketIDs = append([]model.ID{}, request.PacketIDs...)
			change(&r)
			f.refuse(r, "conflict")
		})
	}
}

// Corrupt intake answers an identical retry from the ledger, but never lets a
// new admission through: that one must still read and verify its packets.
func TestAdmissionRetryIgnoresCorruptIntakeNewAdmissionDoesNot(t *testing.T) {
	f := newAdmissionFixture(t)
	f.goodControl()
	packet := f.capture(nil, f.task())
	request := f.request(packet)
	want, err := Admit(context.Background(), f.project, request)
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := store.IntakeDir(f.project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, string(packet.CommandID), "packet.json"), []byte("not a packet"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Admit(context.Background(), f.project, request)
	if err != nil {
		t.Fatalf("identical retry over corrupt intake: %v", err)
	}
	a, errA := model.Encode(got)
	b, errB := model.Encode(want)
	if errA != nil || errB != nil || string(a) != string(b) {
		t.Fatalf("retry returned a different bundle:\n%s\nwant\n%s", a, b)
	}
	changed := request
	changed.Outcome = "rejected"
	f.refuse(changed, "conflict")
	f.refuse(f.request(packet), "intake-corrupt")
}
