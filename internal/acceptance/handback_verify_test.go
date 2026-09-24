package acceptance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/query"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
	"whosaidso/internal/write"
)

// These tests use the public capture/admission/read boundaries, including a
// separately built CLI. A successful capture is not a successful admission.
func hbVerifyStart(t *testing.T, f *gateVerifyFixture, actor model.Actor) (model.RecordRef, write.HandbackRequest) {
	t.Helper()
	task := &model.TaskCreate{ID: f.id(), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}}, Spec: reduceSpec(1)}
	ref := model.RecordRef{Project: f.p.ID, RecordID: task.ID, Revision: 1}
	r := write.HandbackRequest{CommandID: f.id(), Author: actor, AttemptID: f.id(), Outcome: model.AttemptStopped,
		Reason: "  Stopped: e\u0301 / é\r\n", NextAction: "\tOwner decides; commits denied, reconciliation owed?\n"}
	if _, err := f.admit(actor, actor, task, &model.TaskStart{Task: ref, Actor: actor, AttemptID: r.AttemptID}); err != nil {
		t.Fatalf("control: ready task and its first attempt must admit: %v", err)
	}
	p := hbVerifyTask(t, f, ref)
	if p.Status != reduce.StatusInFlight || len(p.Attempts) != 1 || p.Attempts[0].Terminal != nil {
		t.Fatalf("control: expected one live attempt, got %+v", p)
	}
	return ref, r
}

func hbVerifyTask(t *testing.T, f *gateVerifyFixture, ref model.RecordRef) *query.Task {
	t.Helper()
	a, err := gateVerifyShow(f.p, ref.RecordID)
	if err != nil || len(a.Records) != 1 || a.Records[0].Task == nil {
		t.Fatalf("expected task from fresh public read, got %+v, error=%v", a, err)
	}
	return a.Records[0].Task
}

func hbVerifyAdmit(f *gateVerifyFixture, packets ...model.PacketRef) (model.Bundle, error) {
	ids := make([]model.ID, len(packets))
	for i, packet := range packets {
		ids[i] = packet.CommandID
	}
	return write.Admit(context.Background(), f.p, write.AdmitRequest{CommandID: f.id(), PacketIDs: ids,
		Admitter: model.Actor{ID: "independent-reviewer"}, Outcome: "accepted", Reason: "verify exact receipt"})
}

func hbVerifyReceipt(t *testing.T, f *gateVerifyFixture, packet model.PacketRef) *model.AttemptTerminal {
	t.Helper()
	packets, err := store.ReadIntake(f.p, []model.ID{packet.CommandID})
	if err != nil || len(packets) != 1 || len(packets[0].Events) == 0 {
		t.Fatalf("returned receipt must already be readable in durable intake: %v", err)
	}
	event, err := model.DecodeEvent(packets[0].Events[0])
	if err != nil {
		t.Fatal(err)
	}
	terminal, ok := event.(*model.AttemptTerminal)
	if !ok {
		t.Fatalf("expected terminal receipt, got %T", event)
	}
	return terminal
}

type hbVerifyCapture func(*gateVerifyFixture, write.HandbackRequest) (model.PacketRef, error)

func hbVerifyAPI(f *gateVerifyFixture, r write.HandbackRequest) (model.PacketRef, error) {
	return write.Handback(context.Background(), f.p, r)
}

func hbVerifyCLI(t *testing.T) hbVerifyCapture {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "whosaidso")
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/whosaidso")
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOTOOLCHAIN=local")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Go CLI control build failed (no dependency downloads allowed): %v\n%s", err, out)
	}
	return func(f *gateVerifyFixture, r write.HandbackRequest) (model.PacketRef, error) {
		stWriteConfig(t, f.p.Root, string(f.p.ID), ".whosaidso/events")
		// Writes print a one-line acknowledgement by default; --json is the full result this test decodes.
		args := []string{"handback", "--json", "--command-id", string(r.CommandID), "--actor", r.Author.ID,
			"--attempt-id", string(r.AttemptID), "--outcome", string(r.Outcome), "--reason", r.Reason, "--next-action", r.NextAction}
		if r.CommitsDenied {
			args = append(args, "--commits-denied")
		}
		if r.ReconciliationOwed {
			args = append(args, "--reconciliation-owed")
		}
		for _, hold := range r.Holds {
			args = append(args, "--hold-id", string(hold.BlockerID), "--hold-reason", string(hold.Reason), "--hold-actor", hold.Actor.ID, "--hold-criterion", hold.Criterion)
		}
		cmd := exec.Command(binary, args...)
		cmd.Dir = f.p.Root
		bindProjectHome(cmd)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			return model.PacketRef{}, fmt.Errorf("%w: %s", err, &stderr)
		}
		var packet model.PacketRef
		err := json.Unmarshal(stdout.Bytes(), &packet)
		return packet, err
	}
}

