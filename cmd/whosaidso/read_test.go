package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"whosaidso/internal/model"
	"whosaidso/internal/query"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
)

func readProcess(t *testing.T, root string, input []byte, args ...string) []byte {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, append([]string{"-test.run=^TestWhoSaidSoMainProcess$", "--"}, args...)...)
	command.Dir = root
	command.Env = append(os.Environ(), "WHOSAIDSO_MAIN_TEST_PROCESS=1")
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("fresh CLI %v must succeed, got %v: %s; main dispatch must reach reads", args, err, output)
	}
	return output
}

// readJSON decodes one view's --json answer.
func readJSON[T any](t *testing.T, data []byte) T {
	t.Helper()
	var answer T
	if err := json.Unmarshal(data, &answer); err != nil {
		t.Fatalf("expected explicit JSON answer, got %q: %v", data, err)
	}
	return answer
}

func TestFreshProcessesExplainAdmittedWhoSaidSoConstructionTaskAndSource(t *testing.T) {
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
	body := []byte("Build U09 of WhoSaidSo: the first usable read slice. Go, standard library only. Text and JSON use one structure. Include task revision, attempt holder, blockers, expected next actor, and ledger watermark.")
	path := filepath.Join(root, "u09-request.txt")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	artifact := model.ArtifactRef{Kind: "content", Content: &model.ContentPin{SHA256: model.HashBytes(body), Length: uint64(len(body)),
		MediaType: "text/plain", Locators: []model.Locator{}}, Selector: model.Selector{Kind: "whole"}}
	task.Spec.Intent = "Build U09 of WhoSaidSo: the first usable read slice"
	task.Spec.Subject = "WhoSaidSo construction"
	task.Spec.Scope.SourcePaths = []string{"internal/query/query.go", "cmd/whosaidso/read.go"}
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
	readProcess(t, root, input, "capture", "--command-id", string(cliID(3)), "--actor", "agent", "--blob", path)
	// R19: intake pending is a section of todo.
	pending := readJSON[query.TodoAnswer](t, readProcess(t, root, nil, "todo", "--json"))
	if len(pending.PacketsNotAccepted) != 1 || pending.PacketsNotAccepted[0].Disposition != "pending" || pending.Watermark.Sequence != 0 {
		t.Fatalf("control captured U09 must be visible before admission at watermark 0, got %+v", pending)
	}
	readProcess(t, root, nil, "admit", "--command-id", string(cliID(4)), "--actor", "reviewer", "--outcome", "accepted", "--reason", "admit U09 construction obligation and source", string(cliID(3)))
	showBytes := readProcess(t, root, nil, "show", "--json", string(task.ID))
	show := readJSON[query.ShowAnswer](t, showBytes)
	if len(show.Records) != 1 || show.Records[0].Task.Status != reduce.StatusReady || show.Records[0].Task.Revision != 1 || show.Watermark.Sequence != 1 {
		t.Fatalf("expected admitted U09 READY revision 1 at watermark 1, got %+v", show)
	}
	record := show.Records[0]
	if record.Fact.Task.Intent != task.Spec.Intent || record.Fact.Provenance.SourceRefs[0].Content.SHA256 != artifact.Content.SHA256 || len(record.Sources) != 1 || record.Sources[0].Intake.Speaker.ID != "requesting-owner" {
		t.Fatalf("fresh read must retain U09 intent, source hash and original speaker separately from author/reviewer, got %+v", record)
	}
	// R19: --full is removed; --json is the complete answer.
	text := readProcess(t, root, nil, "show", "--json", string(task.ID))
	if !bytes.Contains(text, []byte(task.Spec.Intent)) || !bytes.Contains(text, []byte(artifact.Content.SHA256)) || !bytes.Contains(text, []byte("watermark")) {
		t.Fatalf("text must explain the same obligation, source and watermark as JSON, got %s", text)
	}
	for _, args := range [][]string{{"history", "--json"}, {"todo", "--json"}, {"show", "--json"}} {
		a := readJSON[query.ViewHeader](t, readProcess(t, root, nil, args...))
		if a.Watermark.Sequence != 1 {
			t.Fatalf("every fresh read command needs watermark 1, got %+v for %v", a.Watermark, args)
		}
	}
	history := readJSON[query.HistoryAnswer](t, readProcess(t, root, nil, "history", "--json", string(task.ID)))
	if len(history.Events) != 2 || history.Events[0].Event.Type != "task.create" || history.Events[1].Event.Type != "source.intake" {
		t.Fatalf("filtered history must include the source and authored task in admitted order, got %+v", history.Events)
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
	claim, err := model.EncodeEvent(&model.ClaimAssert{ID: cliID(30), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}},
		Spec: model.ClaimSpec{Assertion: "the read slice may omit source detail", Falsifier: "inspect its export against this ledger",
			Scope: task.Spec.Scope, ExternalRefs: []model.ExternalReference{}}})
	if err != nil {
		t.Fatal(err)
	}
	finding, err := model.Encode([]model.Event{claim})
	if err != nil {
		t.Fatal(err)
	}
	readProcess(t, root, finding, "capture", "--command-id", string(cliID(31)), "--actor", "agent")
	readProcess(t, root, nil, "admit", "--command-id", string(cliID(32)), "--actor", "reviewer", "--outcome", "accepted", "--reason", "record finding without claiming measurement", string(cliID(31)))
	found := readJSON[query.ShowAnswer](t, readProcess(t, root, nil, "show", "--json", string(cliID(30))))
	if found.Records[0].Claim.Status != reduce.StatusUnmeasured || found.Watermark.Sequence != 2 {
		t.Fatalf("new finding must enter through capture/admit as UNMEASURED CLAIM at watermark 2, got %+v", found)
	}
	readProcess(t, root, finding, "capture", "--command-id", string(cliID(33)), "--actor", "agent")
	readProcess(t, root, nil, "admit", "--command-id", string(cliID(34)), "--actor", "reviewer", "--outcome", "rejected", "--reason", "duplicate assertion", string(cliID(33)))
	rejected := readJSON[query.TodoAnswer](t, readProcess(t, root, nil, "todo", "--json"))
	if len(rejected.PacketsNotAccepted) != 1 || rejected.PacketsNotAccepted[0].Disposition != "rejected" || rejected.Watermark.Sequence != 3 || !strings.Contains(rejected.PacketsNotAccepted[0].Review.Reason, "duplicate assertion") {
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
	if output, _, code := cliRun(t, root, nil, "", "show", "--json", string(cliID(1))); code != 0 || readJSON[query.ShowAnswer](t, []byte(output)).Watermark.Sequence != 1 {
		t.Fatalf("control admitted record read must succeed at watermark 1: %d", code)
	}
	for _, args := range [][]string{{"show", "--bogus"}, {"show", "bad-id"}, {"show", "--kind", "task", string(cliID(1))}, {"show", "--kind", "bogus"},
		{"todo", string(cliID(1))}, {"continue"}, {"history", "--limit", "1"}, {"show", string(cliID(1)), string(cliID(2))}} {
		if output, _, code := cliRun(t, root, nil, "", args...); code != 2 || output != "" {
			t.Fatalf("invalid read %v must be a usage error without an answer, got %d %s", args, code, output)
		}
	}
	for _, args := range [][]string{{"show", "--help"}, {"history", "--help"}, {"todo", "--help"}, {"continue", "--help"}} {
		if _, _, code := cliRun(t, root, nil, "", args...); code != 0 {
			t.Fatalf("read help must succeed for %v: %d", args, code)
		}
	}
	unknown, _, code := cliRun(t, root, nil, "", "show", "--json", string(cliID(99)))
	if code != 0 || readJSON[query.ShowAnswer](t, []byte(unknown)).Result != "UNKNOWN" {
		t.Fatalf("absent valid ID must return a watermarked UNKNOWN answer, got %s, %d", unknown, code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	if code := whosaidso(ctx, []string{"show"}, root, nil, &stdout, &stderr, func(string) string { return "" }); code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), context.Canceled.Error()) {
		t.Fatalf("cancelled read must stop without an answer, got %d %q %q", code, stdout.String(), stderr.String())
	}
	after, err := store.ReadPrefix(project)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("read commands must leave canonical prefix unchanged: %v", err)
	}
}

