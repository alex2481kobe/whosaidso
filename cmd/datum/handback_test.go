package main

// This file holds the handback CLI tests and their control fixture. Capture
// and admit tests stay in write_test.go; run and proof tests live in
// run_test.go.

import (
	"bytes"
	"context"
	"datum/internal/model"
	"datum/internal/store"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

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
			if err := writeCLI(context.Background(), withJSON(args), root, bytes.NewReader(refs), &out, &diagnostic, func(string) string { return env }); err != nil || diagnostic.Len() != 0 {
				t.Fatalf("handback failed: %v; stderr=%s", err, &diagnostic)
			}
			var receipt model.PacketRef
			if err := json.Unmarshal(out.Bytes(), &receipt); err != nil || receipt.CommandID != cliID(8) || receipt.Digest == "" {
				t.Fatalf("expected packet JSON: %s, %v", &out, err)
			}
			var retry bytes.Buffer
			if err := writeCLI(context.Background(), withJSON(args), root, bytes.NewReader(refs), &retry, &diagnostic, func(string) string { return env }); err != nil || !bytes.Equal(out.Bytes(), retry.Bytes()) {
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

// The usage names every outcome on a line of its own, in the order README's
// "Using it" defines them, so the help an agent reads matches the manual.
func TestHandbackUsageDefinesEveryOutcomeLikeTheReadme(t *testing.T) {
	outcomes := []model.AttemptOutcome{model.AttemptSuccess, model.AttemptStopped, model.AttemptRefused, model.AttemptNoReading,
		model.AttemptMeasurementImpossible, model.AttemptRunnerDied, model.AttemptHarnessBroken, model.AttemptOutOfScope, model.AttemptBlockedMidTask}
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	var usageOrder, readmeOrder []string
	for _, line := range strings.Split(strings.SplitN(writeUsage, "OUTCOME is one of nine", 2)[1], "\n") {
		if fields := strings.Fields(line); strings.HasPrefix(line, "  ") && len(fields) > 1 {
			usageOrder = append(usageOrder, fields[0])
		}
	}
	for _, line := range strings.Split(string(readme), "\n") {
		if strings.HasPrefix(line, "- `") && strings.Contains(line, "`:") {
			readmeOrder = append(readmeOrder, strings.SplitN(strings.TrimPrefix(line, "- `"), "`", 2)[0])
		}
	}
	want := make([]string, len(outcomes))
	for i, o := range outcomes {
		want[i] = string(o)
	}
	if !reflect.DeepEqual(usageOrder, want) || !reflect.DeepEqual(readmeOrder, want) {
		t.Fatalf("usage lines %v and README bullets %v must each define the nine outcomes %v, one per line", usageOrder, readmeOrder, want)
	}
}
