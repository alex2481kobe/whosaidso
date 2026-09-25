package main

// `whosaidso show --stale` (formerly state --stale) through a fresh
// process: the stale-claims section appears only when asked, git runs only
// when asked, and the flag belongs to show alone. The git that show --stale and
// continue are handed is the invoking checkout's, never the home's. What
// staleness is (TRUE, FALSE, UNKNOWN) is tested in internal/query, the git
// observer in internal/evidence.

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

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func TestCLIShowStaleRunsGitOnlyWhenAsked(t *testing.T) {
	root, _, records := disposalWorld(t)
	claim := string(records[0].RecordID)
	// A git that records every call and answers nothing. The fixture is not a
	// checkout, so staleness must come back UNKNOWN with a reason.
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "git-calls")
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho \"$@\" >> '"+log+"'\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	read := func(args ...string) ([]byte, error) {
		command := exec.Command(binary, append([]string{"-test.run=^TestWhoSaidSoMainProcess$", "--"}, args...)...)
		command.Dir = root
		command.Env = append(os.Environ(), "WHOSAIDSO_MAIN_TEST_PROCESS=1", "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		err := command.Run()
		if err != nil {
			return stderr.Bytes(), err
		}
		return stdout.Bytes(), nil
	}
	plain, err := read("show", "--json")
	if err != nil {
		t.Fatalf("control: show must answer: %v %s", err, plain)
	}
	if bytes.Contains(plain, []byte(`"stale"`)) {
		t.Fatal("show without --stale must carry no stale section")
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("show without --stale ran git; reads stay cheap unless asked")
	}
	out, err := read("show", "--stale", "--json")
	if err != nil {
		t.Fatalf("show --stale: %v %s", err, out)
	}
	var answer struct {
		Stale struct {
			BlindSpot string `json:"blind_spot"`
			Claims    []struct {
				Claim struct {
					RecordID string `json:"record_id"`
				} `json:"claim"`
				Stale  string `json:"stale"`
				Reason string `json:"reason"`
			} `json:"claims"`
		} `json:"stale"`
	}
	if err := json.Unmarshal(out, &answer); err != nil {
		t.Fatal(err)
	}
	stale := answer.Stale.Claims
	if len(stale) != 1 || stale[0].Claim.RecordID != claim || stale[0].Stale != "UNKNOWN" || stale[0].Reason == "" {
		t.Fatalf("the observed claim must read stale UNKNOWN with git's reason: %s", out)
	}
	if calls, err := os.ReadFile(log); err != nil || len(calls) == 0 || !strings.Contains(answer.Stale.BlindSpot, "uncommitted changes") {
		t.Fatalf("show --stale must ask git and state its blind spot: %v %q", err, answer.Stale.BlindSpot)
	}
	brief, err := read("show", "--stale")
	if err != nil || !strings.Contains(string(brief), "stale claims: 1\n  CLAIM "+claim+" rev 1 stale UNKNOWN") {
		t.Fatalf("the brief must list the stale section: %v\n%s", err, brief)
	}
	if out, err := read("todo", "--stale"); err == nil {
		t.Fatalf("--stale belongs to show alone, todo accepted it: %s", out)
	}
}

// splitCheckout is a home repository at base and a clone of it, the invoking
// checkout, one commit ahead: a commit the home does not have.
func splitCheckout(t *testing.T) (p store.Project, base, ahead model.GitHead) {
	t.Helper()
	home, checkout := t.TempDir(), filepath.Join(t.TempDir(), "checkout")
	homeGit(t, home, "init", "--quiet")
	proofWrite(t, home, "src/tool.sh", "echo base\n")
	homeGit(t, home, "add", ".")
	homeGit(t, home, "commit", "--quiet", "-m", "base")
	homeGit(t, home, "clone", "--quiet", home, checkout)
	proofWrite(t, checkout, "src/tool.sh", "echo ahead\n")
	homeGit(t, checkout, "commit", "--quiet", "-am", "ahead")
	base = model.GitHead{ObjectFormat: "sha1", Commit: homeGit(t, home, "rev-parse", "HEAD")}
	ahead = model.GitHead{ObjectFormat: "sha1", Commit: homeGit(t, checkout, "rev-parse", "HEAD")}
	if base == ahead {
		t.Fatal("control: the checkout must sit at a commit the home does not")
	}
	return store.Project{ID: "test/cli", Root: home, Ledger: filepath.Join(home, ".whosaidso", "events"), Checkout: checkout}, base, ahead
}

func TestStaleAndContinueObserveTheInvokingCheckoutNotTheHome(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	p, base, ahead := splitCheckout(t)
	ctx := context.Background()
	git := staleGit(ctx, p)
	if git.Head.State != model.Known || *git.Head.Value != ahead {
		t.Fatalf("show --stale must compare against the invoking checkout's HEAD %s, not the home's %s: %+v", ahead.Commit, base.Commit, git.Head)
	}
	if changed, err := git.Changes(base, ahead, []string{"src"}); err != nil || !reflect.DeepEqual(changed, []string{"src/tool.sh"}) {
		t.Fatalf("the scoped diff must be read in the checkout, which holds both commits: %v, %v", changed, err)
	}

	observed := observe(ctx, p)
	if observed.Head.State != model.Known || *observed.Head.Value != ahead {
		t.Fatalf("continue must observe the invoking checkout's HEAD %s: %+v", ahead.Commit, observed.Head)
	}
	if observed.Dirty.State != model.Known || *observed.Dirty.Value || observed.ObservedAt.State != model.Known {
		t.Fatalf("control: the clean checkout is observed clean, at a known time: %+v", observed)
	}
	proofWrite(t, p.Root, "src/tool.sh", "edited in the home\n")
	if observed := observe(ctx, p); observed.Dirty.State != model.Known || *observed.Dirty.Value {
		t.Fatalf("the home's working tree is not the checkout's: %+v", observed.Dirty)
	}
	// Continue reports the dirty state it observed; recording it UNKNOWN is
	// the run's policy, not continue's. The stale read carries no dirty state.
	proofWrite(t, p.Checkout, "src/tool.sh", "edited in the checkout\n")
	if observed := observe(ctx, p); observed.Dirty.State != model.Known || !*observed.Dirty.Value {
		t.Fatalf("continue must report the checkout's observed dirty state: %+v", observed.Dirty)
	}
	if again := staleGit(ctx, p); again.Head.State != model.Known || *again.Head.Value != ahead {
		t.Fatalf("an uncommitted edit does not move the stale read's HEAD: %+v", again.Head)
	}
}
