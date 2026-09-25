package acceptance_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
	"github.com/alex2481kobe/whosaidso/internal/write"
)

func outsideWriteProject(t *testing.T) store.Project {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	return store.Project{ID: reduceProject, Root: root, Ledger: filepath.Join(root, ".whosaidso", "events")}
}

// There is deliberately no test that admission REFUSES a packet whose author
// is also its admitter. When author and admitter are the same actor, the
// admission is visible and queryable (--self-admitted), never blocked.
//
// The rule exists so self-admitted work can be AUDITED later rather than
// prevented. Blocking it would destroy the very record those audits read.
//
// Two real defects were found around it:
//
//   - admissionReason writes self-admission as PROSE into a reason string
//     ("Self-admitted: true"). The contract requires it to be queryable. You
//     cannot run a self-admitted audit by grepping sentences.
//   - it compares p.Author.ID == r.Admitter.ID guarded by != "", rather than
//     using model.SameActor, which is the function that already gets actor
//     identity right. Two rules for one question is how they drift apart.

func outsideRunRequest(project store.Project, report, gate string) write.RunRequest {
	events, env, _ := outsideProofFixture()
	return write.RunRequest{
		Author: model.Actor{ID: "producer"}, AttemptID: env.AttemptID, InstrumentRef: env.InstrumentRef,
		Instrument: events[2].(*model.InstrumentDeclare).Spec, ExecutionSourceIdentity: env.ExecutionSourceIdentity,
		Argv: []string{os.Args[0], "-test.run=^TestWriteProducerChild$", "--", "outside-producer", report, gate},
		Dir:  project.Root, Timeout: 10 * time.Second,
		ConfigRequested:    map[string]model.Scalar{"sample_count": evidenceNumber("12")},
		ConditionsDeclared: map[string]model.Scalar{"seed": evidenceNumber("7")},
	}
}

const outsideRunReport = `{"version":1,"config_effective":{"sample_count":{"type":"number","number":8}},"conditions_observed":{"seed":{"type":"number","number":9}}}`

func outsideRunPackets(t *testing.T, project store.Project, result write.RunResult) (model.InvocationEnvelope, model.InvocationEnvelope) {
	t.Helper()
	if result.StartPacket.CommandID == "" || result.SealPacket.CommandID == "" {
		t.Fatal("runner must durably capture both the prelaunch intent and terminal observation")
	}
	packets, err := store.ReadIntake(project, []model.ID{result.StartPacket.CommandID, result.SealPacket.CommandID})
	if err != nil || len(packets) != 2 {
		t.Fatalf("durable run packet pair must remain readable: %v", err)
	}
	var start, seal model.InvocationEnvelope
	for _, packet := range packets {
		if len(packet.Events) != 1 {
			t.Fatal("run fixture must capture one event per packet")
		}
		event, err := model.DecodeEvent(packet.Events[0])
		if err != nil {
			t.Fatalf("durable run event must decode: %v", err)
		}
		switch e := event.(type) {
		case *model.InvocationStart:
			start = e.Envelope
		case *model.InvocationSeal:
			seal = e.Envelope
		default:
			t.Fatalf("unexpected run event %T", event)
		}
	}
	if start.InvocationID == "" || start.InvocationID != seal.InvocationID {
		t.Fatal("durable packets must identify the same invocation")
	}
	return start, seal
}

func outsideRunControl(t *testing.T, project store.Project) {
	t.Helper()
	result, err := write.Run(context.Background(), project, outsideRunRequest(project, outsideRunReport, ""))
	if err != nil {
		t.Fatalf("control producer report must execute and seal: %v", err)
	}
	start, seal := outsideRunPackets(t, project, result)
	if start.ConfigEffective.State != model.Unknown || seal.ConfigEffective.Value == nil || seal.ConditionsObserved.Value == nil {
		t.Fatal("control must distinguish unknown prelaunch facts from the producer's observed facts")
	}
	config, condition := (*seal.ConfigEffective.Value)["sample_count"], (*seal.ConditionsObserved.Value)["seed"]
	if config.Value == nil || config.Value.Number == nil || *config.Value.Number != "8" || condition.Value == nil || condition.Value.Number == nil || *condition.Value.Number != "9" {
		t.Fatalf("control must record actual sample_count=8 and seed=9 rather than requested 12 and 7: config=%+v, condition=%+v", config, condition)
	}
}

