package acceptance_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/store"
	"datum/internal/write"
)

// Exercise the entrances around the handback UTF-8 repair, not just the
// encoder in isolation. A real U+FFFD is legal; an invalid byte is another ID.
func TestWriteVerifyUTF8CannotRewriteLedgerAuthorship(t *testing.T) {
	f := gateVerifyNew(t)
	hbVerifyControl(t, f, hbVerifyAPI)
	t.Run("capture-author", func(t *testing.T) {
		ref, r := hbVerifyStart(t, f, model.Actor{ID: "holder-\ufffd"})
		terminal := recEncode(t, &model.AttemptTerminal{Task: ref, AttemptID: r.AttemptID,
			Outcome: r.Outcome, Reason: r.Reason, NextAction: r.NextAction, DeliveryRefs: []model.ArtifactRef{}})
		packet, err := store.WriteIntake(context.Background(), f.p, store.IntakeRequest{
			CommandID: f.id(), Author: model.Actor{ID: "holder-\xff"}, Events: []model.Event{terminal}})
		if err != nil {
			return
		}
		if _, err := hbVerifyAdmit(f, packet); err == nil {
			p := hbVerifyTask(t, f, ref)
			if p.Attempts[0].Terminal == nil {
				t.Fatal("successful admission must be visible to a fresh reader")
			}
			t.Errorf("expected refusal of capture author %q ending %q's attempt; got a durably admitted terminal. Capture replaces invalid UTF-8 before the handback ownership check, letting different actor bytes acquire the holder's authority", "holder-\xff", r.Author.ID)
		}
	})
	for _, field := range []string{"admitter", "review-reason"} {
		t.Run(field, func(t *testing.T) {
			author := model.Actor{ID: "reviewer-\ufffd"}
			packet := f.capture(author, recEncode(t, f.claim(author)))
			r := write.AdmitRequest{CommandID: f.id(), PacketIDs: []model.ID{packet.CommandID},
				Admitter: author, Outcome: "accepted", Reason: "reviewed exact source"}
			if field == "admitter" {
				r.Admitter.ID = "reviewer-\xff"
			} else {
				r.Reason = "review-\xff"
			}
			if _, err := write.Admit(context.Background(), f.p, r); err != nil {
				return
			}
			prefix, err := store.ReadPrefix(f.p)
			if err != nil {
				t.Fatal(err)
			}
			got := prefix[len(prefix)-1]
			review := recMustDecodeReview(t, got.Events[len(got.Events)-1])
			if field == "admitter" {
				t.Errorf("expected refusal of unrepresentable admitter %q; fresh ledger contains %q and self_admission=%q for author %q. Admission rewrites actor identity after computing attribution, so even its self-admission audit contradicts its stored actor", r.Admitter.ID, got.Admitter.ID, review.SelfAdmission[packet.CommandID], author.ID)
			} else {
				t.Errorf("expected refusal of unrepresentable authored review reason %q; fresh ledger contains %q. Checking UTF-8 after JSON encoding acknowledges different review text than was authored", r.Reason, review.Reason)
			}
		})
	}
	t.Run("handback-hold", func(t *testing.T) {
		ref, r := hbVerifyStart(t, f, model.Actor{ID: "holder"})
		r.Outcome = model.AttemptBlockedMidTask
		r.Holds = []write.HandbackHold{{BlockerID: f.id(), Reason: model.BlockerResume,
			Actor: model.Actor{ID: "assignee-\xff"}, Criterion: "resolve-\xff"}}
		packet, err := write.Handback(context.Background(), f.p, r)
		if err != nil {
			return
		}
		if _, err := hbVerifyAdmit(f, packet); err != nil {
			return
		}
		p := hbVerifyTask(t, f, ref)
		t.Errorf("expected refusal of invalid UTF-8 in bundled hold actor %q and criterion %q; got admitted blockers %+v. Handback's new guard checks the receipt's author and prose but skips its authored hold assignment", r.Holds[0].Actor.ID, r.Holds[0].Criterion, p.Blockers)
	})
}