func hbVerifyControl(t *testing.T, f *gateVerifyFixture, capture hbVerifyCapture) {
	t.Helper()
	ref, r := hbVerifyStart(t, f, model.Actor{ID: "holder"})
	packet, err := capture(f, r)
	if err != nil {
		t.Fatalf("control: honest stopped handback must capture: %v", err)
	}
	got := hbVerifyReceipt(t, f, packet)
	if got.Outcome != r.Outcome || got.Reason != r.Reason || got.NextAction != r.NextAction || got.CommitsDenied || got.ReconciliationOwed {
		t.Fatalf("control: exact Unicode/whitespace and unauthored false flags must survive capture: %+v", got)
	}
	if _, err := hbVerifyAdmit(f, packet); err != nil {
		t.Fatalf("control: holder's stopped receipt must admit: %v", err)
	}
	if p := hbVerifyTask(t, f, ref); p.Status != reduce.StatusReady || p.Closure != nil {
		t.Fatalf("control: stopped attempt must leave task READY and open: %+v", p)
	}
}

func TestHandbackVerifyAuthoredBytesAndActorCannotBeRewritten(t *testing.T) {
	cli := hbVerifyCLI(t)
	for _, route := range []struct {
		name    string
		capture hbVerifyCapture
	}{{"API", hbVerifyAPI}, {"CLI", cli}} {
		t.Run(route.name, func(t *testing.T) {
			f := gateVerifyNew(t)
			hbVerifyControl(t, f, route.capture)
			for _, field := range []string{"reason", "next-action", "author"} {
				t.Run(field, func(t *testing.T) {
					// U+FFFD is a legitimate known ID; an invalid byte is a
					// different input, and must not acquire that ID's authority.
					ref, r := hbVerifyStart(t, f, model.Actor{ID: "holder-\ufffd"})
					switch field {
					case "reason":
						r.Reason = "reason-\xff"
					case "next-action":
						r.NextAction = "action-\xff"
					case "author":
						r.Author.ID = "holder-\xff"
					}
					packet, err := route.capture(f, r)
					if err != nil {
						return // Refusing unrepresentable input is honest.
					}
					got := hbVerifyReceipt(t, f, packet)
					_, admissionErr := hbVerifyAdmit(f, packet)
					if field == "author" {
						if admissionErr == nil {
							p := hbVerifyTask(t, f, ref)
							t.Errorf("expected refusal of author %q handing back holder %q's attempt; got admitted terminal=%+v. JSON replacement turned a different actor's bytes into the holder's ID, bypassing handback ownership", r.Author.ID, "holder-\ufffd", p.Attempts[0].Terminal)
						}
					} else if got.Reason != r.Reason || got.NextAction != r.NextAction {
						t.Errorf("expected exact authored bytes or capture refusal for %s; got reason=%q next_action=%q (authored %q / %q), admission=%v. Handback silently replaced invalid UTF-8 and acknowledged a receipt the writer never authored", field, got.Reason, got.NextAction, r.Reason, r.NextAction, admissionErr)
					}
				})
			}
		})
	}
}

