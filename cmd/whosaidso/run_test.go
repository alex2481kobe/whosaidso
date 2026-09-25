package main

// This file holds the end-to-end tests that drive run, proof and
// reconcile through fresh processes, with their fixtures. Capture, admit and
// handback CLI tests stay in write_test.go.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ---- run and proof through fresh processes ----------------------------

const (
	e2ePass   = `{"results":{"unit":"mm","population":"pose sweep","denominator":"poses","values":[0.0100,0.0200]},"population":{"population":"pose sweep","denominator":"poses","values":["pose-a","pose-b"]}}`
	e2eScript = "mkdir \"$WHOSAIDSO_RUN_DIR/out\"\nprintf '%s' '" + e2ePass + "' > \"$WHOSAIDSO_RUN_DIR/out/result.json\"\nprintf '%s' '{\"version\":1,\"config_effective\":{},\"conditions_observed\":{},\"outputs\":[{\"path\":\"out/result.json\",\"media_type\":\"application/json\"}]}' > \"$WHOSAIDSO_RUN_REPORT\"\n"
)

func e2ePin(body, path, media string) model.ArtifactRef {
	return model.ArtifactRef{Kind: "content", Content: &model.ContentPin{SHA256: model.HashBytes([]byte(body)), Length: uint64(len(body)), MediaType: media, Locators: []model.Locator{{Path: path}}}, Selector: model.Selector{Kind: "whole"}}
}

func e2eOutput(body, name, media string) model.RunOutput {
	return model.RunOutput{Name: name, SHA256: model.HashBytes([]byte(body)), Length: uint64(len(body)), MediaType: media}
}

func e2eKnown[T any](v T) model.Availability[T] {
	return model.Availability[T]{State: model.Known, Value: &v}
}

