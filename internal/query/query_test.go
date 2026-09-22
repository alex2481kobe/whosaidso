package query

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

func TestTaskRevisionHoldersAndUnknownNeverBorrowNearbyActors(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	spec := testTask(1).Spec
	spec.NextActor = model.Actor{UnknownReason: "no next actor was assigned"}
	appendEvents(t, p, 101,
		&model.TaskAmend{Target: testRef(1, 1), ExpectedRevision: 1, Replacement: spec, Provenance: testTask(1).Provenance},
		&model.TaskStart{Task: testRef(1, 2), AttemptID: testID(70), Actor: model.Actor{ID: "worker"}},
		&model.BlockerHold{Task: testRef(1, 2), BlockerID: testID(80), Reason: model.BlockerPrerequisite,
			Actor: model.Actor{UnknownReason: "external owner not named"}, Criterion: "external input arrives"})
	a := readAnswer(t, p, "show", testID(1))
	task := a.Records[0].Task
	if task.Revision != 2 || task.Status != reduce.StatusInFlight || len(task.AttemptHolders) != 1 || task.AttemptHolders[0].Actor != (model.Actor{ID: "worker"}) {
		t.Fatalf("expected revision 2 IN FLIGHT with worker holding the attempt, got %+v; task revision and holder are separate facts", task)
	}
	if task.ExpectedNextActor != (Unknown{"UNKNOWN", "no next actor was assigned"}) || task.Outcome != "UNKNOWN" || len(task.Blockers) != 1 || task.Blockers[0].Actor.ID != "" {
		t.Fatalf("missing actor/outcome must stay UNKNOWN despite nearby worker and reviewer, got %+v", task)
	}
	if a.Watermark.Sequence != 2 || a.Watermark.Events != 4 || a.Watermark.Head.(Head).CommandID != testID(101) {
		t.Fatalf("expected the whole read's second bundle watermark with 4 events, got %+v; record origin is not the watermark", a.Watermark)
	}
	appendEvents(t, p, 102, &model.AttemptTerminal{Task: testRef(1, 2), AttemptID: testID(70), Outcome: model.AttemptNoReading,
		Reason: "no reading", NextAction: "wait for input", DeliveryRefs: []model.ArtifactRef{}})
	task = readAnswer(t, p, "show", testID(1)).Records[0].Task
	if task.Status != reduce.StatusBlocked || len(task.AttemptHolders) != 0 || len(task.Attempts) != 1 || len(task.Reasons) != 1 {
		t.Fatalf("terminal attempt must retain its receipt but cease holding; remaining hold must explain BLOCKED, got %+v", task)
	}
}

func TestTodoIncludesEveryOpenStatusAndExcludesClosed(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	appendEvents(t, p, 101, testTask(2), testTask(3), testTask(4),
		&model.TaskStart{Task: testRef(2, 1), AttemptID: testID(70), Actor: model.Actor{ID: "worker"}},
		&model.BlockerHold{Task: testRef(3, 1), BlockerID: testID(80), Reason: model.BlockerResume,
			Actor: model.Actor{ID: "owner"}, Criterion: "resume is authorized"},
		&model.TaskClose{Task: testRef(4, 1), Outcome: model.ClosureCancelled,
			Authority:             model.Authority{Actor: model.Actor{ID: "owner"}, SourceRef: testArtifact(), Selector: model.Selector{Kind: "whole"}, Scope: testScope()},
			AcceptanceWitnessRefs: []model.AcceptanceWitness{}, DeliveryWitnessRefs: []model.ArtifactRef{}})
	a := readAnswer(t, p, "task todo", "")
	statuses := []reduce.TaskStatus{}
	for _, record := range a.Records {
		statuses = append(statuses, record.Task.Status)
	}
	want := []reduce.TaskStatus{reduce.StatusReady, reduce.StatusInFlight, reduce.StatusBlocked}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("TODO must contain READY, IN FLIGHT and BLOCKED in identity order and exclude CLOSED; got %v", statuses)
	}
	if got := readAnswer(t, p, "show", testID(4)).Records[0].Task.Status; got != reduce.StatusClosed {
		t.Fatalf("excluded CLOSED task must remain inspectable, got %s", got)
	}
}

