package write

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

func runTestID(n int) model.ID { return model.ID(fmt.Sprintf("%026d", n)) }

func runTestProject(t *testing.T) store.Project {
	t.Helper()
	base := t.TempDir()
	t.Setenv("HOME", base)
	root := filepath.Join(base, "project")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	return store.Project{ID: "datum/run-test", Root: root, Ledger: filepath.Join(root, "record", "events")}
}

func runTestNumber(s string) model.Scalar {
	n := json.Number(s)
	return model.Scalar{Type: "number", Number: &n}
}

func runTestRequest(root, mode string, args ...string) RunRequest {
	ref := model.ArtifactRef{Kind: "content", Content: &model.ContentPin{SHA256: model.HashBytes([]byte("fixture")), Length: 7, MediaType: "text/plain", Locators: []model.Locator{{Path: "fixture"}}}, Selector: model.Selector{Kind: "whole"}}
	argv := append([]string{os.Args[0], "-test.run=^TestRunChildProcess$", "--", mode}, args...)
	return RunRequest{
		Author: model.Actor{ID: "lane-c"}, AttemptID: runTestID(2),
		InstrumentRef:           model.RecordRef{Project: "datum/run-test", RecordID: runTestID(3), Revision: 1},
		Instrument:              model.InstrumentSpec{QuestionAnswered: "what did the process observe", BlindTo: "unreported configuration", NotAnswered: "task completion", ConfigSurface: []string{"samples", "seed"}, DangerousDefaults: []string{}, ValidRange: "fixture inputs", ImplementationRef: ref, Validation: runUnknown[model.InstrumentValidation]()},
		ExecutionSourceIdentity: model.ExecutionIdentity{Project: "datum/run-test", MachineID: runUnknown[model.ID](), SourceRefs: []model.ArtifactRef{ref}, Head: runUnknown[model.GitHead](), Dirty: runUnknown[bool]()},
		Argv:                    argv, Dir: root, ConfigRequested: map[string]model.Scalar{"samples": runTestNumber("64"), "seed": runTestNumber("99")},
		ConditionsDeclared: map[string]model.Scalar{"idle": {Type: "bool", Bool: runTestPtr(true)}},
	}
}

func runTestPtr[T any](v T) *T { return &v }

func runTestControl(t *testing.T, project store.Project) RunResult {
	t.Helper()
	result, err := Run(context.Background(), project, runTestRequest(project.Root, "ok"))
	if err != nil {
		t.Fatalf("good control: %v", err)
	}
	runTestOutcome(t, result.Envelope, "exit", 0)
	if result.StartPacket.CommandID == "" || result.SealPacket.CommandID == "" {
		t.Fatal("control did not publish both packets")
	}
	runTestPackets(t, project, result)
	return result
}

func runTestOutcome(t *testing.T, e model.InvocationEnvelope, kind string, code int) {
	t.Helper()
	if e.Outcome.State != model.Known || e.Outcome.Value == nil || e.Outcome.Value.Kind != kind {
		t.Fatalf("outcome = %+v, want %s", e.Outcome, kind)
	}
	if kind == "exit" && (e.Outcome.Value.ExitCode == nil || *e.Outcome.Value.ExitCode != code) {
		t.Fatalf("exit = %+v, want %d", e.Outcome.Value, code)
	}
}