func TestHandbackVerifyNineOutcomesThroughAPIAndCLI(t *testing.T) {
	cli := hbVerifyCLI(t)
	for _, route := range []struct {
		name    string
		capture hbVerifyCapture
	}{{"API", hbVerifyAPI}, {"CLI", cli}} {
		t.Run(route.name, func(t *testing.T) {
			f := gateVerifyNew(t)
			hbVerifyControl(t, f, route.capture)
			for _, outcome := range []model.AttemptOutcome{model.AttemptSuccess, model.AttemptStopped, model.AttemptRefused, model.AttemptNoReading, model.AttemptMeasurementImpossible, model.AttemptRunnerDied, model.AttemptHarnessBroken, model.AttemptOutOfScope, model.AttemptBlockedMidTask} {
				t.Run(string(outcome), func(t *testing.T) {
					ref, r := hbVerifyStart(t, f, model.Actor{ID: "holder"})
					r.Outcome = outcome
					if outcome == model.AttemptOutOfScope || outcome == model.AttemptBlockedMidTask {
						r.Holds = []write.HandbackHold{{BlockerID: f.id(), Reason: model.BlockerResume, Actor: r.Author, Criterion: "owner assigns the unfinished work"}}
					}
					packet, err := route.capture(f, r)
					if err != nil {
						t.Fatal(err)
					}
					// CLI process has exited here, before any admission. A fresh
					// reader must still see the pending receipt and live attempt.
					got := hbVerifyReceipt(t, f, packet)
					if p := hbVerifyTask(t, f, ref); p.Attempts[0].Terminal != nil {
						t.Fatal("capture closed an attempt before admission")
					}
					if got.Outcome != outcome || got.Reason != r.Reason || got.NextAction != r.NextAction || got.CommitsDenied || got.ReconciliationOwed {
						t.Fatalf("authored receipt changed: %+v", got)
					}
					if _, err := hbVerifyAdmit(f, packet); err != nil {
						t.Fatal(err)
					}
					p := hbVerifyTask(t, f, ref)
					want := reduce.StatusReady
					if outcome == model.AttemptSuccess || len(r.Holds) != 0 {
						want = reduce.StatusBlocked
					}
					if p.Status != want || p.Closure != nil || p.Attempts[0].Terminal == nil || p.Attempts[0].Terminal.Outcome != outcome {
						t.Fatalf("expected %s with no task closure for %s, got %+v; terminal prose cannot fulfill the obligation", want, outcome, p)
					}
					if outcome == model.AttemptSuccess && (len(p.Reasons) != 1 || p.Reasons[0].Kind != reduce.ReasonAwaitingAcceptance) {
						t.Fatalf("success without witnesses escaped awaiting-acceptance: %+v", p.Reasons)
					}
					consumer := &model.TaskCreate{ID: f.id(), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}}, Spec: reduceSpec(1)}
					consumer.Spec.Prerequisites = []model.Prerequisite{{Kind: "task-success", Target: ref, WaiverPolicy: "forbid"}}
					if _, err := f.admit(r.Author, r.Author, consumer); err != nil {
						t.Fatal(err)
					}
					dep := hbVerifyTask(t, f, model.RecordRef{Project: f.p.ID, RecordID: consumer.ID, Revision: 1})
					if dep.Status != reduce.StatusBlocked || dep.Prerequisites[0].Satisfied() {
						t.Fatalf("%s receipt satisfied another task's ordinary success dependency: %+v", outcome, dep)
					}
				})
			}
			for _, badOutcome := range []model.AttemptOutcome{"", "SUCCESS", " success "} {
				_, r := hbVerifyStart(t, f, model.Actor{ID: "holder"})
				r.Outcome, r.Reason = badOutcome, "Everything is complete, VERIFIED and delivered"
				if _, err := route.capture(f, r); err == nil {
					t.Errorf("expected refusal of outcome %q, got capture; prose must not supply or normalize the outcome", badOutcome)
				}
			}
		})
	}
}

func TestHandbackVerifyTakeoverReceiptPacketOrder(t *testing.T) {
	f := gateVerifyNew(t)
	hbVerifyControl(t, f, hbVerifyAPI)
	for _, receiptFirst := range []bool{false, true} {
		ref, r := hbVerifyStart(t, f, model.Actor{ID: "holder"})
		body := []byte(`{"prior_writer":"stopped"}`)
		gateVerifyPut(t, filepath.Join(f.p.Root, "stopped.json"), body)
		next := &model.TaskTakeover{Task: ref, Actor: r.Author, AttemptID: f.id(), PriorAttemptID: r.AttemptID, StoppedConfirmationRef: gateVerifyContent(body, "stopped.json")}
		terminal := &model.AttemptTerminal{Task: ref, AttemptID: next.AttemptID, Outcome: model.AttemptBlockedMidTask, Reason: "new writer reached a boundary", NextAction: "owner resumes", DeliveryRefs: []model.ArtifactRef{}}
		hold := &model.BlockerHold{Task: ref, BlockerID: f.id(), Reason: model.BlockerResume, Actor: r.Author, Criterion: "owner supplies missing input"}
		// The receipt packet deliberately has the lower ULID in the attack;
		// IDs identify immutable packets, not attempt dependency order.
		var takeoverPacket, receiptPacket model.PacketRef
		if receiptFirst {
			receiptPacket = f.capture(r.Author, recEncode(t, terminal), recEncode(t, hold))
			takeoverPacket = f.capture(r.Author, recEncode(t, next))
		} else {
			takeoverPacket = f.capture(r.Author, recEncode(t, next))
			receiptPacket = f.capture(r.Author, recEncode(t, terminal), recEncode(t, hold))
		}
		before := hbVerifyTask(t, f, ref).Attempts[0]
		_, err := hbVerifyAdmit(f, receiptPacket, takeoverPacket)
		if err != nil {
			if !receiptFirst {
				t.Fatalf("control: bundled takeover, terminal and open hold must admit: %v", err)
			}
			t.Errorf("expected the same valid takeover/blocked-mid-task/open-hold bundle to admit regardless of packet ULID order; got %v. Gate ordering omits attempt dependencies and strands an honest receipt whose provider is in the same admission", err)
			continue
		}
		p := hbVerifyTask(t, f, ref)
		if len(p.Attempts) != 2 || !reflect.DeepEqual(p.Attempts[0], before) || len(p.Blockers) != 1 || !p.Blockers[0].Open() {
			t.Fatalf("expected untouched prior attempt and bundled open hold, got %+v", p)
		}
	}
}