func TestPendingRetainsRejectionsCorrectionsAndMissingLocalPackets(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	refs := []model.PacketRef{}
	for _, n := range []int{2, 3, 4, 5} {
		refs = append(refs, capturePacket(t, p, n))
	}
	control := readAnswer(t, p, "intake pending", "")
	if len(control.Intake) != 4 || control.Intake[0].Disposition != "pending" || control.Watermark.Sequence != 1 {
		t.Fatalf("control four captures must be pending without advancing ledger, got %+v", control)
	}
	for i, outcome := range []string{"accepted", "rejected", "correction-requested"} {
		reviewPacket(t, p, 200+i, refs[i], outcome)
	}
	a := readAnswer(t, p, "intake pending", "")
	if len(a.Intake) != 3 || a.Watermark.Sequence != 4 {
		t.Fatalf("expected rejected, correction-requested and unreviewed packets at sequence 4, got %+v", a)
	}
	for i, want := range []string{"rejected", "correction-requested", "pending"} {
		if a.Intake[i].Disposition != want || a.Intake[i].Packet == nil {
			t.Fatalf("packet %d must retain bytes and disposition %s, got %+v; review must not erase proposals", i, want, a.Intake[i])
		}
	}
	if a.Intake[0].Review.Actor.ID != "reviewer" || a.Intake[0].Review.Reason == "" || a.Intake[0].Review.Packet != refs[1] {
		t.Fatalf("rejection must explain who rejected which bytes and why, got %+v", a.Intake[0])
	}
	dir, err := store.IntakeDir(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, string(refs[1].CommandID))); err != nil {
		t.Fatal(err)
	}
	a = readAnswer(t, p, "intake pending", "")
	if len(a.Intake) != 3 || a.Intake[0].Disposition != "rejected" || a.Intake[0].Unavailable == nil || a.Intake[0].Unavailable.State != "UNKNOWN" || a.Intake[0].Packet != nil {
		t.Fatalf("missing local intake must preserve the canonical rejection with UNKNOWN bytes, got %+v", a.Intake)
	}
}

func TestClaimAndMissingRecordDoNotGainGuessedAnswers(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	appendEvents(t, p, 101, &model.ClaimAssert{ID: testID(2), Provenance: testTask(1).Provenance,
		Spec: model.ClaimSpec{Assertion: "U09 may omit a read-time fact", Falsifier: "compare the export with the admitted ledger",
			Scope: testScope(), ExternalRefs: []model.ExternalReference{}}})
	a := readAnswer(t, p, "show", testID(2))
	claim := a.Records[0].Claim
	if claim.Status != reduce.StatusUnmeasured || claim.Support.EvidenceAvailable != reduce.TruthUnknown || claim.Support.ApplicableScope != reduce.TruthFalse {
		t.Fatalf("a new finding must remain an UNMEASURED CLAIM with unknown evidence and no established support, got %+v", claim)
	}
	for _, command := range []string{"show", "history"} {
		a = readAnswer(t, p, command, testID(99))
		if a.Result != "UNKNOWN" || a.Reason == "" || a.Watermark.Sequence != 2 || len(a.Records)+len(a.History) != 0 {
			t.Fatalf("absent record must answer UNKNOWN with reason and current watermark, got %+v; nearby records cannot fill it", a)
		}
	}
}

func TestDeletingGeneratedOutputChangesNeitherAnswerNorCanonicalBytes(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	capturePacket(t, p, 2)
	dir, err := store.IntakeDir(p)
	if err != nil {
		t.Fatal(err)
	}
	ledger, intake := treeBytes(t, p.Ledger), treeBytes(t, dir)
	for _, command := range []string{"show", "history", "task todo", "intake pending"} {
		before := readAnswer(t, p, command, "")
		var rendered bytes.Buffer
		if err := RenderText(&rendered, before); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(p.Root, "generated.txt")
		if err := os.WriteFile(path, rendered.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		after := readAnswer(t, p, command, "")
		if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(ledger, treeBytes(t, p.Ledger)) || !reflect.DeepEqual(intake, treeBytes(t, dir)) {
			t.Fatalf("%s changed after deleting generated output; the answer and canonical bytes must be identical", command)
		}
	}
}

func TestEmptyAndCorruptReadsAreDifferent(t *testing.T) {
	p := testProject(t)
	for _, command := range []string{"show", "history", "task todo", "intake pending"} {
		a := readAnswer(t, p, command, "")
		if a.Result != "KNOWN" || a.Watermark.Sequence != 0 || a.Watermark.Head.(Unknown).State != "UNKNOWN" {
			t.Fatalf("empty control must have known empty inventory and UNKNOWN head, got %+v", a)
		}
	}
	if _, err := os.Stat(p.Ledger); !os.IsNotExist(err) {
		t.Fatalf("empty read must not create a ledger, got %v", err)
	}
	readyControl(t, p)
	if err := os.WriteFile(filepath.Join(p.Ledger, "broken"), []byte("not a bundle"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(p, Request{Command: "show"}); err == nil {
		t.Fatal("expected corrupt ledger error, got success; a partial answer would hide canonical state")
	}
}