func runTestPackets(t *testing.T, project store.Project, result RunResult) []model.Event {
	t.Helper()
	packets, err := store.ReadIntake(project, []model.ID{result.StartPacket.CommandID, result.SealPacket.CommandID})
	if err != nil {
		t.Fatal(err)
	}
	if len(packets) != 2 || len(packets[0].Events) != 1 || len(packets[1].Events) != 1 {
		t.Fatalf("unexpected packets: %+v", packets)
	}
	start, err := model.DecodeEvent(packets[0].Events[0])
	if err != nil {
		t.Fatal(err)
	}
	seal, err := model.DecodeEvent(packets[1].Events[0])
	if err != nil {
		t.Fatal(err)
	}
	s := start.(*model.InvocationStart).Envelope
	e := seal.(*model.InvocationSeal).Envelope
	if s.Outcome.State != model.Unknown || s.ObservedAt.State != model.Unknown || s.OutputRefs.State != model.Unknown || s.ConfigEffective.State != model.Unknown || s.ConditionsObserved.State != model.Unknown || s.Visual.State != model.Unknown || s.Isolation.State != model.Unknown {
		t.Fatal("start contains observations")
	}
	if !reflect.DeepEqual(e, result.Envelope) {
		t.Fatal("returned envelope differs from durable seal")
	}
	e.ConfigEffective, e.ConditionsObserved, e.Visual, e.Isolation = s.ConfigEffective, s.ConditionsObserved, s.Visual, s.Isolation
	e.Outcome, e.ObservedAt, e.OutputRefs = s.Outcome, s.ObservedAt, s.OutputRefs
	if !reflect.DeepEqual(e, s) {
		t.Fatal("seal changed immutable intent")
	}
	return []model.Event{packets[0].Events[0], packets[1].Events[0]}
}

func runTestArtifact(t *testing.T, project store.Project, result RunResult, name string) []byte {
	t.Helper()
	if result.Envelope.OutputRefs.Value == nil {
		t.Fatal("missing output refs")
	}
	for _, ref := range *result.Envelope.OutputRefs.Value {
		if filepath.Base(ref.Content.Locators[0].Path) != name {
			continue
		}
		inbox, err := store.IntakeDir(project)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(inbox, string(result.SealPacket.CommandID), "blobs", string(ref.Content.SHA256)))
		if err != nil {
			t.Fatal(err)
		}
		if uint64(len(data)) != ref.Content.Length || model.HashBytes(data) != ref.Content.SHA256 {
			t.Fatal("seal pins different bytes")
		}
		return data
	}
	t.Fatalf("artifact %s absent", name)
	return nil
}

func TestRunCommandFailureDoesNotEndAttempt(t *testing.T) {
	project := runTestProject(t)
	control := runTestControl(t, project)
	if string(runTestArtifact(t, project, control, "stdout")) != "intent-was-durable\n" {
		t.Fatal("child did not verify prelaunch intent")
	}
	r := runTestRequest(project.Root, "fail")
	result, err := Run(context.Background(), project, r)
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("failed child error = %v", err)
	}
	runTestOutcome(t, result.Envelope, "exit", 7)
	events := runTestPackets(t, project, result)
	if result.Envelope.ConfigEffective.State != model.Unknown || result.Envelope.ConditionsObserved.State != model.Unknown || result.Envelope.Isolation.State != model.Unknown {
		t.Fatal("request became an observation")
	}
	provenance := model.Provenance{Author: r.Author, SourceRefs: []model.ArtifactRef{}}
	taskRef := model.RecordRef{Project: project.ID, RecordID: runTestID(1), Revision: 1}
	prefix := []model.TypedEvent{
		&model.TaskCreate{ID: taskRef.RecordID, Provenance: provenance, Spec: model.TaskSpec{Intent: "observe subprocesses", Subject: "runner", Scope: model.Scope{SourcePaths: []string{"internal/write"}, ContextRefs: []model.RecordRef{}, AppliesWhen: "running", Limitations: "fixture only"}, NonGoals: []string{"no task closure"}, AcceptanceCriteria: []model.AcceptanceCriterion{{ID: runTestID(4), Revision: 1, Criterion: "observations are honest"}}, ContextRefs: []model.RecordRef{}, ConstraintRefs: []model.RecordRef{}, Prerequisites: []model.Prerequisite{}, NextActor: r.Author}},
		&model.InstrumentDeclare{ID: r.InstrumentRef.RecordID, Provenance: provenance, Spec: r.Instrument},
		&model.TaskStart{Task: taskRef, AttemptID: r.AttemptID, Actor: r.Author},
	}
	bundle := model.Bundle{Version: model.WireVersion, Project: project.ID, Sequence: 1, CommandID: runTestID(10), RequestDigest: model.HashBytes([]byte("fixture bundle")), Admitter: model.Actor{ID: "coordinator"}, RecordedAt: time.Now().UTC(), Packets: []model.PacketRef{}}
	for _, payload := range prefix {
		event, err := model.EncodeEvent(payload)
		if err != nil {
			t.Fatal(err)
		}
		bundle.Events = append(bundle.Events, event)
	}
	before, err := reduce.Replay([]model.Bundle{bundle})
	if err != nil {
		t.Fatal(err)
	}
	beforeTask, ok := before.Task(reduce.Ident{Project: project.ID, ID: taskRef.RecordID})
	if !ok || beforeTask.Status != reduce.StatusInFlight {
		t.Fatal("good control lacks live attempt")
	}
	bundle.Events = append(bundle.Events, events...)
	after, err := reduce.Replay([]model.Bundle{bundle})
	if err != nil {
		t.Fatal(err)
	}
	afterTask, _ := after.Task(reduce.Ident{Project: project.ID, ID: taskRef.RecordID})
	if afterTask.Status != reduce.StatusInFlight || len(afterTask.LiveAttempts) != 1 {
		t.Fatalf("failure ended lane: %+v", afterTask)
	}
}