func TestHandbackVerifyAtomicHoldsAcrossPacketsAndRetries(t *testing.T) {
	f := gateVerifyNew(t)
	hbVerifyControl(t, f, hbVerifyAPI)
	for _, outcome := range []model.AttemptOutcome{model.AttemptBlockedMidTask, model.AttemptOutOfScope} {
		for _, holdFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/hold-first=%v", outcome, holdFirst), func(t *testing.T) {
				ref, r := hbVerifyStart(t, f, model.Actor{ID: "holder"})
				// Takeover after a terminal attempt must meet READY (BLOCKED wins),
				// so the second attempt takes over the first while it is still live.
				body := []byte(`{"prior_writer":"stopped"}`)
				gateVerifyPut(t, filepath.Join(f.p.Root, "prior.json"), body)
				next := &model.TaskTakeover{Task: ref, Actor: r.Author, AttemptID: f.id(), PriorAttemptID: r.AttemptID, StoppedConfirmationRef: gateVerifyContent(body, "prior.json")}
				if _, err := f.admit(r.Author, r.Author, next); err != nil {
					t.Fatalf("control: takeover of a live attempt must admit without READY: %v", err)
				}
				r.Outcome = outcome
				// Neither denial of commits nor an explicit reconciliation debt
				// gives a receipt permission to omit its bundled hold.
				r.CommitsDenied, r.ReconciliationOwed = true, true
				hold := &model.BlockerHold{Task: ref, BlockerID: f.id(), Reason: model.BlockerResume, Actor: r.Author, Criterion: "owner assigns unfinished work"}
				var holdPacket model.PacketRef
				if holdFirst {
					holdPacket = f.capture(r.Author, recEncode(t, hold))
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				packet, err := write.Handback(ctx, f.p, r)
				if err != nil {
					t.Fatalf("cancelled work must still capture its final receipt: %v", err)
				}
				if !holdFirst {
					holdPacket = f.capture(r.Author, recEncode(t, hold))
				}
				before, err := store.ReadPrefix(f.p)
				if err != nil {
					t.Fatal(err)
				}
				for retry := 0; retry < 2; retry++ {
					if _, err := hbVerifyAdmit(f, packet); recCode(err) != "missing-hold" {
						t.Errorf("expected missing-hold despite a separate pending hold and authored flags; got %v, retry=%d", err, retry)
					}
				}
				after, err := store.ReadPrefix(f.p)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("holdless admission changed the ledger: %v", err)
				}
				if retry, err := hbVerifyAPI(f, r); err != nil || retry != packet {
					t.Fatalf("failed admission must retain the exact retryable receipt: %+v, %v", retry, err)
				}
				ids := []model.ID{packet.CommandID, holdPacket.CommandID}
				request := write.AdmitRequest{CommandID: f.id(), PacketIDs: ids, Admitter: r.Author, Outcome: "accepted", Reason: "atomic retry with the pending hold"}
				bundle, err := write.Admit(context.Background(), f.p, request)
				if err != nil {
					t.Fatalf("receipt plus open hold must admit in either packet order: %v", err)
				}
				again, err := write.Admit(context.Background(), f.p, request)
				// Freshly authored RawMessages and decoded ledger RawMessages
				// may differ in indentation; compare their canonical wire bytes.
				bundleBytes, firstErr := model.Encode(bundle)
				againBytes, retryErr := model.Encode(again)
				if err != nil || firstErr != nil || retryErr != nil || !bytes.Equal(bundleBytes, againBytes) {
					t.Fatalf("identical admission retry must return the same bundle: %v", err)
				}
				// The takeover's attempt is still live, so the task reads IN FLIGHT
				// here; BLOCKED is asserted once that attempt ends below.
				p := hbVerifyTask(t, f, ref)
				if p.Closure != nil || p.Status != reduce.StatusInFlight || len(p.Blockers) != 1 || !p.Blockers[0].Open() || p.Blockers[0].Held.Sequence != bundle.Sequence || !p.CommitsDenied || !p.Attempts[0].Terminal.ReconciliationOwed {
					t.Fatalf("receipt escaped its atomic hold or lost authored flags: %+v", p)
				}
				// An older, still-open hold cannot serve the next attempt's
				// terminal receipt merely because it is open on the same task.
				r.CommandID, r.AttemptID = f.id(), next.AttemptID
				second, err := hbVerifyAPI(f, r)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := hbVerifyAdmit(f, second); recCode(err) != "missing-hold" {
					t.Errorf("expected missing-hold for a second receipt borrowing a previous bundle's open hold; got %v", err)
				}
				// Put the clear in the lower-ID packet so ordering must follow
				// the blocker dependency; neither ordering may hide the clear.
				hold.BlockerID = f.id()
				clear := &model.BlockerClear{Task: ref, BlockerID: hold.BlockerID, HoldRef: model.BlockerRef{Task: ref, BlockerID: hold.BlockerID}, ResolvingWitness: gateVerifyContent(body, "prior.json")}
				clearPacket := f.capture(r.Author, recEncode(t, clear))
				freshHold := f.capture(r.Author, recEncode(t, hold))
				if _, err := hbVerifyAdmit(f, freshHold, second, clearPacket); recCode(err) != "missing-hold" {
					t.Errorf("expected missing-hold when its bundled hold is cleared in another packet; got %v", err)
				}
				// Control: with its own open hold and no clear, the second receipt
				// admits, so the refusals above were about the hold alone.
				if _, err := hbVerifyAdmit(f, second, freshHold); err != nil {
					t.Fatalf("control: second receipt plus its own open hold must admit: %v", err)
				}
				if p := hbVerifyTask(t, f, ref); p.Closure != nil || p.Status != reduce.StatusBlocked || len(p.Blockers) != 2 || !p.Blockers[0].Open() || !p.Blockers[1].Open() {
					t.Fatalf("both receipts' holds must stay open and block the task: %+v", p)
				}
			})
		}
	}
}

