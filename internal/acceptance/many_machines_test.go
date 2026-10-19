package acceptance_test

// Many agents on several machines, one ledger, through the built binary and
// real git: one origin, three machines (each its own clone and home), and on
// the first machine a second profile (its own home) working in a linked
// worktree. It follows the protocol in whosaidso help branches and checks:
//   - two machines claim one task at once: the first push wins, the loser's
//     push is rejected, and after the merge check claim says LOST (exit 1),
//     todo names the lost claim with its resolve command, and resolving it
//     leaves check ledger clean;
//   - one owner message captured in two places becomes a set-aside bundle at
//     the merge, check ledger fails until it is resolved;
//   - a departed holder's claim is released on the owner's word by another
//     machine, the old claim reads ENDED there after a sync, and a third
//     machine claims the task again;
//   - todo's board names each holder as ACCOUNT/MACHINE/ROLE, and every
//     machine reads the same ledger after syncing.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/query"
)

// machine is one checkout with its own home: a clone, or a worktree of one.
func machine(t *testing.T, root string, ids int, scope model.Scope) *flowWorld {
	return &flowWorld{t: t, root: root, home: t.TempDir(), project: "flow/review", n: ids, scope: scope}
}

// tryGit runs git in dir and returns its error instead of failing the test.
func tryGit(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=m", "-c", "user.email=m@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %v: %s", args, err, out)
	}
	return nil
}

// publish commits the ledger and pushes it to origin's main; a rejected push
// is returned, not fatal.
func (w *flowWorld) publish(message string) error {
	w.t.Helper()
	nestGit(w.t, w.root, "add", "--", ".whosaidso")
	nestGit(w.t, w.root, "commit", "-q", "-m", message)
	return tryGit(w.root, "push", "-q", "origin", "HEAD:main")
}

// sync merges origin's main, as help branches says: a merge, never a rebase.
func (w *flowWorld) sync() {
	w.t.Helper()
	nestGit(w.t, w.root, "fetch", "-q", "origin")
	nestGit(w.t, w.root, "merge", "-q", "--no-edit", "origin/main")
}

func (w *flowWorld) claimAs(actor string, task model.RecordRef) model.ID {
	w.t.Helper()
	attempt := w.id()
	w.mustAdmit(actor, &model.TaskStart{Task: task, Actor: model.Actor{ID: actor}, AttemptID: attempt})
	return attempt
}

func (w *flowWorld) todo() query.TodoAnswer {
	w.t.Helper()
	out, err := w.cli(nil, "todo", "--json")
	var a query.TodoAnswer
	if err != nil || json.Unmarshal(out, &a) != nil {
		w.t.Fatalf("todo --json in %s: %v", w.root, err)
	}
	return a
}

// resolveAll records a setaside.resolve for every unresolved set-aside
// bundle, with the command todo prints.
func (w *flowWorld) resolveAll(actor, reason string) {
	w.t.Helper()
	for _, v := range w.todo().SetAside {
		want := fmt.Sprintf("setaside.resolve --set bundle.sequence=%d --set bundle.command_id=%s", v.Sequence, v.CommandID)
		if !strings.Contains(v.Resolve, want) {
			w.t.Fatalf("todo's resolve command must name the bundle: %q", v.Resolve)
		}
		if _, err := w.cli(nil, "template", "setaside.resolve", "--actor", actor, "--set", fmt.Sprintf("bundle.sequence=%d", v.Sequence),
			"--set", "bundle.command_id="+string(v.CommandID), "--set", "reason="+reason, "--capture", "--admit", "--reason", reason); err != nil {
			w.t.Fatalf("the printed resolve command must admit: %v", err)
		}
	}
}

func ownerMessage(w *flowWorld, file string, order uint64, task model.RecordRef) {
	w.t.Helper()
	body := []byte(`{"owner":"` + file + `"}`)
	w.put(file, body)
	ref := pvPin(body, file)
	w.mustAdmit("biz1/m1/clerk", &model.SourceIntake{SourceID: w.id(), SourceRef: ref, OriginalDigest: ref.Content.SHA256, Length: ref.Content.Length,
		Speaker: model.Actor{ID: "owner"}, Order: order, Referents: []model.RecordRef{task}})
}