func TestRunSpawnFailureAndLiteralArgv(t *testing.T) {
	project := runTestProject(t)
	runTestControl(t, project)
	r := runTestRequest(project.Root, "ok")
	r.Argv = []string{filepath.Join(project.Root, "does-not-exist")}
	result, err := Run(context.Background(), project, r)
	if err == nil {
		t.Fatal("missing executable accepted")
	}
	runTestOutcome(t, result.Envelope, "spawn-failed", 0)
	runTestPackets(t, project, result)
	marker := filepath.Join(project.Root, "shell-was-run")
	r.Argv = []string{"touch " + marker}
	result, err = Run(context.Background(), project, r)
	if err == nil {
		t.Fatal("shell string accepted")
	}
	runTestOutcome(t, result.Envelope, "spawn-failed", 0)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("implicit shell executed: %v", err)
	}
	r = runTestRequest(project.Root, "args", "a b", "$(touch "+marker+")", "x;y")
	result, err = Run(context.Background(), project, r)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(runTestArtifact(t, project, result, "stdout"), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, r.Argv[4:]) {
		t.Fatalf("argv changed: %q", got)
	}
}

func TestRunRefusesBeforeLaunch(t *testing.T) {
	project := runTestProject(t)
	runTestControl(t, project)
	for _, name := range []string{"empty argv", "NUL argv", "invalid actor", "undeclared knob", "cancelled", "intake unavailable"} {
		t.Run(name, func(t *testing.T) {
			runTestControl(t, project)
			marker := filepath.Join(project.Root, "marker")
			r := runTestRequest(project.Root, "mark", marker)
			ctx := context.Background()
			switch name {
			case "empty argv":
				r.Argv = nil
			case "NUL argv":
				r.Argv = append(r.Argv, "bad\x00arg")
			case "invalid actor":
				r.Author = model.Actor{}
			case "undeclared knob":
				r.ConfigRequested["not-declared"] = runTestNumber("1")
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "intake unavailable":
				home := t.TempDir()
				t.Setenv("HOME", home)
				if err := os.WriteFile(filepath.Join(home, ".datum"), []byte("not a directory"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			result, err := Run(ctx, project, r)
			if err == nil || result.StartPacket.CommandID != "" || result.SealPacket.CommandID != "" {
				t.Fatalf("refusal = %+v, %v", result, err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("refused command launched: %v", err)
			}
		})
	}
}

func TestRunProducerFactsAndBoundedStreams(t *testing.T) {
	project := runTestProject(t)
	runTestControl(t, project)
	report := `{"version":1,"config_effective":{"samples":{"type":"number","number":8}},"conditions_observed":{"idle":{"type":"bool","bool":false}},"visual":{"backend":{"state":"known","value":"webgl"},"framing":{"state":"known","value":{"projection":{"state":"known","value":"perspective"}}}},"outputs":[{"path":"result.json","media_type":"application/json"}]}`
	result, err := Run(context.Background(), project, runTestRequest(project.Root, "report", report))
	if err != nil {
		t.Fatal(err)
	}
	runTestPackets(t, project, result)
	e := result.Envelope
	if (*e.ConfigEffective.Value)["samples"].Value == nil || string(*(*e.ConfigEffective.Value)["samples"].Value.Number) != "8" || (*e.ConfigEffective.Value)["seed"].State != model.Unknown {
		t.Fatal("effective config was inferred from request")
	}
	if *(*e.ConditionsObserved.Value)["idle"].Value.Bool {
		t.Fatal("declared condition replaced actual condition")
	}
	if e.Visual.Value.Backend.State != model.Known || e.Visual.Value.Clip.State != model.Unknown || e.Visual.Value.Framing.Value.CameraPosition.State != model.Unknown {
		t.Fatal("missing visual metadata was invented")
	}
	if string(runTestArtifact(t, project, result, "producer.json")) != report || string(runTestArtifact(t, project, result, "result.json")) != `{"actual":8}` {
		t.Fatal("producer artifacts changed")
	}
	result, err = Run(context.Background(), project, runTestRequest(project.Root, "burst"))
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []struct {
		name  string
		tail  []byte
		value byte
	}{{"stdout", result.StdoutTail, 'o'}, {"stderr", result.StderrTail, 'e'}} {
		data := runTestArtifact(t, project, result, output.name)
		if len(data) != 2<<20 || !bytes.Equal(data, bytes.Repeat([]byte{output.value}, 2<<20)) || len(output.tail) != runTailBytes || !bytes.Equal(output.tail, data[len(data)-runTailBytes:]) {
			t.Fatalf("stream or tail corrupted: %s", output.name)
		}
	}
	packets, err := store.ReadIntake(project, []model.ID{result.SealPacket.CommandID})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := model.Encode(packets[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 16<<10 || bytes.Contains(encoded, bytes.Repeat([]byte("o"), 64)) {
		t.Fatal("full output entered record")
	}
}

func TestRunInvalidReportStillSealsObservedExit(t *testing.T) {
	project := runTestProject(t)
	control, err := Run(context.Background(), project, runTestRequest(project.Root, "report", `{"version":1,"config_effective":{"samples":{"type":"number","number":8}}}`))
	if err != nil || control.Envelope.ConfigEffective.State != model.Known {
		t.Fatalf("good report control: %v", err)
	}
	runTestPackets(t, project, control)
	for _, report := range []string{
		`{"version":1,"config_effective":{"unknown":{"type":"number","number":1}}}`,
		`{"version":1,"config_effective":{"samples":{"type":"number","number":1}},"outcome":"pass"}`,
		`{"version":1,"version":2,"version":1}`,
		`{"version":1,"outputs":[{"path":"../escape","media_type":"text/plain"}]}`,
		`{"version":1,"outputs":[{"path":"link","media_type":"text/plain"}]}`,
		`{"version":1} {"version":1}`,
		strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66),
	} {
		result, err := Run(context.Background(), project, runTestRequest(project.Root, "report", report))
		if err == nil {
			t.Fatalf("invalid report accepted: %s", report)
		}
		runTestOutcome(t, result.Envelope, "exit", 0)
		runTestPackets(t, project, result)
		if result.Envelope.ConfigEffective.State != model.Unknown || result.Envelope.Visual.State != model.Unknown {
			t.Fatal("invalid report supplied observations")
		}
		if string(runTestArtifact(t, project, result, "producer.json")) != report {
			t.Fatal("rejected report lost")
		}
	}
}

func TestRunSurvivingObserverSealsSignalAndCancellation(t *testing.T) {
	project := runTestProject(t)
	runTestControl(t, project)
	result, err := Run(context.Background(), project, runTestRequest(project.Root, "signal"))
	if err == nil {
		t.Fatal("signal returned success")
	}
	runTestOutcome(t, result.Envelope, "signal", 0)
	if result.Envelope.Outcome.Value.Signal == nil || *result.Envelope.Outcome.Value.Signal != syscall.SIGTERM.String() {
		t.Fatalf("wrong observed signal: %+v", result.Envelope.Outcome.Value)
	}
	runTestPackets(t, project, result)
	marker := filepath.Join(project.Root, "cancel-ready")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { result, err = Run(ctx, project, runTestRequest(project.Root, "block", marker)); close(done) }()
	runTestWaitFile(t, marker)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled runner did not return")
	}
	if err == nil {
		t.Fatal("cancelled child returned success")
	}
	runTestOutcome(t, result.Envelope, "signal", 0)
	runTestPackets(t, project, result)
}

func TestRunKilledObserverLeavesDurableUnknown(t *testing.T) {
	project := runTestProject(t)
	runTestControl(t, project)
	marker := filepath.Join(project.Root, "orphan-ready")
	observer := exec.Command(os.Args[0], "-test.run=^TestRunChildProcess$", "--", "observer", project.Root, marker)
	var diagnostic bytes.Buffer
	observer.Stderr = &diagnostic
	if err := observer.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observer.Process.Kill() })
	pid := runTestWaitFile(t, marker)
	childID, err := strconv.Atoi(string(pid))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p, err := os.FindProcess(childID)
		if err == nil {
			_ = p.Kill()
		}
	})
	before, err := store.ReadIntake(project, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 3 {
		t.Fatalf("child ran without one durable start: %d packets", len(before))
	}
	if err := observer.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := observer.Wait(); err == nil {
		t.Fatal("observer survived kill")
	}
	after, err := store.ReadIntake(project, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("killed observer fabricated terminal packet")
	}
	found := false
	for _, packet := range after {
		payload, err := model.DecodeEvent(packet.Events[0])
		if err != nil {
			t.Fatal(err)
		}
		if start, ok := payload.(*model.InvocationStart); ok && start.Envelope.Argv[3] == "block" {
			found = true
			if start.Envelope.Outcome.State != model.Unknown || start.Envelope.ObservedAt.State != model.Unknown || start.Envelope.OutputRefs.State != model.Unknown {
				t.Fatal("dead observer claimed terminal knowledge")
			}
		}
	}
	if !found {
		t.Fatalf("missing orphan invocation, observer stderr: %s", diagnostic.String())
	}
}

func TestRunBoundedDrainAndNoAdmissionLock(t *testing.T) {
	project := runTestProject(t)
	runTestControl(t, project)
	// A ledger path that cannot be opened proves run never asks admission to read
	// or lock it. Intake and process observation remain available independently.
	project.Ledger = filepath.Join(project.Root, "not-a-ledger")
	if err := os.WriteFile(project.Ledger, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, code := range []int{0, 7} {
		marker := filepath.Join(project.Root, fmt.Sprintf("descendant-%d", code))
		started := time.Now()
		result, err := Run(context.Background(), project, runTestRequest(project.Root, "descendant", marker, strconv.Itoa(code)))
		pid, readErr := os.ReadFile(marker)
		if readErr != nil {
			t.Fatal(readErr)
		}
		childID, parseErr := strconv.Atoi(string(pid))
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		p, findErr := os.FindProcess(childID)
		if findErr != nil {
			t.Fatal(findErr)
		}
		_ = p.Kill()
		if time.Since(started) > 5*time.Second {
			t.Fatal("descendant held observer open")
		}
		if err == nil {
			t.Fatal("incomplete stream capture returned success")
		}
		runTestOutcome(t, result.Envelope, "exit", code)
		runTestPackets(t, project, result)
		if result.Envelope.OutputRefs.State != model.Unknown {
			t.Fatal("truncated stream claimed complete capture")
		}
	}
}

func runTestWaitFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			return data
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child did not become ready: %s", path)
	return nil
}

// The fixture is the actual spawned binary so exit, signal, inherited handles
// and observer death all cross a real operating system process boundary.
func TestRunChildProcess(t *testing.T) {
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 {
		return
	}
	args := os.Args[separator+1:]
	if len(args) == 0 {
		os.Exit(90)
	}
	mode := args[0]
	if mode == "observer" {
		project := store.Project{ID: "datum/run-test", Root: args[1]}
		_, err := Run(context.Background(), project, runTestRequest(project.Root, "block", args[2]))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(91)
		}
		os.Exit(0)
	}
	if mode == "holdpipe" {
		time.Sleep(12 * time.Second)
		os.Exit(0)
	}
	if mode == "signal" {
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		time.Sleep(time.Second)
		os.Exit(92)
	}
	switch mode {
	case "ok", "fail":
		packets, err := store.ReadIntake(store.Project{ID: "datum/run-test"}, nil)
		if err != nil {
			os.Exit(93)
		}
		found := false
		for _, packet := range packets {
			payload, err := model.DecodeEvent(packet.Events[0])
			if err != nil {
				os.Exit(94)
			}
			if start, ok := payload.(*model.InvocationStart); ok && string(start.Envelope.InvocationID) == filepath.Base(os.Getenv("DATUM_RUN_DIR")) && start.Envelope.Outcome.State == model.Unknown {
				found = true
			}
		}
		if !found {
			os.Exit(95)
		}
		fmt.Fprintln(os.Stdout, "intent-was-durable")
		if mode == "fail" {
			fmt.Fprintln(os.Stderr, "child failed")
			os.Exit(7)
		}
	case "mark":
		if err := os.WriteFile(args[1], []byte("launched"), 0600); err != nil {
			os.Exit(96)
		}
	case "args":
		if err := json.NewEncoder(os.Stdout).Encode(args[1:]); err != nil {
			os.Exit(96)
		}
	case "report":
		if err := os.WriteFile(os.Getenv("DATUM_RUN_REPORT"), []byte(args[1]), 0600); err != nil {
			os.Exit(96)
		}
		if err := os.WriteFile(filepath.Join(os.Getenv("DATUM_RUN_DIR"), "result.json"), []byte(`{"actual":8}`), 0600); err != nil {
			os.Exit(96)
		}
		if err := os.Symlink(os.Getenv("DATUM_RUN_REPORT"), filepath.Join(os.Getenv("DATUM_RUN_DIR"), "link")); err != nil {
			os.Exit(96)
		}
	case "burst":
		if _, err := os.Stdout.Write(bytes.Repeat([]byte("o"), 2<<20)); err != nil {
			os.Exit(96)
		}
		if _, err := os.Stderr.Write(bytes.Repeat([]byte("e"), 2<<20)); err != nil {
			os.Exit(96)
		}
	case "block":
		if err := os.WriteFile(args[1], []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(96)
		}
		time.Sleep(15 * time.Second)
	case "descendant":
		child := exec.Command(os.Args[0], "-test.run=^TestRunChildProcess$", "--", "holdpipe")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(96)
		}
		if err := os.WriteFile(args[1], []byte(strconv.Itoa(child.Process.Pid)), 0600); err != nil {
			os.Exit(96)
		}
		code, _ := strconv.Atoi(args[2])
		os.Exit(code)
	default:
		fmt.Fprintln(os.Stderr, strings.Join(args, " "))
		os.Exit(97)
	}
	os.Exit(0)
}
