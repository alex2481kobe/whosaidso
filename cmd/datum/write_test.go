package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
	for _, command := range []string{"publish", "append", "run", "decision", "claim"} {
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
	event, err := model.EncodeEvent(&model.TaskStart{Task: model.RecordRef{Project: p.ID, RecordID: cliID(1), Revision: 1}, Actor: model.Actor{ID: "lane"}, AttemptID: cliID(7)})
	if err != nil {
		t.Fatal(err)
	}
	data, err = model.Encode([]model.Event{event})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := callWriteCLI(t, root, data, "capture", "--command-id", string(cliID(5)), "--actor", "lane"); err != nil {
		t.Fatal(err)
	}
	if _, err := callWriteCLI(t, root, nil, "admit", "--command-id", string(cliID(6)), "--actor", "reviewer", "--outcome", "accepted", "--reason", "start checked", string(cliID(5))); err != nil {
		t.Fatal(err)
	}
	return root, p, []string{"handback", "--command-id", string(cliID(8)), "--attempt-id", string(cliID(7)), "--outcome", "stopped", "--reason", "  exact reason\n", "--next-action", "  exact next action\n"}
}

func TestCLIHandbackOutcomesAndAttribution(t *testing.T) {
	for _, name := range []string{"success", "stopped", "refused", "no-reading", "measurement-impossible", "runner-died", "harness-broken", "out-of-scope", "blocked-mid-task", "unknown", "explicit-empty"} {
		t.Run(name, func(t *testing.T) {
			root, p, args := cliHandbackControl(t)
			outcome, author := name, model.Actor{ID: "lane"}
			env := "lane"
			if name == "unknown" || name == "explicit-empty" {
				outcome, author = "stopped", model.Actor{UnknownReason: "no actor supplied by --actor or DATUM_ACTOR"}
				if name == "unknown" {
					env = ""
				} else {
					args = append(args, "--actor", "")
				}
			} else if name == "success" {
				env, args = "wrong-environment-actor", append(args, "--actor", "lane")
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
	for _, flag := range []string{"--outcome", "--reason", "--next-action", "--attempt-id"} {
		for _, value := range []string{"omitted", "", "\u200b", " "} {
			t.Run(flag+"/"+value, func(t *testing.T) {
				args := append([]string(nil), control...)
				for i := 1; i < len(args); i += 2 {
					if args[i] == flag {
						args[i+1] = value
						if value == "omitted" {
							args = append(args[:i], args[i+2:]...)
						}
						break
					}
				}
				out, err := callWriteCLI(t, root, nil, args...)
				if err == nil || len(out) != 0 {
					t.Fatalf("missing authored %s was acknowledged: %s, %v", flag, out, err)
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
}