func TestWriteVerifyProducerRejectsInvalidUTF8BeforeDecodingFacts(t *testing.T) {
	p := outsideWriteProject(t)
	outsideRunControl(t, p)
	// Unlike argv, this string becomes the bytes of producer.json. Keep the
	// raw report artifact and process result; refuse only its purported facts.
	report := strings.Replace(outsideRunReport, `"type":"number","number":8`, "\"type\":\"string\",\"string\":\"adapter-\xff\"", 1)
	// Transport only: the malformed bytes travel hex-encoded because argv is
	// authored intent and now refuses invalid UTF-8. The child writes the
	// decoded ORIGINAL bytes to producer.json, and every assertion below is
	// unchanged. Reconciled in the open by the coordinator, 2026-09-22.
	result, err := write.Run(context.Background(), p, outsideRunRequest(p, "hex:"+hex.EncodeToString([]byte(report)), ""))
	_, seal := outsideRunPackets(t, p, result)
	raw, readErr := os.ReadFile(filepath.Join(result.ArtifactDir, "producer.json"))
	if readErr != nil || !bytes.Equal(raw, []byte(report)) {
		t.Fatalf("fixture must deliver the original malformed report bytes to the runner: got %q, error=%v", raw, readErr)
	}
	if err == nil || seal.ConfigEffective.State != model.Unknown {
		var observed *model.Scalar
		if seal.ConfigEffective.Value != nil {
			observed = (*seal.ConfigEffective.Value)["sample_count"].Value
		}
		got := "<unknown>"
		if observed != nil && observed.String != nil {
			got = *observed.String
		}
		t.Errorf("expected invalid-report refusal and UNKNOWN configuration for raw producer text %q; got error=%v, state=%s, stored value=%q. Report decoding repairs malformed UTF-8 into a KNOWN observation before the strict packet decoder sees it", "adapter-\xff", err, seal.ConfigEffective.State, got)
	}
}

// The existing post-copy context barrier orders mutations without a data race:
// Run has validated and copied intent, but has not published or launched yet.
func writeVerifyAfterCopy(t *testing.T, p store.Project, r write.RunRequest, mutate func()) (write.RunResult, error) {
	t.Helper()
	ctx := &outsideCopiedIntentContext{Context: context.Background(), copied: make(chan struct{}), resume: make(chan struct{})}
	resume := sync.OnceFunc(func() { close(ctx.resume) })
	defer resume()
	type answer struct {
		result write.RunResult
		err    error
	}
	done := make(chan answer, 1)
	go func() {
		result, err := write.Run(ctx, p, r)
		done <- answer{result, err}
	}()
	select {
	case <-ctx.copied:
	case a := <-done:
		t.Fatalf("control request must reach the post-copy barrier: %v", a.err)
	case <-time.After(8 * time.Second):
		t.Fatal("control request did not reach the post-copy barrier")
	}
	mutate()
	resume()
	select {
	case a := <-done:
		return a.result, a.err
	case <-time.After(15 * time.Second):
		t.Fatal("producer did not finish after releasing the post-copy barrier")
		return write.RunResult{}, context.DeadlineExceeded
	}
}

func TestWriteVerifyInstrumentConfigSurfaceIsFrozenForSeal(t *testing.T) {
	p := outsideWriteProject(t)
	outsideRunControl(t, p)
	r := outsideRunRequest(p, outsideRunReport, "")
	result, err := writeVerifyAfterCopy(t, p, r, func() { r.Instrument.ConfigSurface[0] = "reused-storage" })
	_, seal := outsideRunPackets(t, p, result)
	if err != nil || seal.ConfigEffective.State != model.Known {
		t.Errorf("expected the control's sample_count=8 report to seal against the original instrument declaration after caller storage reuse; got error=%v, configuration=%+v. Intent is frozen, but runSeal rereads the caller-owned ConfigSurface slice and invalidates an otherwise valid observation", err, seal.ConfigEffective)
	}
}