func TestManyMachinesShareOneLedger(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	base := t.TempDir()
	nestGit(t, base, "init", "-q", "--bare", "origin.git")
	origin := filepath.Join(base, "origin.git")

	m1 := flowNew(t)
	m1.n = 100
	m1.task("render the hall") // a claim held from the start
	pvPut(t, m1.root, ".gitignore", []byte(nestIgnore))
	nestGit(t, m1.root, "init", "-q")
	nestGit(t, m1.root, "add", "--", ".gitignore", "whosaidso.toml", ".whosaidso")
	nestGit(t, m1.root, "commit", "-q", "-m", "ledger")
	nestGit(t, m1.root, "remote", "add", "origin", origin)
	nestGit(t, m1.root, "push", "-q", "-u", "origin", "main")
	clone := func(name string, ids int) *flowWorld {
		dir := filepath.Join(base, name)
		nestGit(t, base, "clone", "-q", origin, name)
		return machine(t, dir, ids, m1.scope)
	}
	m2, m3 := clone("m2", 400), clone("m3", 600)
	profileB := filepath.Join(base, "m1-profile-b")
	nestGit(t, m1.root, "worktree", "add", "-q", "-b", "lane-b", profileB)
	m1b := machine(t, profileB, 800, m1.scope)

	// A new task is the race.
	floor := m1.createTask("tile the floor")
	if err := m1.publish("task: floor"); err != nil {
		t.Fatal(err)
	}
	m2.sync()
	m3.sync()
	won := m2.claimAs("biz1/m2/implementer", floor.ref)
	lost := m3.claimAs("biz2/m3/implementer", floor.ref)
	if err := m2.publish("claim floor"); err != nil {
		t.Fatalf("the first claim pushes: %v", err)
	}
	if err := m3.publish("claim floor"); err == nil {
		t.Fatal("the second claim's push must be rejected: main moved")
	}
	m3.sync()
	if status, ok := m3.attemptClaim(lost); status != query.ClaimLost || ok {
		t.Fatalf("after the merge the second claim is LOST with exit 1, got %s", status)
	}
	m2.sync()
	if status, ok := m2.attemptClaim(won); status != query.ClaimHeld || !ok {
		t.Fatalf("the first claim is HELD, got %s", status)
	}
	lostTodo := m3.todo()
	if aside := lostTodo.SetAside; len(aside) != 1 || !strings.Contains(aside[0].Next, "lost claim") {
		t.Fatalf("todo names the lost claim: %+v", aside)
	}
	if lostTodo.Totals.IntakeSetAside != 1 || lostTodo.Totals.IntakeUnreviewed != 0 {
		t.Fatalf("the lost claim's packet is set aside, never offered for admission again: %+v", lostTodo.Totals)
	}
	if a, code := checkLedger(t, m3.root, m3.home); a.Clean || code != 1 || len(a.SetAside) != 1 {
		t.Fatalf("an unresolved set-aside fails check ledger: exit %d %+v", code, a)
	}
	m3.resolveAll("biz2/m3/implementer", "lost the claim; stopped")
	nestGit(t, m3.root, "add", "--", ".whosaidso")
	nestGit(t, m3.root, "commit", "-q", "-m", "resolve lost claim")
	if a, code := checkLedger(t, m3.root, m3.home); !a.Clean || code != 0 || len(a.Resolved) != 1 {
		t.Fatalf("a resolved set-aside is listed, not owed: exit %d %+v", code, a)
	}
	if err := tryGit(m3.root, "push", "-q", "origin", "HEAD:main"); err != nil {
		t.Fatalf("the merged, resolved ledger pushes: %v", err)
	}

	// One owner message captured by two profiles of machine 1, each from the
	// same current main.
	m1.sync()
	m1b.sync()
	ownerMessage(m1, "owner/order-5.json", 5, floor.ref)
	if err := m1.publish("owner order 5"); err != nil {
		t.Fatal(err)
	}
	ownerMessage(m1b, "owner/order-5-again.json", 5, floor.ref)
	nestGit(t, m1b.root, "add", "--", ".whosaidso")
	nestGit(t, m1b.root, "commit", "-q", "-m", "owner order 5, captured again")
	m1b.sync()
	aside := m1b.todo().SetAside
	if len(aside) != 1 || aside[0].Code != "duplicate-source-order" {
		t.Fatalf("one message captured twice is set aside at the merge: %+v", aside)
	}
	if a, _ := checkLedger(t, m1b.root, m1b.home); a.Clean {
		t.Fatal("check ledger fails until the duplicate is resolved")
	}
	m1b.resolveAll("biz1/m1/coordinator", "the same message; it stays out")
	if err := m1b.publish("resolve duplicate"); err != nil {
		t.Fatalf("the lane, merged and resolved, pushes to main: %v", err)
	}

	// The floor's holder departs; machine 1 releases the claim on the owner's word.
	m1.sync()
	if c := m1.todo().InFlight; !holds(c, floor.ref.RecordID, "biz1/m2/implementer") {
		t.Fatalf("the board names the holder: %+v", c)
	}
	order := m1.ownerSays("owner", "owner/release.json", floor.ref)
	if err := m1.releaseAs("biz1/m1/coordinator", won, order); err != nil {
		t.Fatalf("the release on the owner's word admits: %v", err)
	}
	if err := m1.publish("release floor"); err != nil {
		t.Fatal(err)
	}
	m2.sync()
	if status, ok := m2.attemptClaim(won); status != query.ClaimEnded || ok {
		t.Fatalf("the departed holder's claim reads ENDED after a sync, got %s", status)
	}
	m3.sync()
	again := m3.claimAs("biz2/m3/implementer", floor.ref)
	if err := m3.publish("claim floor again"); err != nil {
		t.Fatal(err)
	}
	if status, ok := m3.attemptClaim(again); status != query.ClaimHeld || !ok {
		t.Fatalf("the released task is claimed again, got %s", status)
	}
	if a, code := checkLedger(t, m3.root, m3.home); !a.Clean || code != 0 {
		t.Fatalf("the shared ledger passes check ledger: exit %d %+v", code, a)
	}
	var marks []string
	for _, m := range []*flowWorld{m1, m1b, m2, m3} {
		m.sync()
		mark, _ := json.Marshal(m.todo().Watermark)
		marks = append(marks, string(mark))
	}
	for _, w := range marks[1:] {
		if w != marks[0] {
			t.Fatalf("every machine reads the same ledger after syncing: %+v", marks)
		}
	}
}

// createTask admits a task without claiming it.
func (w *flowWorld) createTask(intent string) flowTask {
	w.t.Helper()
	spec := model.TaskSpec{Intent: intent, Subject: "the machines fixture", Scope: w.scope, NonGoals: []string{"production writes"},
		AcceptanceCriteria: []model.AcceptanceCriterion{{ID: w.id(), Revision: 1, Criterion: "the work is delivered"}},
		ContextRefs:        []model.RecordRef{}, ConstraintRefs: []model.RecordRef{}, Prerequisites: []model.Prerequisite{}, NextActor: model.Actor{ID: flowAgent}}
	create := &model.TaskCreate{ID: w.id(), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}}, Spec: spec}
	w.mustAdmit("biz1/m1/coordinator", create)
	return flowTask{ref: w.ref(create.ID, 1), spec: spec}
}

func holds(tasks []query.TodoTask, task model.ID, holder string) bool {
	for _, t := range tasks {
		if t.Ref.RecordID != task {
			continue
		}
		for _, c := range t.Claims {
			if b, _ := json.Marshal(c.Holder); strings.Contains(string(b), `"`+holder+`"`) {
				return true
			}
		}
	}
	return false
}