func TestWriteProducerCaseVariantsCannotMergeContradictoryFacts(t *testing.T) {
	project := outsideWriteProject(t)
	outsideRunControl(t, project)
	// encoding/json matches struct field names case-insensitively. Both keys
	// below target Scalar.Number even though a literal-key duplicate check differs.
	for _, fields := range []string{`"number":8,"NUMBER":99`, `"NUMBER":99,"number":8`} {
		report := strings.Replace(outsideRunReport, `"number":8`, fields, 1)
		result, err := write.Run(context.Background(), project, outsideRunRequest(project, report, ""))
		_, seal := outsideRunPackets(t, project, result)
		if err == nil || seal.ConfigEffective.State != model.Unknown {
			observed := "unknown"
			if seal.ConfigEffective.Value != nil {
				v := (*seal.ConfigEffective.Value)["sample_count"]
				if v.Value != nil && v.Value.Number != nil {
					observed = string(*v.Value.Number)
				}
			}
			t.Errorf("contradictory producer fields {%s} yielded effective sample_count=%s, error=%v. Expected invalid-report refusal and UNKNOWN configuration: two spellings of Scalar.Number must not silently choose a fact by JSON order", fields, observed, err)
		}
	}
}

// Run first checks Err after runIntent has copied caller-owned data, before
// publishing the start packet or launching any producer goroutines or child.
type outsideCopiedIntentContext struct {
	context.Context
	once   sync.Once
	copied chan struct{}
	resume chan struct{}
}

func (ctx *outsideCopiedIntentContext) Err() error {
	ctx.once.Do(func() {
		close(ctx.copied)
		<-ctx.resume
	})
	return ctx.Context.Err()
}

func TestWriteProducerFreezesCallerIntentBeforeLaunch(t *testing.T) {
	project := outsideWriteProject(t)
	outsideRunControl(t, project)
	request := outsideRunRequest(project, outsideRunReport, "")
	ctx := &outsideCopiedIntentContext{
		Context: context.Background(),
		copied:  make(chan struct{}),
		resume:  make(chan struct{}),
	}
	resume := sync.OnceFunc(func() { close(ctx.resume) })
	defer resume() // Also unblock Run if the barrier assertion fails.
	type runAnswer struct {
		result write.RunResult
		err    error
	}
	done := make(chan runAnswer, 1)
	go func() {
		result, err := write.Run(ctx, project, request)
		done <- runAnswer{result, err}
	}()
	select {
	case <-ctx.copied:
	case answer := <-done:
		t.Fatalf("fixture producer exited before its post-copy barrier: %v", answer.err)
	case <-time.After(8 * time.Second):
		t.Fatal("fixture producer never reached its post-copy barrier")
	}
	// Receiving copied orders all copy reads before these writes. Closing resume
	// orders these writes before publication and sealing, even if copying regresses.
	request.ConfigRequested["sample_count"] = evidenceNumber("999")
	request.ConditionsDeclared["seed"] = evidenceNumber("777")
	resume()
	answer := <-done
	if answer.err != nil {
		t.Fatalf("fixture run must complete after caller storage is reused: %v", answer.err)
	}
	start, seal := outsideRunPackets(t, project, answer.result)
	wantConfig := map[string]model.Scalar{"sample_count": evidenceNumber("12")}
	wantConditions := map[string]model.Scalar{"seed": evidenceNumber("7")}
	for _, packet := range []struct {
		name     string
		envelope model.InvocationEnvelope
	}{{"start", start}, {"seal", seal}} {
		if !reflect.DeepEqual(packet.envelope.ConfigRequested, wantConfig) {
			t.Errorf("durable %s must preserve original requested sample_count=12 after caller map reuse: got %+v", packet.name, packet.envelope.ConfigRequested)
		}
		if !reflect.DeepEqual(packet.envelope.ConditionsDeclared, wantConditions) {
			t.Errorf("durable %s must preserve original declared seed=7 after caller map reuse: got %+v", packet.name, packet.envelope.ConditionsDeclared)
		}
	}
}

// Subprocess fixture, not a negative test. All parent tests first run a real
// successful report through this binary and verify the durable packets.
func TestWriteProducerChild(t *testing.T) {
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) != 4 || args[1] != "outside-producer" {
		return
	}
	report, gate := args[2], args[3]
	// A report whose bytes are not valid UTF-8 cannot travel as argv: argv is
	// authored intent, and the encoder refuses invalid UTF-8 there. "hex:" carries such bytes
	// intact. Reports are JSON and begin with "{", so the prefix cannot
	// collide with a real one.
	if rest, ok := strings.CutPrefix(report, "hex:"); ok {
		raw, err := hex.DecodeString(rest)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(93)
		}
		report = string(raw)
	}
	if gate != "" {
		if err := os.WriteFile(gate+".ready", []byte("waiting"), 0600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(91)
		}
		deadline := time.Now().Add(8 * time.Second)
		for {
			if _, err := os.Stat(gate + ".release"); err == nil {
				break
			}
			if time.Now().After(deadline) {
				os.Exit(92)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if err := os.WriteFile(os.Getenv("WHOSAIDSO_RUN_REPORT"), []byte(report), 0600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(93)
	}
	os.Exit(0)
}