func TestHandbackVerifyTakeoverSelectedConfirmationAndPriorWriter(t *testing.T) {
	f := gateVerifyNew(t)
	hbVerifyControl(t, f, hbVerifyAPI)
	ref, r := hbVerifyStart(t, f, model.Actor{ID: "holder"})
	body := []byte(`{"stopped":"prior writer confirmed"}`)
	gateVerifyPut(t, filepath.Join(f.p.Root, "confirmation.json"), body)
	pin := gateVerifyContent(body, "confirmation.json")
	pin.Selector = model.Selector{Kind: "json-pointer", Pointer: "/stopped"}
	next := &model.TaskTakeover{Task: ref, Actor: model.Actor{ID: "second-writer"}, AttemptID: f.id(), PriorAttemptID: r.AttemptID, StoppedConfirmationRef: pin}
	before := hbVerifyTask(t, f, ref).Attempts[0]
	if _, err := f.admit(next.Actor, next.Actor, next); err != nil {
		t.Fatalf("control: selected, available confirmation must allow takeover: %v", err)
	}
	p := hbVerifyTask(t, f, ref)
	if len(p.Attempts) != 2 || !reflect.DeepEqual(p.Attempts[0], before) || p.Attempts[1].Terminal != nil {
		t.Fatalf("takeover erased or terminated the prior writer: %+v", p)
	}
	for _, attack := range []string{"missing-selector", "wrong-author", "unknown-author"} {
		t.Run(attack, func(t *testing.T) {
			if attack == "missing-selector" {
				bad := *next
				bad.AttemptID = f.id()
				bad.StoppedConfirmationRef.Selector.Pointer = "/never-confirmed"
				if _, err := f.admit(bad.Actor, bad.Actor, &bad); recCode(err) != "unavailable" {
					t.Errorf("expected refusal when confirmation bytes resolve but the selected confirmation does not; got %v", err)
				}
			} else {
				bad := r
				bad.CommandID, bad.Author = f.id(), next.Actor
				if attack == "unknown-author" {
					bad.Author = model.Actor{UnknownReason: "writer not recorded"}
				}
				packet, err := hbVerifyAPI(f, bad)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := hbVerifyAdmit(f, packet); recCode(err) != "attribution-mismatch" {
					t.Errorf("expected refusal of %+v ending the prior holder's attempt, got %v; takeover grants no authority over the previous writer's receipt", bad.Author, err)
				}
			}
			if got := hbVerifyTask(t, f, ref); !reflect.DeepEqual(got, p) {
				t.Fatalf("refused operation changed the two writers: %+v", got)
			}
		})
	}
}
