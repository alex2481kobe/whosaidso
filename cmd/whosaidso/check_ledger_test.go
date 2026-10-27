package main

// Check-ledger comparison is exercised through the CLI using independent
// synthetic checkouts. The comparison policy itself belongs to internal/write.

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
	"github.com/alex2481kobe/whosaidso/internal/write"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLedgerComparisonReportsCountsIdentityChangesAndUnknownSides(t *testing.T) {
	head, data := cliFixture(t)
	setup := func(root string) {
		t.Helper()
		for _, dir := range []string{"events", "artifacts"} {
			if err := os.MkdirAll(filepath.Join(root, ".whosaidso", dir), 0755); err != nil {
				t.Fatal(err)
			}
		}
	}
	setup(head)
	homeGit(t, head, "init", "-q")
	homeGit(t, head, "add", "whosaidso.toml")
	homeGit(t, head, "commit", "-q", "-m", "config")
	base := filepath.Join(t.TempDir(), "base")
	homeGit(t, head, "clone", "-q", head, base)
	setup(base)
	compare := func(jsonOutput bool) (string, string, int) {
		args := []string{"check", "ledger", "--against-checkout", base}
		if jsonOutput {
			args = append(args, "--json")
		}
		return cliRun(t, head, nil, "", args...)
	}
	if out, errs, code := compare(false); code != 0 || !strings.Contains(out, "not_committed: 0 -> 0") {
		t.Fatalf("clean control: %d %s %s", code, out, errs)
	}
	admit := func(root, id, admit string) {
		t.Helper()
		if out, errs, code := cliRun(t, root, data, "agent", "capture", "--admit", "--reason", "fixture", "--command-id", id, "--admit-command-id", admit); code != 0 {
			t.Fatalf("capture: %d %s %s", code, out, errs)
		}
	}
	admit(head, string(cliID(30)), string(cliID(31)))
	if out, errs, code := compare(false); code != 1 || !strings.Contains(errs, "refused: not_committed increased: 0 -> 1") || !strings.Contains(out, "added:") {
		t.Fatalf("increase: %d %s %s", code, out, errs)
	}
	admit(base, string(cliID(40)), string(cliID(41)))
	out, errs, code := compare(true)
	var answer struct {
		Allowed bool                                          `json:"allowed"`
		Classes map[string]struct{ Added, Resolved []string } `json:"classes"`
	}
	if err := json.Unmarshal([]byte(out), &answer); err != nil {
		t.Fatal(err, out)
	}
	change := answer.Classes["not_committed"]
	if code != 0 || !answer.Allowed || len(change.Added) != 1 || len(change.Resolved) != 1 || change.Added[0] == change.Resolved[0] {
		t.Fatalf("equal counts must expose replacements: %d %s %s", code, out, errs)
	}
	if out, errs, code := compare(false); code != 0 || !strings.Contains(out, "added:") || !strings.Contains(out, "resolved:") {
		t.Fatalf("text replacements: %d %s %s", code, out, errs)
	}
	// Each side independently loses measurement. A replay error is not a zero.
	for _, side := range []struct{ name, root string }{{"base", base}, {"head", head}} {
		files, err := filepath.Glob(filepath.Join(side.root, ".whosaidso/events/*.json"))
		if err != nil || len(files) != 1 {
			t.Fatalf("fixture bundles: %v %v", files, err)
		}
		original, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(files[0], []byte("broken bundle"), 0600); err != nil {
			t.Fatal(err)
		}
		out, errs, code := compare(true)
		if code != 1 || !strings.Contains(errs, "refused: set_asides unmeasured at "+side.name) || !strings.Contains(out, `"unmeasured"`) {
			t.Errorf("unknown %s: %d %s %s", side.name, code, out, errs)
		}
		if err := os.WriteFile(files[0], original, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if out, _, code := cliRun(t, head, nil, "", "help", "check", "ledger"); code != 0 || !strings.Contains(out, "--against-checkout") || !strings.Contains(out, "unmeasured") || !strings.Contains(out, "identities") {
		t.Fatalf("help lacks additive contract: %d %s", code, out)
	}
}

func TestLedgerPinLocationTextDistinguishesRefsNoneAndUnmeasured(t *testing.T) {
	a := &write.LedgerCheck{GitPins: []string{"pin remains a problem"}, GitPinLocations: []write.GitPinLocation{
		{Commit: "side", Refs: []string{"refs/heads/side", "refs/remotes/origin/side"}},
		{Commit: "dangling", Refs: []string{}},
		{Commit: "unreadable", Refs: []string{}, Error: "query failed"},
	}}
	for _, text := range []string{ledgerText(a), ledgerComparisonText(&write.LedgerComparison{Base: a, Head: a})} {
		for _, want := range []string{"pin locations side:", "refs/heads/side", "refs/remotes/origin/side", "no local or remote-tracking refs contain it", "unmeasured: query failed"} {
			if !strings.Contains(text, want) {
				t.Errorf("text lacks %q: %s", want, text)
			}
		}
	}
}

// ledgerPinCheckout keeps a valid pin on a side branch outside HEAD. The
// known problem provides the equal-count control for failed ancestry reads.
func ledgerPinCheckout(t *testing.T) (string, string) {
	t.Helper()
	root, data := cliFixture(t)
	homeGit(t, root, "init", "-q")
	homeGit(t, root, "add", "whosaidso.toml")
	homeGit(t, root, "commit", "-q", "-m", "config")
	main := homeGit(t, root, "branch", "--show-current")
	homeGit(t, root, "switch", "-q", "-c", "side")
	proofWrite(t, root, "witness.txt", "pinned bytes")
	homeGit(t, root, "add", "witness.txt")
	homeGit(t, root, "commit", "-q", "-m", "side")
	pin := homeGit(t, root, "rev-parse", "HEAD")
	homeGit(t, root, "switch", "-q", main)
	var events []model.Event
	if err := json.Unmarshal(data, &events); err != nil {
		t.Fatal(err)
	}
	event, err := model.DecodeEvent(events[0])
	if err != nil {
		t.Fatal(err)
	}
	task := event.(*model.TaskCreate)
	task.Provenance.SourceRefs = []model.ArtifactRef{{Kind: "git", Git: &model.GitPin{ObjectFormat: "sha1", Commit: pin, Path: "witness.txt"}, Selector: model.Selector{Kind: "whole"}}}
	raw, err := model.EncodeEvent(task)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Transact(context.Background(), project, cliID(70), model.HashBytes([]byte("pin fixture")), func([]model.Bundle) (model.Bundle, error) {
		return model.Bundle{Admitter: model.Actor{ID: "reviewer"}, Packets: []model.PacketRef{}, Events: []model.Event{raw}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	homeGit(t, root, "add", ".whosaidso/events")
	homeGit(t, root, "commit", "-q", "-m", "pin")
	return root, pin
}

func TestLedgerComparisonRefusesOperationalAncestryFailure(t *testing.T) {
	head, pin := ledgerPinCheckout(t)
	base := filepath.Join(t.TempDir(), "base")
	homeGit(t, head, "clone", "-q", "--no-hardlinks", head, base)
	compare := func(root, against string) (string, string, int) {
		return cliRun(t, root, nil, "", "check", "ledger", "--against-checkout", against, "--json")
	}
	out, errs, code := compare(head, base)
	if code != 0 {
		t.Fatalf("equal known problems must compare: %d %s %s", code, out, errs)
	}
	var control write.LedgerComparison
	if err := json.Unmarshal([]byte(out), &control); err != nil {
		t.Fatal(err)
	}
	if *control.Base.Classes["git_pins"].Count != 1 || *control.Head.Classes["git_pins"].Count != 1 {
		t.Fatalf("one unreachable pin per side: %s", out)
	}
	// Remove the shared parent's loose object only in head's independent clone.
	// Both tips still exist, but answering ancestry must traverse the missing parent.
	parent := homeGit(t, head, "rev-parse", pin+"^")
	if err := os.Remove(filepath.Join(head, ".git", "objects", parent[:2], parent[2:])); err != nil {
		t.Fatal(err)
	}
	broken := homeGit(t, head, "rev-parse", "HEAD")
	// Prove this is an operational failure, rather than an ordinary negative.
	diagnostic, err := exec.Command("git", "-C", head, "merge-base", "--is-ancestor", pin, broken).CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() == 0 || !strings.Contains(string(diagnostic), "Could not read") {
		t.Fatalf("fixture must produce an operational Git error: %v %s", err, diagnostic)
	}
	for _, side := range []string{"head", "base"} {
		root, against := head, base
		if side == "base" {
			root, against = base, head
		}
		out, errs, code = compare(root, against)
		var a write.LedgerComparison
		if err := json.Unmarshal([]byte(out), &a); err != nil {
			t.Fatal(err, out)
		}
		unknown := a.Head
		if side == "base" {
			unknown = a.Base
		}
		c := unknown.Classes["git_pins"]
		if code != 1 || a.Allowed || c.State != "unmeasured" || c.Count != nil || unknown.Clean || !strings.Contains(errs, "refused: git_pins unmeasured at "+side) {
			t.Errorf("operational error is not a matching pin count at %s: %d %s %s", side, code, out, errs)
		}
		if unknown.Classes["not_committed"].State != "measured" {
			t.Errorf("independent class lost its measurement: %+v", unknown.Classes)
		}
	}
}

func TestLedgerCheckTreeUsesCapturedCommit(t *testing.T) {
	root, _ := ledgerPinCheckout(t)
	trace := filepath.Join(t.TempDir(), "trace.jsonl")
	t.Setenv("GIT_TRACE2_EVENT", trace)
	out, errs, code := cliRun(t, root, nil, "", "check", "ledger", "--json")
	if code != 1 {
		t.Fatalf("known pin problem: %d %s %s", code, out, errs)
	}
	var a write.LedgerCheck
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e struct {
			Event string
			Argv  []string
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		if e.Event != "start" {
			continue
		}
		for _, arg := range e.Argv {
			if arg == "ls-tree" {
				found = true
				args := " " + strings.Join(e.Argv, " ") + " "
				if !strings.Contains(args, " "+a.Commit+" ") || strings.Contains(args, " HEAD ") {
					t.Fatalf("tree query must use captured %s: %v", a.Commit, e.Argv)
				}
			}
		}
	}
	if !found {
		t.Fatal("no tree query observed")
	}
}