func TestWriteVerifyNestedInvocationIntentCopiesAreOwned(t *testing.T) {
	p := outsideWriteProject(t)
	outsideRunControl(t, p)
	r := outsideRunRequest(p, outsideRunReport, "")
	_, env, _ := outsideProofFixture()
	r.CriterionRef = env.CriterionRef
	r.InputRefs = []model.ArtifactRef{gateVerifyContent([]byte("input"), "input.json")}
	r.InputRefs[0].Git = &model.GitPin{ObjectFormat: "sha1", Commit: strings.Repeat("a", 40), Path: "input.json"}
	r.ExecutionSourceIdentity.SourceRefs = []model.ArtifactRef{gateVerifyContent([]byte("source"), "source.json")}
	r.ExecutionSourceIdentity.MachineID = laneEEvidenceKnown(laneEReduceID(90))
	r.ExecutionSourceIdentity.Head = laneEEvidenceKnown(model.GitHead{ObjectFormat: "sha1", Commit: strings.Repeat("b", 40)})
	r.ExecutionSourceIdentity.Dirty = laneEEvidenceKnown(true)
	text, flag := "original", true
	r.ConditionsDeclared["label"] = model.Scalar{Type: "string", String: &text}
	r.ConditionsDeclared["flag"] = model.Scalar{Type: "bool", Bool: &flag}
	// An independent wire copy records all original nested values for comparison.
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var want write.RunRequest
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	control, err := write.Run(context.Background(), p, r)
	if err != nil {
		t.Fatalf("control: fully populated nested intent must seal: %v", err)
	}
	outsideRunPackets(t, p, control)
	result, err := writeVerifyAfterCopy(t, p, r, func() {
		r.Argv[0] = "caller-reused-executable"
		*r.ConfigRequested["sample_count"].Number = "999"
		*r.ConditionsDeclared["seed"].Number = "777"
		text, flag = "reused", false
		r.CriterionRef.Value.Revision++
		*r.ExecutionSourceIdentity.MachineID.Value = laneEReduceID(91)
		r.ExecutionSourceIdentity.Head.Value.Commit = strings.Repeat("c", 40)
		*r.ExecutionSourceIdentity.Dirty.Value = false
		r.InputRefs[0].Git.Path = "reused.json"
		r.InputRefs[0].Content.Length++
		r.InputRefs[0].Content.Locators[0].Path = "reused.json"
		r.InputRefs[0].Selector = model.Selector{Kind: "json-pointer", Pointer: "/reused"}
		r.ExecutionSourceIdentity.SourceRefs[0].Content.Locators[0].Path = "reused-source.json"
	})
	if err != nil {
		t.Fatalf("expected nested caller reuse not to affect execution or sealing: %v", err)
	}
	start, seal := outsideRunPackets(t, p, result)
	for _, e := range []model.InvocationEnvelope{start, seal} {
		gotFields := []any{e.Argv, e.InputRefs, e.ConfigRequested, e.ConditionsDeclared, e.CriterionRef, e.ExecutionSourceIdentity}
		wantFields := []any{want.Argv, want.InputRefs, want.ConfigRequested, want.ConditionsDeclared, want.CriterionRef, want.ExecutionSourceIdentity}
		if !reflect.DeepEqual(gotFields, wantFields) {
			t.Errorf("expected original argv, artifact slices/pins/locators, scalar pointers, criterion and execution identity in both durable packets; got %+v, want %+v. Caller reuse must not rewrite captured intent", gotFields, wantFields)
		}
	}
}

func writeVerifyCLI(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "datum")
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/datum")
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOTOOLCHAIN=local")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("control: Go CLI must build without dependency downloads: %v\n%s", err, out)
	}
	return binary
}

func writeVerifyCLIPacket(binary string, f *gateVerifyFixture, input string, args ...string) (model.PacketRef, error) {
	cmd := exec.Command(binary, args...)
	cmd.Dir, cmd.Stdin = f.p.Root, strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return model.PacketRef{}, fmt.Errorf("%w: %s", err, &stderr)
	}
	var ref model.PacketRef
	err := json.Unmarshal(stdout.Bytes(), &ref)
	return ref, err
}

