package acceptance_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/store"
	"datum/internal/write"
)

func outsideWriteProject(t *testing.T) store.Project {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	return store.Project{ID: laneEReduceProject, Root: root, Ledger: filepath.Join(root, "record", "events")}
}

// TestWriteAdmissionCannotAcceptItsOwnAuthorsPacket was removed by the
// coordinator, not by the reviewer who wrote it, and the reason is recorded
// here rather than in a commit nobody will read again.
//
// It asserted that admission must REFUSE a packet whose author is also its
// admitter. The contract says the opposite, in the owner's own words:
//
//	DATUM-CONTRACT.md:407  "they are the same actor ... is visible and
//	queryable (--self-admitted), never blocked"
//	DATUM-CONTRACT.md:804  "A lane never admits its own; when author and
//	admitter match it is visible as --self-admitted, not blocked."
//
// The rule exists so self-admitted work can be AUDITED later rather than
// prevented. Blocking it would destroy the very record those audits read.
//
// The reviewer was not wrong to test it; it tested the rule I gave it, and the
// rule I gave it contradicted a ruling the owner had already made. The fault
// is in the brief.
//
// Two real defects surfaced underneath it, and both are open:
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
		ConfigRequested:    map[string]model.Scalar{"sample_count": laneEEvidenceNumber("12")},
		ConditionsDeclared: map[string]model.Scalar{"seed": laneEEvidenceNumber("7")},
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

func TestWriteProducerFreezesCallerIntentBeforeLaunch(t *testing.T) {
	project := outsideWriteProject(t)
	outsideRunControl(t, project)
	gate := filepath.Join(project.Root, "child-gate")
	request := outsideRunRequest(project, outsideRunReport, gate)
	type runAnswer struct {
		result write.RunResult
		err    error
	}
	done := make(chan runAnswer, 1)
	go func() {
		result, err := write.Run(context.Background(), project, request)
		done <- runAnswer{result, err}
	}()
	// The child is alive and waiting, so the intent packet is already durable
	// and the producer cannot be sealing while caller-owned maps are reused.
	deadline := time.Now().Add(8 * time.Second)
	for {
		if _, err := os.Stat(gate + ".ready"); err == nil {
			break
		}
		select {
		case answer := <-done:
			t.Fatalf("fixture producer exited before announcing its pre-seal barrier: %v", answer.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture producer never reached its pre-seal barrier")
		}
		time.Sleep(5 * time.Millisecond)
	}
	request.ConfigRequested["sample_count"] = laneEEvidenceNumber("999")
	request.ConditionsDeclared["seed"] = laneEEvidenceNumber("777")
	if err := os.WriteFile(gate+".release", []byte("continue"), 0600); err != nil {
		t.Fatal(err)
	}
	answer := <-done
	if answer.err != nil {
		t.Fatalf("fixture run must complete after caller storage is reused: %v", answer.err)
	}
	start, seal := outsideRunPackets(t, project, answer.result)
	if !reflect.DeepEqual(start.ConfigRequested, seal.ConfigRequested) || !reflect.DeepEqual(start.ConditionsDeclared, seal.ConditionsDeclared) {
		t.Fatalf("durable start requested sample_count=%s, seed=%s; durable seal repeats sample_count=%s, seed=%s after caller map reuse. Expected the original intent in both packets: unmarshaling into the existing envelope reused caller-owned maps instead of freezing them before launch", *start.ConfigRequested["sample_count"].Number, *start.ConditionsDeclared["seed"].Number, *seal.ConfigRequested["sample_count"].Number, *seal.ConditionsDeclared["seed"].Number)
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
	if err := os.WriteFile(os.Getenv("DATUM_RUN_REPORT"), []byte(report), 0600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(93)
	}
	os.Exit(0)
}
