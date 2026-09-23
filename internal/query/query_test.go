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

func TestPendingHonorsReviewEventsWithoutEnvelopePackets(t *testing.T) {
	for _, outcome := range []string{"rejected", "correction-requested", "accepted"} {
		t.Run(outcome, func(t *testing.T) {
			p := testProject(t)
			readyControl(t, p)
			first, second := capturePacket(t, p, 2), capturePacket(t, p, 3)
			// Reverse event order to check that packet output still sorts by ID.
			bundle := appendEvents(t, p, 101, &model.ReviewAdmit{
				Packets: []model.PacketRef{second, first}, Outcome: outcome,
				Actor: model.Actor{ID: "reviewer"}, Reason: "admitted event is authoritative"})
			if len(bundle.Packets) != 0 {
				t.Fatal("fixture must omit envelope packet references")
			}
			for _, missing := range []bool{false, true} {
				if missing {
					dir, err := store.IntakeDir(p)
					if err != nil {
						t.Fatal(err)
					}
					for _, ref := range []model.PacketRef{first, second} {
						if err := os.RemoveAll(filepath.Join(dir, string(ref.CommandID))); err != nil {
							t.Fatal(err)
						}
					}
				}
				a := readAnswer(t, p, "intake pending", "")
				if a.Result != "KNOWN" || a.Watermark.Sequence != 2 {
					t.Fatalf("expected successful review at watermark 2, got %+v", a)
				}
				if outcome == "accepted" {
					if len(a.Intake) != 0 {
						t.Fatalf("accepted packets must leave pending, got %+v", a.Intake)
					}
				} else {
					if len(a.Intake) != 2 {
						t.Fatalf("both reviews must survive missing=%t, got %+v", missing, a.Intake)
					}
					for i, ref := range []model.PacketRef{first, second} {
						packet := a.Intake[i]
						if packet.CommandID != ref.CommandID || packet.Disposition != outcome || packet.Review == nil ||
							packet.Review.Packet != ref || packet.Review.Outcome != outcome || packet.Review.Actor.ID != "reviewer" ||
							packet.Review.Reason != "admitted event is authoritative" || packet.Review.Origin != (reduce.Origin{Sequence: 2, EventIndex: 0}) {
							t.Fatalf("must retain ordered review attribution for %s, got %+v", ref.CommandID, packet)
						}
						if missing {
							if packet.Packet != nil || packet.Unavailable == nil || packet.Unavailable.State != "UNKNOWN" {
								t.Fatalf("missing local bytes must be UNKNOWN, got %+v", packet)
							}
						} else if packet.Packet == nil || packet.Unavailable != nil {
							t.Fatalf("local packet bytes must remain visible, got %+v", packet)
						}
					}
				}
				var exported, rendered bytes.Buffer
				if err := RenderJSON(&exported, a); err != nil {
					t.Fatal(err)
				}
				if err := RenderText(&rendered, a); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(jsonLeaves(t, exported.Bytes()), textLeaves(t, rendered.String())) {
					t.Fatal("text and JSON must preserve the same review facts")
				}
			}
		})
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
	for _, command := range []string{"show", "history", "intake pending"} {
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
	for _, command := range []string{"show", "history", "intake pending"} {
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