func TestWriteVerifyCLIJSONAliasesCannotChooseAuthoredMeaning(t *testing.T) {
	f := gateVerifyNew(t)
	f.control()
	stWriteConfig(t, f.p.Root, string(f.p.ID), ".datum/events")
	binary := writeVerifyCLI(t)
	author := model.Actor{ID: "reviewer"}
	t.Run("capture-event-type", func(t *testing.T) {
		capture := func(keys string) (model.PacketRef, model.ID, error) {
			claim := f.claim(author)
			raw := recEncode(t, claim)
			input := "[{" + keys + ",\"data\":" + string(raw.Data) + "}]"
			// R19: writes print a one-line acknowledgement by default; --json is the full result this test decodes.
			packet, err := writeVerifyCLIPacket(binary, f, input, "capture", "--json", "--actor", author.ID, "--events", "-")
			return packet, claim.ID, err
		}
		packet, id, err := capture(`"type":"claim.assert"`)
		if err != nil {
			t.Fatalf("control: unambiguous claim JSON must capture: %v", err)
		}
		if _, err := hbVerifyAdmit(f, packet); err != nil {
			t.Fatalf("control: captured claim must admit: %v", err)
		}
		f.unmeasured(id)
		for _, keys := range []string{`"type":"claim.revise","TYPE":"claim.assert"`, `"TYPE":"claim.revise","type":"claim.assert"`} {
			packet, id, err := capture(keys)
			if err != nil {
				continue
			}
			if _, err := hbVerifyAdmit(f, packet); err == nil {
				f.unmeasured(id)
				t.Errorf("expected refusal of conflicting event tags {%s}; CLI captured and admitted claim %s. Case-insensitive decoding discards an authored tag before the gate's exact-key check, so packet hashes bind only the decoder's chosen meaning", keys, id)
			}
		}
	})
	t.Run("handback-delivery-selector", func(t *testing.T) {
		body := []byte(`{"approved":"deliver this","private":"do not deliver this"}`)
		gateVerifyPut(t, filepath.Join(f.p.Root, "delivery.json"), body)
		pin := gateVerifyContent(body, "delivery.json")
		pin.Selector = model.Selector{Kind: "json-pointer", Pointer: "/approved"}
		data, err := json.Marshal([]model.ArtifactRef{pin})
		if err != nil {
			t.Fatal(err)
		}
		capture := func(input string) (model.PacketRef, error) {
			_, r := hbVerifyStart(t, f, author)
			// R19: writes print a one-line acknowledgement by default; --json is the full result this test decodes.
			return writeVerifyCLIPacket(binary, f, input, "handback", "--json", "--actor", author.ID, "--attempt-id", string(r.AttemptID),
				"--outcome", "stopped", "--reason", "stopped", "--next-action", "owner reviews", "--delivery-refs", "-")
		}
		packet, err := capture(string(data))
		if err != nil {
			t.Fatalf("control: unambiguous delivery selector must capture: %v", err)
		}
		if _, err := hbVerifyAdmit(f, packet); err != nil {
			t.Fatalf("control: selected delivery must admit: %v", err)
		}
		for _, keys := range []string{`"pointer":"/approved","POINTER":"/private"`, `"POINTER":"/approved","pointer":"/private"`} {
			input := strings.Replace(string(data), `"pointer":"/approved"`, keys, 1)
			packet, err := capture(input)
			if err != nil {
				continue
			}
			if _, err := hbVerifyAdmit(f, packet); err == nil {
				got := hbVerifyReceipt(t, f, packet)
				t.Errorf("expected refusal of contradictory delivery selectors {%s}; CLI admitted selector %q. Duplicate-key hardening skips case aliases in delivery JSON, so admission verifies a different selection after the ambiguity has been erased", keys, got.DeliveryRefs[0].Selector.Pointer)
			}
		}
	})
}
