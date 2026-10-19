package acceptance_test

// whosaidso check ledger through the built binary: clean only when the ledger
// at HEAD is whole for a fresh clone. Each problem is made on a clean control
// and must flip the answer: bundles left uncommitted, an artifact store left
// uncommitted, a git pin to a commit HEAD cannot reach, and a shallow
// repository. It reads no home: an unusable WHOSAIDSO_HOME does not change
// the answer. Set-aside bundles, resolved and not, are the many-machine
// test's (many_machines_test.go).

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/write"
)

// checkLedger runs whosaidso check ledger --json in dir with extra env.
func checkLedger(t *testing.T, dir, home string, env ...string) (write.LedgerCheck, int) {
	t.Helper()
	cmd := exec.Command(pvWhoSaidSo(t), "check", "ledger", "--json")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), append([]string{"HOME=" + home}, env...)...)
	out, err := cmd.Output()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	var a write.LedgerCheck
	if jerr := json.Unmarshal(out, &a); jerr != nil {
		t.Fatalf("check ledger --json printed %q (exit %d): %v", out, code, jerr)
	}
	return a, code
}

func (w *flowWorld) commitAll(message string, paths ...string) {
	w.t.Helper()
	nestGit(w.t, w.root, append([]string{"add", "--"}, paths...)...)
	nestGit(w.t, w.root, "commit", "-q", "-m", message)
}

func TestCheckLedgerFindsWhatAFreshCloneLacks(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	w := flowNew(t)
	task := w.task("the merged work")
	pvPut(t, w.root, ".gitignore", []byte(nestIgnore))
	nestGit(t, w.root, "init", "-q")
	w.commitAll("ledger", ".gitignore", "whosaidso.toml", ".whosaidso/events")
	clean := func(what string) {
		t.Helper()
		if a, code := checkLedger(t, w.root, w.home); !a.Clean || code != 0 {
			t.Fatalf("%s: want clean with exit 0, got exit %d: %+v", what, code, a)
		}
	}
	clean("control: a committed ledger")

	w.task("work not yet committed")
	if a, code := checkLedger(t, w.root, w.home); a.Clean || code != 1 || len(a.NotCommitted) != 2 {
		t.Fatalf("bundles left out of HEAD must fail the check: exit %d %+v", code, a)
	}
	w.commitAll("second task", ".whosaidso/events")
	clean("the bundles committed")

	body := []byte(`{"ruling":"keep the pose sweep"}`)
	w.put("rulings/keep.json", body)
	ref := pvPin(body, "rulings/keep.json")
	w.mustAdmit("clerk", &model.SourceIntake{SourceID: w.id(), SourceRef: ref, OriginalDigest: ref.Content.SHA256, Length: ref.Content.Length,
		Speaker: model.Actor{ID: "owner"}, Referents: []model.RecordRef{task.ref}})
	w.commitAll("events only", ".whosaidso/events")
	if a, code := checkLedger(t, w.root, w.home); a.Clean || code != 1 || len(a.NotCommitted) != 1 || !strings.Contains(a.NotCommitted[0], string(ref.Content.SHA256)) {
		t.Fatalf("a content artifact present only in the working tree must fail the check: exit %d %+v", code, a)
	}
	w.commitAll("artifacts", ".whosaidso/artifacts")
	clean("the artifacts committed")

	nestGit(t, w.root, "switch", "-q", "-c", "side")
	w.put("witness/side.txt", []byte("made on a branch main never merges\n"))
	w.commitAll("side work", "witness/side.txt")
	side := nestGit(t, w.root, "rev-parse", "HEAD")
	nestGit(t, w.root, "switch", "-q", "main")
	hold := w.id()
	w.mustAdmit(flowAgent, &model.BlockerHold{Task: task.ref, BlockerID: hold, Reason: model.BlockerAwaitingAcceptance,
		Actor: model.Actor{ID: "owner"}, Criterion: "the side witness"})
	w.mustAdmit(flowAgent, &model.BlockerClear{Task: task.ref, BlockerID: hold, HoldRef: model.BlockerRef{Task: task.ref, BlockerID: hold},
		ResolvingWitness: model.ArtifactRef{Kind: "git", Git: &model.GitPin{ObjectFormat: "sha1", Commit: side, Path: "witness/side.txt"}, Selector: model.Selector{Kind: "whole"}}})
	w.commitAll("cleared on a side commit", ".whosaidso/events")
	a, code := checkLedger(t, w.root, w.home)
	if a.Clean || code != 1 || len(a.GitPins) != 1 || !strings.Contains(a.GitPins[0], "HEAD cannot reach") {
		t.Fatalf("a git pin HEAD cannot reach must fail the check: exit %d %+v", code, a)
	}
	nestGit(t, w.root, "merge", "-q", "--no-edit", "side")
	clean("the side branch merged")
	if a, code := checkLedger(t, w.root, w.home, "WHOSAIDSO_HOME=not/absolute"); !a.Clean || code != 0 {
		t.Fatalf("the check reads no home: an unusable WHOSAIDSO_HOME changes nothing, got exit %d %+v", code, a)
	}

	shallow := filepath.Join(t.TempDir(), "shallow")
	nestGit(t, w.root, "clone", "-q", "--depth", "1", "file://"+w.root, shallow)
	cmd := exec.Command(pvWhoSaidSo(t), "check", "ledger")
	cmd.Dir, cmd.Env = shallow, append(os.Environ(), "HOME="+w.home)
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "git fetch --unshallow") {
		t.Fatalf("a shallow repository is refused: %v %s", err, out)
	}
}
