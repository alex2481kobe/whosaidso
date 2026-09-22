package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"datum/internal/model"
	"datum/internal/query"
	"datum/internal/reduce"
	"datum/internal/store"
)

func readProcess(t *testing.T, root string, input []byte, args ...string) []byte {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, append([]string{"-test.run=^TestDatumMainProcess$", "--"}, args...)...)
	command.Dir = root
	command.Env = append(os.Environ(), "DATUM_MAIN_TEST_PROCESS=1")
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("fresh CLI %v must succeed, got %v: %s; main dispatch must reach reads", args, err, output)
	}
	return output
}

func readJSON(t *testing.T, data []byte) query.Answer {
	t.Helper()
	var answer query.Answer
	if err := json.Unmarshal(data, &answer); err != nil {
		t.Fatalf("expected explicit JSON answer, got %q: %v", data, err)
	}
	return answer
}

func TestFreshProcessesExplainAdmittedDatumConstructionTaskAndSource(t *testing.T) {
	root, initial := cliFixture(t)
	var events []model.Event
	if err := json.Unmarshal(initial, &events); err != nil {
		t.Fatal(err)
	}
	typed, err := model.DecodeEvent(events[0])
	if err != nil {
		t.Fatal(err)
	}
	task := typed.(*model.TaskCreate)
	// This is the actual U09 obligation, admitted through the real gate in an
	// isolated project. It does not claim to complete the user's canonical task.
	body := []byte("Build U09 of Datum: the first usable read slice. Go, standard library only. Text and JSON use one structure. Include task revision, attempt holder, blockers, expected next actor, and ledger watermark.")
	path := filepath.Join(root, "u09-request.txt")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	artifact := model.ArtifactRef{Kind: "content", Content: &model.ContentPin{SHA256: model.HashBytes(body), Length: uint64(len(body)),
		MediaType: "text/plain", Locators: []model.Locator{}}, Selector: model.Selector{Kind: "whole"}}
	task.Spec.Intent = "Build U09 of Datum: the first usable read slice"
	task.Spec.Subject = "Datum construction"
	task.Spec.Scope.SourcePaths = []string{"internal/query/query.go", "cmd/datum/read.go"}
	task.Spec.AcceptanceCriteria[0].Criterion = "a fresh process explains this task and its source"
	task.Provenance.SourceRefs = []model.ArtifactRef{artifact}
	create, err := model.EncodeEvent(task)
	if err != nil {
		t.Fatal(err)
	}
	source, err := model.EncodeEvent(&model.SourceIntake{SourceID: cliID(20), OriginalDigest: artifact.Content.SHA256,
		Length: uint64(len(body)), SourceRef: artifact, Speaker: model.Actor{ID: "requesting-owner"},
		Referents: []model.RecordRef{{Project: "test/cli", RecordID: task.ID, Revision: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	input, err := model.Encode([]model.Event{create, source})
	if err != nil {
		t.Fatal(err)
	}
	readProcess(t, root, input, "capture", "--command-id", string(cliID(3)), "--actor", "lane", "--blob", path)
	pending := readJSON(t, readProcess(t, root, nil, "intake", "pending", "--json"))
	if len(pending.Intake) != 1 || pending.Intake[0].Disposition != "pending" || pending.Watermark.Sequence != 0 {
		t.Fatalf("control captured U09 must be visible before admission at watermark 0, got %+v", pending)
	}
	readProcess(t, root, nil, "admit", "--command-id", string(cliID(4)), "--actor", "reviewer", "--outcome", "accepted", "--reason", "admit U09 construction obligation and source", string(cliID(3)))
	showBytes := readProcess(t, root, nil, "show", "--json", string(task.ID))
	show := readJSON(t, showBytes)
	if len(show.Records) != 1 || show.Records[0].Task.Status != reduce.StatusReady || show.Records[0].Task.Revision != 1 || show.Watermark.Sequence != 1 {
		t.Fatalf("expected admitted U09 READY revision 1 at watermark 1, got %+v", show)
	}
	record := show.Records[0]
	if record.Fact.Task.Intent != task.Spec.Intent || record.Fact.Provenance.SourceRefs[0].Content.SHA256 != artifact.Content.SHA256 || len(record.Sources) != 1 || record.Sources[0].Intake.Speaker.ID != "requesting-owner" {
		t.Fatalf("fresh read must retain U09 intent, source hash and original speaker separately from author/reviewer, got %+v", record)
	}
	text := readProcess(t, root, nil, "show", string(task.ID))
	if !bytes.Contains(text, []byte(task.Spec.Intent)) || !bytes.Contains(text, []byte(artifact.Content.SHA256)) || !bytes.Contains(text, []byte("watermark")) {
		t.Fatalf("text must explain the same obligation, source and watermark as JSON, got %s", text)
	}
	for _, args := range [][]string{{"history", "--json"}, {"task", "todo", "--json"}, {"intake", "pending", "--json"}} {
		a := readJSON(t, readProcess(t, root, nil, args...))
		if a.Watermark.Sequence != 1 {
			t.Fatalf("every fresh read command needs watermark 1, got %+v for %v", a.Watermark, args)
		}
	}
	history := readJSON(t, readProcess(t, root, nil, "history", "--json", string(task.ID)))
	if len(history.History) != 2 || history.History[0].Event.Type != "task.create" || history.History[1].Event.Type != "source.intake" {
		t.Fatalf("filtered history must include the source and authored task in admitted order, got %+v", history.History)
	}
	generated := filepath.Join(root, "generated-read.txt")
	if err := os.WriteFile(generated, text, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(generated); err != nil {
		t.Fatal(err)
	}
	if after := readProcess(t, root, nil, "show", "--json", string(task.ID)); !bytes.Equal(showBytes, after) {
		t.Fatalf("deleting generated output changed a fresh process's answer: before %s after %s", showBytes, after)
	}
	claim, err := model.EncodeEvent(&model.ClaimAssert{ID: cliID(30), Provenance: model.Provenance{Author: model.Actor{ID: "lane"}, SourceRefs: []model.ArtifactRef{}},
		Spec: model.ClaimSpec{Assertion: "the read slice may omit source detail", Falsifier: "inspect its export against this ledger",
			Scope: task.Spec.Scope, ExternalRefs: []model.ExternalReference{}}})
	if err != nil {
		t.Fatal(err)
	}
	finding, err := model.Encode([]model.Event{claim})
	if err != nil {
		t.Fatal(err)
	}
	readProcess(t, root, finding, "capture", "--command-id", string(cliID(31)), "--actor", "lane")
	readProcess(t, root, nil, "admit", "--command-id", string(cliID(32)), "--actor", "reviewer", "--outcome", "accepted", "--reason", "record finding without claiming measurement", string(cliID(31)))
	found := readJSON(t, readProcess(t, root, nil, "show", "--json", string(cliID(30))))
	if found.Records[0].Claim.Status != reduce.StatusUnmeasured || found.Watermark.Sequence != 2 {
		t.Fatalf("new finding must enter through capture/admit as UNMEASURED CLAIM at watermark 2, got %+v", found)
	}
	readProcess(t, root, finding, "capture", "--command-id", string(cliID(33)), "--actor", "lane")
	readProcess(t, root, nil, "admit", "--command-id", string(cliID(34)), "--actor", "reviewer", "--outcome", "rejected", "--reason", "duplicate assertion", string(cliID(33)))
	rejected := readJSON(t, readProcess(t, root, nil, "intake", "pending", "--json"))
	if len(rejected.Intake) != 1 || rejected.Intake[0].Disposition != "rejected" || rejected.Watermark.Sequence != 3 || !strings.Contains(rejected.Intake[0].Review.Reason, "duplicate assertion") {
		t.Fatalf("rejected finding must remain explainable at watermark 3, got %+v", rejected)
	}
}

func TestReadCLIConventionsErrorsAndNoCanonicalWrites(t *testing.T) {
	root, data := cliFixture(t)
	cliControl(t, root, data)
	project, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.ReadPrefix(project)
	if err != nil {
		t.Fatal(err)
	}
	call := func(args ...string) ([]byte, error) {
		var output bytes.Buffer
		err := readCLI(context.Background(), args, root, &output, io.Discard)
		return output.Bytes(), err
	}
	if output, err := call("show", "--json", string(cliID(1))); err != nil || readJSON(t, output).Watermark.Sequence != 1 {
		t.Fatalf("control admitted record read must succeed at watermark 1: %v", err)
	}
	for _, args := range [][]string{{"task"}, {"task", "done"}, {"intake"}, {"intake", "all"}, {"show", "--bogus"},
		{"show", "bad-id"}, {"show", string(cliID(1)), "--json"}, {"task", "todo", string(cliID(1))}, {"read", "show"}} {
		if output, err := call(args...); err == nil || len(output) != 0 {
			t.Fatalf("invalid read %v must fail without an answer, got %s, %v; flag conventions match writes", args, output, err)
		}
	}
	for _, args := range [][]string{{"read", "--help"}, {"show", "--help"}, {"history", "--help"}, {"task", "todo", "--help"}, {"intake", "pending", "--help"}} {
		if _, err := call(args...); err != nil {
			t.Fatalf("read help must succeed for %v: %v", args, err)
		}
	}
	unknown, err := call("show", "--json", string(cliID(99)))
	if err != nil || readJSON(t, unknown).Result != "UNKNOWN" {
		t.Fatalf("absent valid ID must return a watermarked UNKNOWN answer, got %s, %v", unknown, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := readCLI(ctx, []string{"show"}, root, io.Discard, io.Discard); err != context.Canceled {
		t.Fatalf("cancelled read must stop, got %v", err)
	}
	after, err := store.ReadPrefix(project)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("read commands must leave canonical prefix unchanged: %v", err)
	}
	if output := readProcess(t, root, nil, "read", "--help"); !bytes.Contains(output, []byte("datum intake pending")) {
		t.Fatalf("read commands must be discoverable without changing write help, got %s", output)
	}
}
