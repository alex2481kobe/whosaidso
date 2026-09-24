package query

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
)

func TestTaskRevisionHoldersAndUnknownNeverBorrowNearbyActors(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	spec := testTask(1).Spec
	spec.NextActor = model.Actor{UnknownReason: "no next actor was assigned"}
	appendEvents(t, p, 101,
		&model.TaskAmend{Target: testRef(1, 1), Replacement: spec, Provenance: testTask(1).Provenance},
		&model.TaskStart{Task: testRef(1, 2), AttemptID: testID(70), Actor: model.Actor{ID: "worker"}},
		&model.BlockerHold{Task: testRef(1, 2), BlockerID: testID(80), Reason: model.BlockerPrerequisite,
			Actor: model.Actor{UnknownReason: "external owner not named"}, Criterion: "external input arrives"})
	a := showOf(t, p, testID(1))
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
	appendEvents(t, p, 102, admittedAs(102, map[int]string{0: "worker"}, &model.AttemptTerminal{Task: testRef(1, 2), AttemptID: testID(70), Outcome: model.AttemptNoReading,
		Reason: "no reading", NextAction: "wait for input", DeliveryRefs: []model.ArtifactRef{}})...)
	task = showOf(t, p, testID(1)).Records[0].Task
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
	control := todoOf(t, p)
	if len(control.PacketsNotAccepted) != 4 || control.PacketsNotAccepted[0].Disposition != "pending" || control.Watermark.Sequence != 1 {
		t.Fatalf("control four captures must be pending without advancing ledger, got %+v", control)
	}
	for i, outcome := range []string{"accepted", "rejected", "correction-requested"} {
		reviewPacket(t, p, 200+i, refs[i], outcome)
	}
	a := todoOf(t, p)
	if len(a.PacketsNotAccepted) != 3 || a.Watermark.Sequence != 4 {
		t.Fatalf("expected rejected, correction-requested and unreviewed packets at sequence 4, got %+v", a)
	}
	for i, want := range []string{"rejected", "correction-requested", "pending"} {
		if a.PacketsNotAccepted[i].Disposition != want || a.PacketsNotAccepted[i].Packet == nil {
			t.Fatalf("packet %d must retain bytes and disposition %s, got %+v; review must not erase proposals", i, want, a.PacketsNotAccepted[i])
		}
	}
	if a.PacketsNotAccepted[0].Review.Actor.ID != "reviewer" || a.PacketsNotAccepted[0].Review.Reason == "" || a.PacketsNotAccepted[0].Review.Packet != refs[1] {
		t.Fatalf("rejection must explain who rejected which bytes and why, got %+v", a.PacketsNotAccepted[0])
	}
	dir, err := store.IntakeDir(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, string(refs[1].CommandID))); err != nil {
		t.Fatal(err)
	}
	a = todoOf(t, p)
	if len(a.PacketsNotAccepted) != 3 || a.PacketsNotAccepted[0].Disposition != "rejected" || a.PacketsNotAccepted[0].Unavailable == nil || a.PacketsNotAccepted[0].Unavailable.State != "UNKNOWN" || a.PacketsNotAccepted[0].Packet != nil {
		t.Fatalf("missing local intake must preserve the canonical rejection with UNKNOWN bytes, got %+v", a.PacketsNotAccepted)
	}
}

func TestPendingHonorsReviewEventsWithoutEnvelopePackets(t *testing.T) {
	for _, outcome := range []string{"rejected", "correction-requested", "accepted"} {
		t.Run(outcome, func(t *testing.T) {
			p := testProject(t)
			readyControl(t, p)
			first, second := capturePacket(t, p, 2), capturePacket(t, p, 3)
			// Reverse event order to check that packet output still sorts by ID.
			refs := []model.PacketRef{second, first}
			bundle := appendEvents(t, p, 101, &model.ReviewAdmit{
				Packets: refs, Outcome: outcome,
				Actor: model.Actor{ID: "reviewer"}, Reason: "admitted event is authoritative",
				Authors: authoredBy("lane-a", refs), CapturedAt: uncaptured(refs), EventPackets: []model.ID{}})
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
				a := todoOf(t, p)
				if a.Result != "KNOWN" || a.Watermark.Sequence != 2 {
					t.Fatalf("expected successful review at watermark 2, got %+v", a)
				}
				if outcome == "accepted" {
					if len(a.PacketsNotAccepted) != 0 {
						t.Fatalf("accepted packets must leave pending, got %+v", a.PacketsNotAccepted)
					}
				} else {
					if len(a.PacketsNotAccepted) != 2 {
						t.Fatalf("both reviews must survive missing=%t, got %+v", missing, a.PacketsNotAccepted)
					}
					for i, ref := range []model.PacketRef{first, second} {
						packet := a.PacketsNotAccepted[i]
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
				assertViewHonest(t, a) // the brief shows only the answer's own review facts
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
	claim := showOf(t, p, testID(2)).Records[0].Claim
	if claim.Status != reduce.StatusUnmeasured || claim.Support.EvidenceAvailable != reduce.TruthUnknown || claim.Support.ApplicableScope != reduce.TruthFalse {
		t.Fatalf("a new finding must remain an UNMEASURED CLAIM with unknown evidence and no established support, got %+v", claim)
	}
	show, history := showOf(t, p, testID(99)), historyOf(t, p, testID(99))
	for _, h := range []ViewHeader{show.ViewHeader, history.ViewHeader} {
		if h.Result != "UNKNOWN" || h.Reason == "" || h.Watermark.Sequence != 2 || len(show.Records)+len(history.Events)+len(history.Reviews) != 0 {
			t.Fatalf("absent record must answer UNKNOWN with reason and current watermark, got %+v; nearby records cannot fill it", h)
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
	for _, view := range []string{"show", "history", "todo"} {
		before := view_(t, p, ViewRequest{View: view})
		var rendered bytes.Buffer
		if err := RenderViewBrief(&rendered, before); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(p.Root, "generated.txt")
		if err := os.WriteFile(path, rendered.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		after := view_(t, p, ViewRequest{View: view})
		if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(ledger, treeBytes(t, p.Ledger)) || !reflect.DeepEqual(intake, treeBytes(t, dir)) {
			t.Fatalf("%s changed after deleting generated output; the answer and canonical bytes must be identical", view)
		}
	}
}

func TestEmptyAndCorruptReadsAreDifferent(t *testing.T) {
	p := testProject(t)
	for _, view := range []string{"show", "history", "todo"} {
		a := view_(t, p, ViewRequest{View: view}).Header()
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
	if _, err := ReadView(p, ViewRequest{View: "show"}); err == nil {
		t.Fatal("expected corrupt ledger error, got success; a partial answer would hide canonical state")
	}
}
