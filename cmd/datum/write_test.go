package main

import (
	"bytes"
	"context"
	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cliFixture(t *testing.T) (string, []byte) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "datum.toml"), []byte("id = \"test/cli\"\nledger = \".datum/events\"\n"), 0600); err != nil {
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

// callWriteCLI runs a write in process and asks for its full result
// (--json), which these tests decode; the default acknowledgement is tested
// in acks_test.go.
func callWriteCLI(t *testing.T, root string, input []byte, args ...string) ([]byte, error) {
	t.Helper()
	return callCLI(root, input, func(string) string { return "" }, withJSON(args)...)
}

// callCLI runs one command in process; a nonzero exit is an error carrying
// the status and stderr.
func callCLI(root string, input []byte, getenv func(string) string, args ...string) ([]byte, error) {
	var output, diagnostic bytes.Buffer
	if code := datum(context.Background(), args, root, bytes.NewReader(input), &output, &diagnostic, getenv); code != 0 {
		return output.Bytes(), fmt.Errorf("exit status %d: %s", code, diagnostic.String())
	}
	if diagnostic.Len() != 0 {
		return output.Bytes(), fmt.Errorf("stderr: %s", diagnostic.String())
	}
	return output.Bytes(), nil
}

// acknowledged reports whether a failed write printed anything but the JSON
// error object --json prints for a failure.
func acknowledged(out []byte) bool {
	var e struct {
		Error *struct{ Code, Message string } `json:"error"`
	}
	return len(out) != 0 && (json.Unmarshal(out, &e) != nil || e.Error == nil)
}

// withJSON adds --json after a write verb, so the test reads the full result.
func withJSON(args []string) []string {
	switch args[0] {
	case "capture", "admit", "handback", "run", "reconcile":
		return append([]string{args[0], "--json"}, args[1:]...)
	}
	return args
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
		if out, errs, code := cliRun(t, root, nil, "", command); code != 2 || out != "" || !strings.Contains(errs, "unknown command") {
			t.Fatalf("unknown command %s must be a usage error: %d %q %q", command, code, out, errs)
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
		env := func(string) string { return "from-environment" }
		args := []string{"capture", "--command-id", string(cliID(21 + i)), "--blob", blob}
		if i == 1 {
			args = append(args, "--actor", "")
		}
		out, err := callCLI(root, data, env, withJSON(args)...)
		if err != nil {
			t.Fatal(err)
		}
		var packet model.PacketRef
		if err := json.Unmarshal(out, &packet); err != nil {
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