// e2eInvoke runs one whosaidso command in a fresh process and returns its stdout.
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
	command := exec.Command(binary, append([]string{"-test.run=^TestWhoSaidSoMainProcess$", "--"}, withJSON(args)...)...)
	command.Dir, command.Stdin = root, bytes.NewReader(input)
	command.Env = append(os.Environ(), "WHOSAIDSO_MAIN_TEST_PROCESS=1", "WHOSAIDSO_ACTOR=agent")
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
	agent := model.Provenance{SourceRefs: []model.ArtifactRef{}}
	scope := model.Scope{SourcePaths: []string{}, ContextRefs: []model.RecordRef{}, AppliesWhen: "this fixture", Limitations: "not a real ledger"}
	task := &model.TaskCreate{ID: next(), Provenance: agent, Spec: model.TaskSpec{Intent: "measure", Subject: "pose sweep", Scope: scope, NonGoals: []string{"production writes"},
		AcceptanceCriteria: []model.AcceptanceCriterion{{ID: next(), Revision: 1, Criterion: "measured"}}, ContextRefs: []model.RecordRef{}, ConstraintRefs: []model.RecordRef{}, Prerequisites: []model.Prerequisite{}, NextActor: model.Actor{ID: "agent"}}}
	claim := &model.ClaimAssert{ID: next(), Provenance: agent, Spec: model.ClaimSpec{Assertion: "every pose is below 0.05 mm", Falsifier: "a pose reaches 0.05 mm", Scope: scope, ExternalRefs: []model.ExternalReference{}}}
	instrument := &model.InstrumentDeclare{ID: next(), Provenance: agent, Spec: model.InstrumentSpec{QuestionAnswered: "pose penetration depth", BlindTo: "unmeasured poses",
		NotAnswered: "production behaviour", ConfigSurface: []string{}, DangerousDefaults: []string{}, ValidRange: "the fixture sweep",
		ImplementationRef: e2ePin(e2eScript, "tools/measure.sh", "text/plain"),
		Validation:        e2eKnown(model.InstrumentValidation{Ref: e2ePin(`{"validated":"against a known pose sweep"}`, "validation/measure.json", "application/json"), Version: "v1"})}}
	admit(task, claim, instrument)
	result, population := e2ePin(e2ePass, "out/result.json", "application/json"), e2ePin(e2ePass, "out/result.json", "application/json")
	result.Selector, population.Selector = model.Selector{Kind: "json-pointer", Pointer: "/results"}, model.Selector{Kind: "json-pointer", Pointer: "/population"}
	target := json.Number("0.05")
	claimRef := model.RecordRef{Project: "test/cli", RecordID: claim.ID, Revision: 1}
	fix := &model.CriterionFix{Claim: claimRef, CriterionID: next(), Revision: 1, Author: model.Actor{ID: "agent"}, SourceRefs: []model.ArtifactRef{},
		Expression: model.CriterionExpression{ResultSelector: result, Unit: "mm", Population: model.Population{Identity: "pose sweep", Selector: population, Denominator: "poses"},
			Operator: model.Less, Target: model.Scalar{Type: "number", Number: &target}, Reducer: model.All},
		Policy: model.EvaluationPolicy{Inclusion: "entire-criterion-family", Retry: "retain-all"}}
	admit(fix)
	attempt := next()
	admit(&model.TaskStart{Task: model.RecordRef{Project: "test/cli", RecordID: task.ID, Revision: 1}, Actor: model.Actor{ID: "agent"}, AttemptID: attempt})
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
// admit the records, a frozen criterion and an attempt, `whosaidso run` a producer,
// admit its start and seal (MEASURED), then capture and admit proof (PROVEN).
// The criterion's contract path out/result.json names this run's output
// out/result.json.
func TestCLIFreshProcessesRunToProven(t *testing.T) {
	root, criterion, instrument, attempt := e2eWorld(t)
	out, err := e2eInvoke(t, root, nil, "run", "--attempt-id", string(attempt), "--instrument", string(instrument.RecordID),
		"--claim", string(criterion.Claim.RecordID), "--claim-revision", "1", "--criterion-id", string(criterion.CriterionID), "--criterion-revision", "1", "--", "/bin/sh", "tools/measure.sh")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Envelope    model.InvocationEnvelope `json:"envelope"`
		StartPacket model.PacketRef          `json:"start_packet"`
		SealPacket  model.PacketRef          `json:"seal_packet"`
	}
	if err := json.Unmarshal(out, &result); err != nil || result.SealPacket.CommandID == "" {
		t.Fatalf("run printed no packets: %s, %v", out, err)
	}
	// The run records this machine's persistent id, and says why it has no git
	// state: the fixture root is not a git checkout.
	identity := result.Envelope.ExecutionSourceIdentity
	home, err := store.Home()
	if err != nil {
		t.Fatal(err)
	}
	machine, err := os.ReadFile(filepath.Join(home, store.MachineIDFile))
	if err != nil || identity.MachineID.State != model.Known || string(*identity.MachineID.Value)+"\n" != string(machine) {
		t.Fatalf("run must record the persistent machine id %q: %+v, %v", machine, identity.MachineID, err)
	}
	if identity.Head.State != model.Unknown || identity.Dirty.State != model.Unknown || !strings.Contains(identity.Head.Reason, "not a readable git checkout") {
		t.Fatalf("head and dirty outside a git checkout must be UNKNOWN with the reason: %+v, %+v", identity.Head, identity.Dirty)
	}
	if _, err := e2eInvoke(t, root, nil, "admit", "--command-id", string(cliID(900)), "--actor", "coordinator", "--outcome", "accepted", "--reason", "admit the run", string(result.StartPacket.CommandID), string(result.SealPacket.CommandID)); err != nil {
		t.Fatal(err)
	}
	if status := e2eStatus(t, root, criterion.Claim); status != reduce.StatusMeasured {
		t.Fatalf("admitted run left the claim %s", status)
	}
	proof := &model.ProofAdmit{Claim: criterion.Claim, CriterionRef: criterion, Verdict: model.VerdictSupports, Judgment: model.ResponsibleJudgment{Actor: model.Actor{ID: "agent"}, Reason: "the run passed"},
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

// A producer outside write.Run declares its output in its own run directory,
// the one output form; fresh processes carry the claim from UNMEASURED to
// PROVEN. The bare contract-path form is refused in internal/write tests.
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
		Outputs: model.Availability[[]model.RunOutput]{State: model.Unknown, Reason: "not launched"}, Visual: model.Availability[model.VisualObservation]{State: model.Unknown, Reason: "numeric"}}
	seal := env
	exit := 0
	seal.ObservedAt, seal.Outcome = e2eKnown(env.StartedAt.Add(time.Millisecond)), e2eKnown(model.ProcessOutcome{Kind: "exit", ExitCode: &exit})
	blob := filepath.Join(t.TempDir(), "result.json")
	proofWrite(t, filepath.Dir(blob), "result.json", e2ePass)
	seal.Outputs = e2eKnown([]model.RunOutput{e2eOutput(e2ePass, "out/result.json", "application/json")})
	steps := [][]string{{"capture", "--command-id", string(cliID(701))}, {"capture", "--command-id", string(cliID(702)), "--blob", blob}}
	for i, event := range []model.TypedEvent{&model.InvocationStart{Envelope: env}, &model.InvocationSeal{StartRef: model.InvocationRef{Project: "test/cli", InvocationID: env.InvocationID}, Envelope: seal}} {
		if _, err := e2eInvoke(t, root, []model.TypedEvent{event}, steps[i]...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e2eInvoke(t, root, nil, "admit", "--command-id", string(cliID(703)), "--actor", "coordinator", "--outcome", "accepted", "--reason", "run", string(cliID(701)), string(cliID(702))); err != nil {
		t.Fatal(err)
	}
	proof := &model.ProofAdmit{Claim: criterion.Claim, CriterionRef: criterion, Verdict: model.VerdictSupports, Judgment: model.ResponsibleJudgment{Actor: model.Actor{ID: "agent"}, Reason: "the complete family passes"},
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

// The artifact store and run directories sit beside the configured
// ledger, in its record folder, never at a second hard-coded location. The
// fixture's record folder is moved and whosaidso.toml follows it; a real run must
// then write, admit, store and prove from the moved folder alone.
func TestCLIRunKeepsArtifactsBesideAConfiguredLedger(t *testing.T) {
	root, criterion, instrument, attempt := e2eWorld(t)
	moved := filepath.Join(root, "custom", "rec")
	if err := os.MkdirAll(filepath.Dir(moved), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, ".whosaidso"), moved); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "whosaidso.toml"), []byte("id = \"test/cli\"\nledger = \"custom/rec/events\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := e2eInvoke(t, root, nil, "run", "--attempt-id", string(attempt), "--instrument", string(instrument.RecordID),
		"--claim", string(criterion.Claim.RecordID), "--claim-revision", "1", "--criterion-id", string(criterion.CriterionID), "--criterion-revision", "1", "--", "/bin/sh", "tools/measure.sh")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Envelope    model.InvocationEnvelope `json:"envelope"`
		StartPacket model.PacketRef          `json:"start_packet"`
		SealPacket  model.PacketRef          `json:"seal_packet"`
	}
	if err := json.Unmarshal(out, &result); err != nil || result.SealPacket.CommandID == "" {
		t.Fatalf("run printed no packets: %s, %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(moved, "artifacts", "runs")); !os.IsNotExist(err) {
		t.Fatalf("a run stages outside the committed store; no run directory may appear in it: %v", err)
	}
	if _, err := e2eInvoke(t, root, nil, "admit", "--command-id", string(cliID(900)), "--actor", "coordinator", "--outcome", "accepted", "--reason", "admit the run", string(result.StartPacket.CommandID), string(result.SealPacket.CommandID)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moved, "artifacts", string(model.HashBytes([]byte(e2ePass))))); err != nil {
		t.Fatalf("admission must store the run's output beside the configured ledger: %v", err)
	}
	proof := &model.ProofAdmit{Claim: criterion.Claim, CriterionRef: criterion, Verdict: model.VerdictSupports, Judgment: model.ResponsibleJudgment{Actor: model.Actor{ID: "agent"}, Reason: "the run passed"},
		Evidence: []model.ObservationDisposition{{InvocationRef: model.InvocationRef{Project: "test/cli", InvocationID: result.Envelope.InvocationID}, Disposition: "supports", Reason: "passed"}}}
	if _, err := e2eInvoke(t, root, []model.TypedEvent{proof}, "capture", "--command-id", string(cliID(901))); err != nil {
		t.Fatal(err)
	}
	if _, err := e2eInvoke(t, root, nil, "admit", "--command-id", string(cliID(902)), "--actor", "coordinator", "--outcome", "accepted", "--reason", "proof", string(cliID(901))); err != nil {
		t.Fatal(err)
	}
	if status := e2eStatus(t, root, criterion.Claim); status != reduce.StatusProven {
		t.Fatalf("a run beside a configured ledger left the claim %s", status)
	}
	if _, err := os.Stat(filepath.Join(root, ".whosaidso")); !os.IsNotExist(err) {
		t.Fatalf("nothing may be written to the default record folder when the ledger is elsewhere: %v", err)
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
		Outputs: model.Availability[[]model.RunOutput]{State: model.Unknown, Reason: "not launched"}, Visual: model.Availability[model.VisualObservation]{State: model.Unknown, Reason: "numeric"}}
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
	if err != nil || !ok || inv.Seal == nil || inv.Seal.Outcome.State != model.Unknown || inv.Seal.Outputs.State != model.Unknown || !strings.Contains(inv.Seal.Outcome.Reason, "agent") {
		t.Fatalf("reconciliation seal missing or carrying a reading: %+v, %v", inv.Seal, err)
	}
	if status := e2eStatus(t, root, criterion.Claim); status != reduce.StatusUnmeasured {
		t.Fatalf("a reconciled run with no reading made the claim %s", status)
	}
}