// R18.2/R19: the replaced verbs and flags are removed with no alias. Each is
// an unknown command or flag (exit 2), never a quiet answer.
func TestRemovedVerbsAndFlagsAreUnreachable(t *testing.T) {
	root, data := cliFixture(t)
	cliControl(t, root, data)
	for _, args := range [][]string{{"now"}, {"state"}, {"instruments"}, {"context"}, {"intake", "pending"}, {"read", "show"},
		{"criterion", "check", "--events", "-"}, {"proof", "check", "--events", "-"}, {"disposal-loss", "--digest", strings.Repeat("a", 64)},
		{"task", "todo"}, {"todo", "--full"}, {"todo", "--brief"}, {"show", "--full"}, {"history", "--brief"}} {
		out, errs, code := cliRun(t, root, nil, "", args...)
		if code != 2 || out != "" || errs == "" {
			t.Fatalf("%v must be refused as a usage error with no answer, got %d %q %q", args, code, out, errs)
		}
	}
}

func TestReadCLISelfAdmissionAudit(t *testing.T) {
	root, _ := cliFixture(t)
	p, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	// C39: the filter answers from the recorded author against the admitter
	// "reviewer" (same, distinct, unknown).
	refs := []model.PacketRef{}
	authors := map[model.ID]model.Actor{}
	captured := map[model.ID]model.Availability[time.Time]{}
	for i, author := range []model.Actor{{ID: "reviewer"}, {ID: "other"}, {UnknownReason: "author not recorded"}} {
		id := cliID(50 + i)
		refs = append(refs, model.PacketRef{CommandID: id, Digest: model.HashBytes([]byte(id))})
		authors[id] = author
		captured[id] = model.Availability[time.Time]{State: model.Unknown, Reason: "not recorded"}
	}
	event, err := model.EncodeEvent(&model.ReviewAdmit{Packets: refs, Outcome: "rejected", Actor: model.Actor{ID: "reviewer"},
		Reason: "per-packet audit control", Authors: authors, CapturedAt: captured, EventPackets: []model.ID{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Transact(context.Background(), p, cliID(60), model.HashBytes([]byte("audit")), func([]model.Bundle) (model.Bundle, error) {
		return model.Bundle{Admitter: model.Actor{ID: "reviewer"}, Packets: []model.PacketRef{}, Events: []model.Event{event}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		flag, state string
		id          model.ID
	}{
		{"--self-admitted", "true", cliID(50)}, {"--self-admitted=true", "true", cliID(50)},
		{"--self-admitted=false", "false", cliID(51)}, {"--self-admitted=unknown", "UNKNOWN", cliID(52)},
	} {
		exported := readProcess(t, root, nil, "history", "--json", tc.flag)
		a := readJSON[query.HistoryAnswer](t, exported)
		if len(a.Reviews) != 1 || a.Reviews[0].Key.CommandID != tc.id || a.Reviews[0].SelfAdmission != tc.state || a.Watermark.Sequence != 1 {
			t.Fatalf("%s selected wrong audit: %+v", tc.flag, a)
		}
		// R19: --full is removed; the default text is the brief of the same --json.
		want, _, err := query.ViewBriefOf(exported)
		if err != nil {
			t.Fatal(err)
		}
		if output := readProcess(t, root, nil, "history", tc.flag); string(output) != want {
			t.Fatalf("CLI formats disagree: %s versus %s", output, want)
		}
	}
	for _, args := range [][]string{
		{"history", "--self-admitted="}, {"history", "--self-admitted=no"}, {"history", "--self-admitted=UNKNOWN"},
		{"history", "--self-admitted", string(cliID(1))}, {"show", "--self-admitted"}, {"todo", "--self-admitted"},
	} {
		if out, _, code := cliRun(t, root, nil, "", args...); code != 2 || out != "" {
			t.Fatalf("invalid filter %v returned %q, %d", args, out, code)
		}
	}
}

func TestFreshProcessInstrumentsOnThisRepositoryShowUnknownValidation(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(cwd, "..", "..")
	bindTestHome(t, root)
	// R19: the instruments preset is show --kind instrument.
	answer := readJSON[query.ShowAnswer](t, readProcess(t, root, nil, "show", "--kind", "instrument", "--json"))
	if answer.Project != "whosaidso/whosaidso" || answer.Watermark.Bundles == 0 || len(answer.Records) == 0 {
		t.Fatalf("instruments must answer from this repository's own ledger, got %+v", answer.ViewHeader)
	}
	// Attention listing is asserted in internal/query; this checks dispatch and rendering.
	for _, r := range answer.Records {
		v := r.Instrument
		if v == nil || v.Validation.State != "KNOWN" && (v.Validation.State != "UNKNOWN" || v.Validation.Reason == "") {
			t.Fatalf("instrument %s validation must be KNOWN, or UNKNOWN with its reason; got %+v", r.Ref.RecordID, v)
		}
	}
	text := readProcess(t, root, nil, "show", "--kind", "instrument")
	if !bytes.Contains(text, []byte("\nattention")) || bytes.Index(text, []byte("\nattention")) > bytes.Index(text, []byte("\ninstruments:")) {
		t.Fatalf("text must list attention before the instrument details, got %s", text)
	}
}
