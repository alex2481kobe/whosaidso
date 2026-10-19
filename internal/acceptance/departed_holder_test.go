package acceptance_test

// A departed holder's attempt, closed or released by someone else on the
// owner's word: the receipt is admitted only when it cites an authority whose
// admitted words name the task, never on the recorder's own say-so, and it
// stays attributed to whoever recorded it. After a release the task is READY
// to claim again, and the old claim reads ENDED.

import (
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/query"
)

// ownerSays admits a source.intake by speaker naming refs, and returns its id.
func (w *flowWorld) ownerSays(speaker, file string, refs ...model.RecordRef) model.ID {
	w.t.Helper()
	body := []byte(`{"ruling":"release the departed agent's attempts"}`)
	w.put(file, body)
	ref := pvPin(body, file)
	id := w.id()
	w.mustAdmit("clerk", &model.SourceIntake{SourceID: id, SourceRef: ref, OriginalDigest: ref.Content.SHA256, Length: ref.Content.Length,
		Speaker: model.Actor{ID: speaker}, Referents: refs})
	return id
}

// releaseAs captures a stopped receipt for attempt as actor, citing source
// when given, and admits it; it returns the first refusal.
func (w *flowWorld) releaseAs(actor string, attempt, source model.ID) error {
	w.t.Helper()
	args := []string{"handback", "--json", "--command-id", string(w.id()), "--actor", actor, "--attempt-id", string(attempt),
		"--outcome", "stopped", "--reason", "the holder departed; released on the owner's order", "--next-action", "claim it again"}
	if source != "" {
		args = append(args, "--authority-source", string(source))
	}
	out, err := w.cli(nil, args...)
	if err != nil {
		return err
	}
	return w.review("accepted", flowPacket(w.t, out))
}

func TestDepartedHoldersAttemptIsReleasedOnTheOwnersWord(t *testing.T) {
	t.Parallel()
	w := flowNew(t)
	task := w.task("work a departed agent held")
	other := w.task("another task")

	w.refused("a receipt for another holder's attempt with no authority", func() error { return w.releaseAs("coordinator", task.attempt, "") })
	self := w.ownerSays("coordinator", "rulings/self.json", task.ref)
	w.refused("a receipt citing its own recorder as the authority", func() error { return w.releaseAs("coordinator", task.attempt, self) })
	elsewhere := w.ownerSays("owner", "rulings/other.json", other.ref)
	if err := w.releaseAs("coordinator", task.attempt, elsewhere); err == nil || !strings.Contains(err.Error(), "authority-scope") {
		t.Fatalf("an authority whose words name another task must refuse before capture, got %v", err)
	}

	// Captured by hand, past handback's own check: words that were never
	// admitted are no authority.
	unsaid := []byte(`{"ruling":"never admitted"}`)
	w.put("rulings/unsaid.json", unsaid)
	scope := w.scope
	scope.ContextRefs = []model.RecordRef{task.ref}
	forged := &model.AttemptTerminal{Task: task.ref, AttemptID: task.attempt, Outcome: "stopped", Reason: "released", NextAction: "claim it",
		DeliveryRefs: []model.ArtifactRef{}, Authority: &model.Authority{Actor: model.Actor{ID: "owner"}, SourceRef: pvPin(unsaid, "rulings/unsaid.json"),
			Selector: model.Selector{Kind: "whole"}, Scope: scope}}
	w.refused("a receipt citing words no source.intake carries", func() error { return w.review("accepted", w.capture("coordinator", forged)) })

	order := w.ownerSays("owner", "rulings/release.json", task.ref)
	if err := w.releaseAs("coordinator", task.attempt, order); err != nil {
		t.Fatalf("a receipt on the owner's admitted word must admit: %v", err)
	}
	r := w.record(task.ref.RecordID)
	if flowStr(r, "task", "status") != "READY" {
		t.Errorf("a released task is READY to claim again, got %s", flowStr(r, "task", "status"))
	}
	if status, ok := w.attemptClaim(task.attempt); status != query.ClaimEnded || ok {
		t.Errorf("the departed holder's claim reads ENDED, got %s", status)
	}
	out, err := w.cli(nil, "show", "--json", string(task.ref.RecordID))
	if err != nil || !strings.Contains(string(out), `"authority"`) || !strings.Contains(string(out), `"owner"`) {
		t.Errorf("show must carry the receipt's authority: %v %.400s", err, out)
	}
	history, _ := w.cli(nil, "history", "--json", string(task.ref.RecordID))
	if !strings.Contains(string(history), "coordinator") {
		t.Errorf("history must attribute the receipt to whoever recorded it: %.400s", history)
	}
}
