package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

func cliFixture(t *testing.T) (string, []byte) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "datum.toml"), []byte("id = \"test/cli\"\nledger = \"record/events\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	task, err := model.EncodeEvent(&model.TaskCreate{
		ID: cliID(1), Provenance: model.Provenance{Author: model.Actor{ID: "lane"}, SourceRefs: []model.ArtifactRef{}},
		Spec: model.TaskSpec{
			Intent: "exercise the CLI", Subject: "an intake packet",
			Scope:    model.Scope{SourcePaths: []string{}, ContextRefs: []model.RecordRef{}, AppliesWhen: "this fixture", Limitations: "not a real ledger"},
			NonGoals: []string{"write production state"}, AcceptanceCriteria: []model.AcceptanceCriterion{{ID: cliID(2), Revision: 1, Criterion: "capture then admit then replay"}},
			ContextRefs: []model.RecordRef{}, ConstraintRefs: []model.RecordRef{}, Prerequisites: []model.Prerequisite{}, NextActor: model.Actor{ID: "lane"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := model.Encode([]model.Event{task})
	if err != nil {
		t.Fatal(err)
	}
	return root, data
}

func cliID(n int) model.ID { return model.ID(fmt.Sprintf("%026d", n)) }

func callWriteCLI(t *testing.T, root string, input []byte, args ...string) ([]byte, error) {
	t.Helper()
	var output, diagnostic bytes.Buffer
	err := writeCLI(context.Background(), args, root, bytes.NewReader(input), &output, &diagnostic, func(string) string { return "" })
	return output.Bytes(), err
}

func cliControl(t *testing.T, root string, data []byte) {
	t.Helper()
	if _, err := callWriteCLI(t, root, data, "capture", "--command-id", string(cliID(3)), "--actor", "lane"); err != nil {
		t.Fatal(err)
	}
	project, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := store.ReadPrefix(project)
	if err != nil || len(prefix) != 0 {
		t.Fatalf("capture bypassed admission: %v, %v", prefix, err)
	}
	if _, err := callWriteCLI(t, root, nil, "admit", "--command-id", string(cliID(4)), "--actor", "reviewer", "--outcome", "accepted", "--reason", "checked the task", string(cliID(3))); err != nil {
		t.Fatal(err)
	}
	prefix, err = store.ReadPrefix(project)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reduce.Replay(prefix)
	if err != nil || len(snapshot.Records()) != 1 {
		t.Fatalf("good CLI control did not replay: %+v, %v", snapshot.Records(), err)
	}
}

func TestCLICaptureAdmissionAndExplicitUnavailable(t *testing.T) {
	root, data := cliFixture(t)
	cliControl(t, root, data)
	// run is enabled by U12; TestCLIRunRefusesBeforeLaunch covers its refusals.
	for _, command := range []string{"publish", "append", "decision", "claim"} {
		_, err := callWriteCLI(t, root, nil, command)
		if err == nil || !strings.Contains(err.Error(), "unavailable-until-integrated") {
			t.Fatalf("unchecked command %s was not refused: %v", command, err)
		}
	}
	_, err := callWriteCLI(t, root, nil, "admit", "--command-id", string(cliID(4)), "--actor", "reviewer", "--outcome", "rejected", "--reason", "changed judgment", string(cliID(3)))
	if err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("CLI mixed-content retry was not refused: %v", err)
	}
}

func TestCLICaptureRejectsMalformedEvents(t *testing.T) {
	root, data := cliFixture(t)
	cliControl(t, root, data)
	for _, input := range []string{
		`[{"type":"task.start","type":"task.create","data":{}}]`,
		`[{"type":"task.create","data":{},"extra":true}]`,
		`[{"type":"task.create","data":{}}] {}`,
		`null`,
		`[{"type":"task.create","TYPE":"task.start","data":{}}]`,
		`[{"TYPE":"task.start","type":"task.create","data":{}}]`,
		`[{"type":"task.create","Data":{}}]`,
		"[{\"type\":\"task.create\",\"data\":{},\"x-\xff\":1}]",
		// A valid event whose last-wins alias would otherwise decode cleanly.
		strings.Replace(string(data), `"type": "task.create"`, `"TYPE": "task.start", "type": "task.create"`, 1),
	} {
		if _, err := callWriteCLI(t, root, []byte(input), "capture", "--actor", "lane"); err == nil {
			t.Fatalf("malformed intake was acknowledged: %s", input)
		}
	}
}

func TestCLIActorFallbackUnknownAndBlobCapture(t *testing.T) {
	root, _ := cliFixture(t)
	body := []byte("speaker identity is separate from the capturing actor")
	blob := filepath.Join(root, "source.txt")
	if err := os.WriteFile(blob, body, 0600); err != nil {
		t.Fatal(err)
	}
	source, err := model.EncodeEvent(&model.SourceIntake{
		SourceID: cliID(20), OriginalDigest: model.HashBytes(body), Length: uint64(len(body)), Speaker: model.Actor{ID: "owner"}, Referents: []model.RecordRef{},
		SourceRef: model.ArtifactRef{Kind: "content", Content: &model.ContentPin{SHA256: model.HashBytes(body), Length: uint64(len(body)), MediaType: "text/plain", Locators: []model.Locator{}}, Selector: model.Selector{Kind: "whole"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := model.Encode([]model.Event{source})
	if err != nil {
		t.Fatal(err)
	}
	for i, expected := range []model.Actor{{ID: "from-environment"}, {UnknownReason: "no actor supplied by --actor or DATUM_ACTOR"}} {
		var out bytes.Buffer
		env := func(string) string { return "from-environment" }
		args := []string{"capture", "--command-id", string(cliID(21 + i)), "--blob", blob}
		if i == 1 {
			args = append(args, "--actor", "")
		}
		if err := writeCLI(context.Background(), args, root, bytes.NewReader(data), &out, io.Discard, env); err != nil {
			t.Fatal(err)
		}
		var packet model.PacketRef
		if err := json.Unmarshal(out.Bytes(), &packet); err != nil {
			t.Fatal(err)
		}
		project, err := store.Discover(root)
		if err != nil {
			t.Fatal(err)
		}
		packets, err := store.ReadIntake(project, []model.ID{packet.CommandID})
		if err != nil || packets[0].Author != expected {
			t.Fatalf("actor was guessed or lost: %+v, %v", packets, err)
		}
	}
}

func cliHandbackControl(t *testing.T) (string, store.Project, []string) {
	t.Helper()
	root, data := cliFixture(t)
	cliControl(t, root, data)
	p, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	event, err := model.EncodeEvent(&model.TaskStart{Task: model.RecordRef{Project: p.ID, RecordID: cliID(1), Revision: 1}, Actor: model.Actor{ID: "holder-é-持有者-🦊-�"}, AttemptID: cliID(7)})
	if err != nil {
		t.Fatal(err)
	}
	data, err = model.Encode([]model.Event{event})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := callWriteCLI(t, root, data, "capture", "--command-id", string(cliID(5)), "--actor", "holder-é-持有者-🦊-�"); err != nil {
		t.Fatal(err)
	}
	if _, err := callWriteCLI(t, root, nil, "admit", "--command-id", string(cliID(6)), "--actor", "reviewer", "--outcome", "accepted", "--reason", "start checked", string(cliID(5))); err != nil {
		t.Fatal(err)
	}
	return root, p, []string{"handback", "--command-id", string(cliID(8)), "--attempt-id", string(cliID(7)), "--outcome", "stopped", "--reason", "  exact reason é / e\u0301 / 理由 / 🦊 / �\r\n", "--next-action", "  exact next action é / 次 / 🦊\n"}
}

func TestCLIHandbackOutcomesAndAttribution(t *testing.T) {
	for _, name := range []string{"success", "stopped", "refused", "no-reading", "measurement-impossible", "runner-died", "harness-broken", "out-of-scope", "blocked-mid-task", "unknown", "explicit-empty"} {
		t.Run(name, func(t *testing.T) {
			root, p, args := cliHandbackControl(t)
			outcome, author, env := name, model.Actor{ID: "holder-é-持有者-🦊-�"}, "holder-é-持有者-🦊-�"
			if name == "unknown" || name == "explicit-empty" {
				outcome, author = "stopped", model.Actor{UnknownReason: "no actor supplied by --actor or DATUM_ACTOR"}
				if name == "unknown" {
					env = ""
				} else {
					args = append(args, "--actor", "")
				}
			} else if name == "success" {
				env, args = "wrong-environment-actor", append(args, "--actor", "holder-é-持有者-🦊-�")
			}
			args[6] = outcome
			withHold := outcome == "blocked-mid-task" || outcome == "out-of-scope"
			if withHold {
				args = append(args, "--hold-id", string(cliID(9)), "--hold-reason", "resume", "--hold-actor", "owner", "--hold-criterion", "  owner reassigns\n")
			}
			body := []byte("delivery bytes")
			pin := model.ArtifactRef{Kind: "content", Content: &model.ContentPin{SHA256: model.HashBytes(body), Length: uint64(len(body)), MediaType: "text/plain", Locators: []model.Locator{{Path: "delivery.txt"}}}, Selector: model.Selector{Kind: "whole"}}
			refs, _ := model.Encode([]model.ArtifactRef{pin})
			if err := os.WriteFile(filepath.Join(root, "delivery.txt"), body, 0600); err != nil {
				t.Fatal(err)
			}
			args = append(args, "--delivery-refs", "-", "--commits-denied", "--reconciliation-owed")
			if name == "success" {
				path := filepath.Join(root, "refs.json")
				if err := os.WriteFile(path, refs, 0600); err != nil {
					t.Fatal(err)
				}
				args[len(args)-3] = path
			}
			var out, diagnostic bytes.Buffer
			if err := writeCLI(context.Background(), args, root, bytes.NewReader(refs), &out, &diagnostic, func(string) string { return env }); err != nil || diagnostic.Len() != 0 {
				t.Fatalf("handback failed: %v; stderr=%s", err, &diagnostic)
			}
			var receipt model.PacketRef
			if err := json.Unmarshal(out.Bytes(), &receipt); err != nil || receipt.CommandID != cliID(8) || receipt.Digest == "" {
				t.Fatalf("expected packet JSON: %s, %v", &out, err)
			}
			var retry bytes.Buffer
			if err := writeCLI(context.Background(), args, root, bytes.NewReader(refs), &retry, &diagnostic, func(string) string { return env }); err != nil || !bytes.Equal(out.Bytes(), retry.Bytes()) {
				t.Fatalf("identical handback retry changed receipt: %s, %v", &retry, err)
			}
			packets, err := store.ReadIntake(p, []model.ID{receipt.CommandID})
			if err != nil || len(packets) != 1 || packets[0].Author != author {
				t.Fatalf("authorship lost: %+v, %v", packets, err)
			}
			terminal, err := model.DecodeEvent(packets[0].Events[0])
			want := &model.AttemptTerminal{Task: model.RecordRef{Project: p.ID, RecordID: cliID(1), Revision: 1}, AttemptID: cliID(7), Outcome: model.AttemptOutcome(outcome), Reason: args[8], NextAction: args[10], DeliveryRefs: []model.ArtifactRef{pin}, CommitsDenied: true, ReconciliationOwed: true}
			if err != nil || !reflect.DeepEqual(terminal, want) {
				t.Fatalf("authored receipt changed: %+v, %v", terminal, err)
			}
			if withHold {
				if len(packets[0].Events) != 2 {
					t.Fatal("hold was not captured with receipt")
				}
				hold, err := model.DecodeEvent(packets[0].Events[1])
				if err != nil || !reflect.DeepEqual(hold, &model.BlockerHold{Task: want.Task, BlockerID: cliID(9), Reason: model.BlockerResume, Actor: model.Actor{ID: "owner"}, Criterion: "  owner reassigns\n"}) {
					t.Fatalf("authored hold changed: %+v, %v", hold, err)
				}
			} else if len(packets[0].Events) != 1 {
				t.Fatal("handback invented events")
			}
			prefix, err := store.ReadPrefix(p)
			if err != nil || len(prefix) != 2 {
				t.Fatalf("capture published a bundle: %v", err)
			}
			if author.ID != "" {
				if _, err := callWriteCLI(t, root, nil, "admit", "--command-id", string(cliID(10)), "--actor", "reviewer", "--outcome", "accepted", "--reason", "receipt checked", string(receipt.CommandID)); err != nil {
					t.Fatalf("CLI outcome cannot be admitted: %v", err)
				}
			}
		})
	}
}

func TestCLIHandbackRefusesMissingMeaning(t *testing.T) {
	root, p, control := cliHandbackControl(t)
	for flag, i := range map[string]int{"--outcome": 5, "--reason": 7, "--next-action": 9, "--attempt-id": 3, "--actor": 11} {
		for _, value := range []string{"omitted", "", "\u200b", " ", "invalid-\xff", "truncated-\xe2\x82"} {
			t.Run(flag+"/"+value, func(t *testing.T) {
				if flag == "--actor" && !strings.Contains(value, "-") {
					return
				}
				args := append(append([]string(nil), control...), "--actor", "lane")
				args[i+1] = value
				if value == "omitted" {
					args = append(args[:i], args[i+2:]...)
				}
				out, err := callWriteCLI(t, root, nil, args...)
				if err == nil || len(out) != 0 {
					t.Fatalf("missing authored %s was acknowledged: %s, %v", flag, out, err)
				}
				field := map[string]string{"--reason": "reason", "--next-action": "next_action", "--actor": "author.id"}[flag]
				if field != "" && strings.Contains(value, "-") && !strings.Contains(err.Error(), field+": input contains invalid UTF-8") {
					t.Fatalf("missing field/UTF-8 diagnostic: %v", err)
				}
				packets, err := store.ReadIntake(p, nil)
				if err != nil || len(packets) != 2 {
					t.Fatalf("invalid handback persisted: %+v, %v", packets, err)
				}
			})
		}
	}
}

func TestCLIHandbackRefusesInvalidFlags(t *testing.T) {
	root, _, control := cliHandbackControl(t)
	for _, extra := range [][]string{{"--outcome", "invented"}, {"--outcome", " success "}, {"--hold-id", string(cliID(9))}, {"--delivery-refs", "-"}, {"--typo"}, {"unexpected-positional"}} {
		args := append(append([]string(nil), control...), extra...)
		out, err := callWriteCLI(t, root, []byte(`[] {}`), args...)
		if err == nil || len(out) != 0 {
			t.Fatalf("invalid flags acknowledged: %v, %s, %v", extra, out, err)
		}
	}
	if out, err := callWriteCLI(t, root, nil, control...); err != nil || !json.Valid(out) {
		t.Fatalf("minimal authored handback failed: %s, %v", out, err)
	}
	// Case aliases must be refused, not resolved by encoding/json's choice.
	pin := `[{"kind":"content","content":{"sha256":"` + string(model.HashBytes([]byte("x"))) + `","length":1,"media_type":"application/json","locators":[{"path":"d.json"}]},"selector":{"kind":"json-pointer","pointer":"/a"}}]`
	for i, input := range []string{pin, strings.Replace(pin, `"pointer":"/a"`, `"pointer":"/a","POINTER":"/b"`, 1), strings.Replace(pin, `"pointer":"/a"`, `"POINTER":"/b","pointer":"/a"`, 1), strings.Replace(pin, `"kind":"content"`, `"Kind":"content"`, 1)} {
		args := append(append([]string(nil), control...), "--delivery-refs", "-", "--command-id", string(cliID(20+i)))
		if out, err := callWriteCLI(t, root, []byte(input), args...); (err == nil) != (i == 0) {
			t.Fatalf("delivery refs %s: out=%s err=%v", input, out, err)
		}
	}
}

// ---- U12: run and proof through fresh processes ----------------------------

const (
	e2ePass   = `{"results":{"unit":"mm","population":"pose sweep","denominator":"poses","values":[0.0100,0.0200]},"population":{"population":"pose sweep","denominator":"poses","values":["pose-a","pose-b"]}}`
	e2eScript = "mkdir \"$DATUM_RUN_DIR/out\"\nprintf '%s' '" + e2ePass + "' > \"$DATUM_RUN_DIR/out/result.json\"\nprintf '%s' '{\"version\":1,\"config_effective\":{},\"conditions_observed\":{},\"outputs\":[{\"path\":\"out/result.json\",\"media_type\":\"application/json\"}]}' > \"$DATUM_RUN_REPORT\"\n"
)

func e2ePin(body, path, media string) model.ArtifactRef {
	return model.ArtifactRef{Kind: "content", Content: &model.ContentPin{SHA256: model.HashBytes([]byte(body)), Length: uint64(len(body)), MediaType: media, Locators: []model.Locator{{Path: path}}}, Selector: model.Selector{Kind: "whole"}}
}

func e2eKnown[T any](v T) model.Availability[T] {
	return model.Availability[T]{State: model.Known, Value: &v}
}

// e2eInvoke runs one datum command in a fresh process and returns its stdout.
func e2eInvoke(t *testing.T, root string, events []model.TypedEvent, args ...string) ([]byte, error) {
	t.Helper()
	var input []byte
	if events != nil {
		raw := make([]model.Event, len(events))
		for i, event := range events {
			encoded, err := model.EncodeEvent(event)
			if err != nil {
				t.Fatal(err)
			}
			raw[i] = encoded
		}
		var err error
		if input, err = model.Encode(raw); err != nil {
			t.Fatal(err)
		}
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, append([]string{"-test.run=^TestDatumMainProcess$", "--"}, args...)...)
	command.Dir, command.Stdin = root, bytes.NewReader(input)
	command.Env = append(os.Environ(), "DATUM_MAIN_TEST_PROCESS=1", "DATUM_ACTOR=lane")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("%v: %s", err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// e2eWorld admits a task, claim and KNOWN-validated instrument, then a frozen
// criterion, then an attempt, each through fresh capture and admit processes.
func e2eWorld(t *testing.T) (string, model.CriterionRef, model.RecordRef, model.ID) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fixture producer is a POSIX shell script")
	}
	root, _ := cliFixture(t)
	for path, body := range map[string]string{"tools/measure.sh": e2eScript, "validation/measure.json": `{"validated":"against a known pose sweep"}`, "out/result.json": e2ePass} {
		proofWrite(t, root, path, body)
	}
	n := 100
	next := func() model.ID { n++; return cliID(n) }
	admit := func(events ...model.TypedEvent) {
		t.Helper()
		packet := next()
		if _, err := e2eInvoke(t, root, events, "capture", "--command-id", string(packet)); err != nil {
			t.Fatal(err)
		}
		if _, err := e2eInvoke(t, root, nil, "admit", "--command-id", string(next()), "--actor", "coordinator", "--outcome", "accepted", "--reason", "fresh-process e2e", string(packet)); err != nil {
			t.Fatal(err)
		}
	}
	lane := model.Provenance{Author: model.Actor{ID: "lane"}, SourceRefs: []model.ArtifactRef{}}
	scope := model.Scope{SourcePaths: []string{}, ContextRefs: []model.RecordRef{}, AppliesWhen: "this fixture", Limitations: "not a real ledger"}
	task := &model.TaskCreate{ID: next(), Provenance: lane, Spec: model.TaskSpec{Intent: "measure", Subject: "pose sweep", Scope: scope, NonGoals: []string{"production writes"},
		AcceptanceCriteria: []model.AcceptanceCriterion{{ID: next(), Revision: 1, Criterion: "measured"}}, ContextRefs: []model.RecordRef{}, ConstraintRefs: []model.RecordRef{}, Prerequisites: []model.Prerequisite{}, NextActor: model.Actor{ID: "lane"}}}
	claim := &model.ClaimAssert{ID: next(), Provenance: lane, Spec: model.ClaimSpec{Assertion: "every pose is below 0.05 mm", Falsifier: "a pose reaches 0.05 mm", Scope: scope, ExternalRefs: []model.ExternalReference{}}}
	instrument := &model.InstrumentDeclare{ID: next(), Provenance: lane, Spec: model.InstrumentSpec{QuestionAnswered: "pose penetration depth", BlindTo: "unmeasured poses",
		NotAnswered: "production behaviour", ConfigSurface: []string{}, DangerousDefaults: []string{}, ValidRange: "the fixture sweep",
		ImplementationRef: e2ePin(e2eScript, "tools/measure.sh", "text/plain"),
		Validation:        e2eKnown(model.InstrumentValidation{Ref: e2ePin(`{"validated":"against a known pose sweep"}`, "validation/measure.json", "application/json"), Version: "v1"})}}
	admit(task, claim, instrument)
	result, population := e2ePin(e2ePass, "out/result.json", "application/json"), e2ePin(e2ePass, "out/result.json", "application/json")
	result.Selector, population.Selector = model.Selector{Kind: "json-pointer", Pointer: "/results"}, model.Selector{Kind: "json-pointer", Pointer: "/population"}
	target := json.Number("0.05")
	claimRef := model.RecordRef{Project: "test/cli", RecordID: claim.ID, Revision: 1}
	fix := &model.CriterionFix{Claim: claimRef, CriterionID: next(), Revision: 1, Author: model.Actor{ID: "lane"}, SourceRefs: []model.ArtifactRef{},
		Expression: model.CriterionExpression{ResultSelector: result, Unit: "mm", Population: model.Population{Identity: "pose sweep", Selector: population, Denominator: "poses"},
			Operator: model.Less, Target: model.Scalar{Type: "number", Number: &target}, Reducer: model.All},
		Policy: model.EvaluationPolicy{Inclusion: "entire-criterion-family", Retry: "retain-all"}}
	admit(fix)
	attempt := next()
	admit(&model.TaskStart{Task: model.RecordRef{Project: "test/cli", RecordID: task.ID, Revision: 1}, Actor: model.Actor{ID: "lane"}, AttemptID: attempt})
	return root, model.CriterionRef{Claim: claimRef, CriterionID: fix.CriterionID, Revision: 1}, model.RecordRef{Project: "test/cli", RecordID: instrument.ID, Revision: 1}, attempt
}

func proofWrite(t *testing.T, root, path, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func e2eStatus(t *testing.T, root string, claim model.RecordRef) reduce.ClaimStatus {
	t.Helper()
	project, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := store.ReadPrefix(project)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reduce.Replay(prefix)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := snapshot.ClaimAt(claim)
	return p.Status
}

// The whole path through the real CLI, one fresh process per step: capture and
// admit the records, a frozen criterion and an attempt, `datum run` a producer,
// admit its start and seal (MEASURED), then capture and admit proof (PROVEN).
// The criterion's contract path out/result.json resolves in this run's own
// directory, record/artifacts/runs/<invocation-id>/out/result.json (R8.3).
func TestCLIFreshProcessesRunToProven(t *testing.T) {
	root, criterion, instrument, attempt := e2eWorld(t)
	out, err := e2eInvoke(t, root, nil, "run", "--attempt-id", string(attempt), "--instrument", string(instrument.RecordID),
		"--claim", string(criterion.Claim.RecordID), "--claim-revision", "1", "--criterion-id", string(criterion.CriterionID), "--criterion-revision", "1", "--", "/bin/sh", "tools/measure.sh")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Envelope    model.InvocationEnvelope `json:"Envelope"`
		StartPacket model.PacketRef          `json:"StartPacket"`
		SealPacket  model.PacketRef          `json:"SealPacket"`
	}
	if err := json.Unmarshal(out, &result); err != nil || result.SealPacket.CommandID == "" {
		t.Fatalf("run printed no packets: %s, %v", out, err)
	}
	if _, err := e2eInvoke(t, root, nil, "admit", "--command-id", string(cliID(900)), "--actor", "coordinator", "--outcome", "accepted", "--reason", "admit the run", string(result.StartPacket.CommandID), string(result.SealPacket.CommandID)); err != nil {
		t.Fatal(err)
	}
	if status := e2eStatus(t, root, criterion.Claim); status != reduce.StatusMeasured {
		t.Fatalf("admitted run left the claim %s", status)
	}
	proof := &model.ProofAdmit{Claim: criterion.Claim, CriterionRef: criterion, Judgment: model.ResponsibleJudgment{Actor: model.Actor{ID: "lane"}, Reason: "the run passed"},
		Evidence: []model.ObservationDisposition{{InvocationRef: model.InvocationRef{Project: "test/cli", InvocationID: result.Envelope.InvocationID}, Disposition: "supports", Reason: "passed"}}}
	if _, err := e2eInvoke(t, root, []model.TypedEvent{proof}, "capture", "--command-id", string(cliID(901))); err != nil {
		t.Fatal(err)
	}
	if _, err := e2eInvoke(t, root, nil, "admit", "--command-id", string(cliID(902)), "--actor", "coordinator", "--outcome", "accepted", "--reason", "proof", string(cliID(901))); err != nil {
		t.Fatal(err)
	}
	if status := e2eStatus(t, root, criterion.Claim); status != reduce.StatusProven {
		t.Fatalf("a real run's proof left the claim %s", status)
	}
}

// A producer outside write.Run may declare its output at the contract path
// itself; fresh processes still carry the claim from UNMEASURED to PROVEN.
func TestCLIFreshProcessesCaptureAdmitProofToProven(t *testing.T) {
	root, criterion, instrument, attempt := e2eWorld(t)
	if status := e2eStatus(t, root, criterion.Claim); status != reduce.StatusUnmeasured {
		t.Fatalf("claim starts %s", status)
	}
	unknown := func(why string) model.Availability[map[string]model.Availability[model.Scalar]] {
		return model.Availability[map[string]model.Availability[model.Scalar]]{State: model.Unknown, Reason: why}
	}
	env := model.InvocationEnvelope{InvocationID: cliID(700), AttemptID: attempt, InstrumentRef: instrument, CriterionRef: e2eKnown(criterion),
		ExecutionSourceIdentity: model.ExecutionIdentity{Project: "test/cli", SourceRefs: []model.ArtifactRef{}, MachineID: model.Availability[model.ID]{State: model.Unknown, Reason: "fixture"}, Head: model.Availability[model.GitHead]{State: model.Unknown, Reason: "fixture"}, Dirty: model.Availability[bool]{State: model.Unknown, Reason: "fixture"}},
		Argv:                    []string{"/bin/sh", "tools/measure.sh"}, InputRefs: []model.ArtifactRef{}, ConfigRequested: map[string]model.Scalar{}, ConditionsDeclared: map[string]model.Scalar{},
		ConfigEffective: unknown("not launched"), ConditionsObserved: unknown("not launched"), Isolation: model.Availability[model.Isolation]{State: model.Unknown, Reason: "not enforced"},
		StartedAt: time.Now().UTC(), ObservedAt: model.Availability[time.Time]{State: model.Unknown, Reason: "not launched"}, Outcome: model.Availability[model.ProcessOutcome]{State: model.Unknown, Reason: "not launched"},
		OutputRefs: model.Availability[[]model.ArtifactRef]{State: model.Unknown, Reason: "not launched"}, Visual: model.Availability[model.VisualObservation]{State: model.Unknown, Reason: "numeric"}}
	seal := env
	exit := 0
	seal.ObservedAt, seal.Outcome = e2eKnown(env.StartedAt.Add(time.Millisecond)), e2eKnown(model.ProcessOutcome{Kind: "exit", ExitCode: &exit})
	seal.OutputRefs = e2eKnown([]model.ArtifactRef{e2ePin(e2ePass, "out/result.json", "application/json")})
	steps := [][]string{{"capture", "--command-id", string(cliID(701))}, {"capture", "--command-id", string(cliID(702))}}
	for i, event := range []model.TypedEvent{&model.InvocationStart{Envelope: env}, &model.InvocationSeal{StartRef: model.InvocationRef{Project: "test/cli", InvocationID: env.InvocationID}, Envelope: seal}} {
		if _, err := e2eInvoke(t, root, []model.TypedEvent{event}, steps[i]...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e2eInvoke(t, root, nil, "admit", "--command-id", string(cliID(703)), "--actor", "coordinator", "--outcome", "accepted", "--reason", "run", string(cliID(701)), string(cliID(702))); err != nil {
		t.Fatal(err)
	}
	proof := &model.ProofAdmit{Claim: criterion.Claim, CriterionRef: criterion, Judgment: model.ResponsibleJudgment{Actor: model.Actor{ID: "lane"}, Reason: "the complete family passes"},
		Evidence: []model.ObservationDisposition{{InvocationRef: model.InvocationRef{Project: "test/cli", InvocationID: env.InvocationID}, Disposition: "supports", Reason: "passed"}}}
	if _, err := e2eInvoke(t, root, []model.TypedEvent{proof}, "capture", "--command-id", string(cliID(704))); err != nil {
		t.Fatal(err)
	}
	if _, err := e2eInvoke(t, root, nil, "admit", "--command-id", string(cliID(705)), "--actor", "coordinator", "--outcome", "accepted", "--reason", "proof", string(cliID(704))); err != nil {
		t.Fatal(err)
	}
	if status := e2eStatus(t, root, criterion.Claim); status != reduce.StatusProven {
		t.Fatalf("fresh-process proof left the claim %s", status)
	}
}

func TestCLIRunRefusesBeforeLaunch(t *testing.T) {
	root, criterion, instrument, attempt := e2eWorld(t)
	marker := filepath.Join(root, "launched")
	for name, args := range map[string][]string{
		"unadmitted-instrument": {"--attempt-id", string(attempt), "--instrument", string(cliID(999))},
		"unadmitted-criterion":  {"--attempt-id", string(attempt), "--instrument", string(instrument.RecordID), "--claim", string(criterion.Claim.RecordID), "--claim-revision", "1", "--criterion-id", string(cliID(998)), "--criterion-revision", "1"},
	} {
		_, err := callWriteCLI(t, root, nil, append(append([]string{"run"}, args...), "--", "/usr/bin/touch", marker)...)
		if err == nil || !strings.Contains(err.Error(), "not admitted") {
			t.Fatalf("%s: run was not refused before launch: %v", name, err)
		}
		if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
			t.Fatalf("%s: the command launched", name)
		}
	}
}

// A dead runner is reconciled through the CLI: its admitted start receives an
// attributed UNKNOWN-outcome seal with no reading, in fresh processes.
func TestCLIFreshProcessesReconcileADeadRunner(t *testing.T) {
	root, criterion, instrument, attempt := e2eWorld(t)
	unknown := func() model.Availability[map[string]model.Availability[model.Scalar]] {
		return model.Availability[map[string]model.Availability[model.Scalar]]{State: model.Unknown, Reason: "not launched"}
	}
	env := model.InvocationEnvelope{InvocationID: cliID(800), AttemptID: attempt, InstrumentRef: instrument, CriterionRef: e2eKnown(criterion),
		ExecutionSourceIdentity: model.ExecutionIdentity{Project: "test/cli", SourceRefs: []model.ArtifactRef{}, MachineID: model.Availability[model.ID]{State: model.Unknown, Reason: "fixture"}, Head: model.Availability[model.GitHead]{State: model.Unknown, Reason: "fixture"}, Dirty: model.Availability[bool]{State: model.Unknown, Reason: "fixture"}},
		Argv:                    []string{"/bin/sh", "tools/measure.sh"}, InputRefs: []model.ArtifactRef{}, ConfigRequested: map[string]model.Scalar{}, ConditionsDeclared: map[string]model.Scalar{},
		ConfigEffective: unknown(), ConditionsObserved: unknown(), Isolation: model.Availability[model.Isolation]{State: model.Unknown, Reason: "not enforced"},
		StartedAt: time.Now().UTC(), ObservedAt: model.Availability[time.Time]{State: model.Unknown, Reason: "not launched"}, Outcome: model.Availability[model.ProcessOutcome]{State: model.Unknown, Reason: "not launched"},
		OutputRefs: model.Availability[[]model.ArtifactRef]{State: model.Unknown, Reason: "not launched"}, Visual: model.Availability[model.VisualObservation]{State: model.Unknown, Reason: "numeric"}}
	if _, err := e2eInvoke(t, root, []model.TypedEvent{&model.InvocationStart{Envelope: env}}, "capture", "--command-id", string(cliID(801))); err != nil {
		t.Fatal(err)
	}
	if _, err := e2eInvoke(t, root, nil, "admit", "--command-id", string(cliID(802)), "--actor", "coordinator", "--outcome", "accepted", "--reason", "start", string(cliID(801))); err != nil {
		t.Fatal(err)
	}
	out, err := e2eInvoke(t, root, nil, "reconcile", "--invocation-id", string(env.InvocationID), "--reason", "runner host lost power")
	if err != nil {
		t.Fatal(err)
	}
	var packet model.PacketRef
	if err := json.Unmarshal(out, &packet); err != nil || packet.CommandID == "" {
		t.Fatalf("reconcile printed no packet: %s, %v", out, err)
	}
	if _, err := e2eInvoke(t, root, nil, "admit", "--command-id", string(cliID(803)), "--actor", "coordinator", "--outcome", "accepted", "--reason", "reconciled", string(packet.CommandID)); err != nil {
		t.Fatal(err)
	}
	project, _ := store.Discover(root)
	prefix, _ := store.ReadPrefix(project)
	snapshot, err := reduce.Replay(prefix)
	inv, ok := snapshot.Invocation(reduce.InvocationKey{Project: "test/cli", InvocationID: env.InvocationID})
	if err != nil || !ok || inv.Seal == nil || inv.Seal.Outcome.State != model.Unknown || inv.Seal.OutputRefs.State != model.Unknown || !strings.Contains(inv.Seal.Outcome.Reason, "lane") {
		t.Fatalf("reconciliation seal missing or carrying a reading: %+v, %v", inv.Seal, err)
	}
	if status := e2eStatus(t, root, criterion.Claim); status != reduce.StatusUnmeasured {
		t.Fatalf("a reconciled run with no reading made the claim %s", status)
	}
}
