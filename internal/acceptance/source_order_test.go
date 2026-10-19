package acceptance_test

// Admission refuses a second capture of one speaker's message: a
// source.intake whose known speaker and order an admitted source holds is
// refused, and the refusal names the next unused order. An unknown speaker
// has no sequence, so it never collides.

import (
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestAdmissionRefusesADuplicateSourceOrder(t *testing.T) {
	t.Parallel()
	w := flowNew(t)
	task := w.task("work the owner asked for")
	say := func(file string, speaker model.Actor, order uint64) error {
		body := []byte(`{"said":"` + file + `"}`)
		w.put(file, body)
		ref := pvPin(body, file)
		return w.review("accepted", w.capture("clerk", &model.SourceIntake{SourceID: w.id(), SourceRef: ref, OriginalDigest: ref.Content.SHA256,
			Length: ref.Content.Length, Speaker: speaker, Order: order, Referents: []model.RecordRef{task.ref}}))
	}
	owner := model.Actor{ID: "owner"}
	if err := say("said/a.json", owner, 4); err != nil {
		t.Fatalf("control: the owner's message 4 admits: %v", err)
	}
	err := say("said/b.json", owner, 4)
	if err == nil || !strings.Contains(err.Error(), "duplicate-source-order") || !strings.Contains(err.Error(), "next unused is 5") {
		t.Fatalf("a second message 4 by the owner is refused with the next order: %v", err)
	}
	if err := say("said/c.json", owner, 5); err != nil {
		t.Fatalf("the next order admits: %v", err)
	}
	unknown := model.Actor{UnknownReason: "an unsigned note"}
	if say("said/d.json", unknown, 0) != nil || say("said/e.json", unknown, 0) != nil {
		t.Fatal("an unknown speaker has no sequence to collide in")
	}
}
