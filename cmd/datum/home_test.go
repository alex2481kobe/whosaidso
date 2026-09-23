package main

// The home verb and the two roots, driven through the CLI: unbound refusal,
// reads and admission from a second worktree landing in the home, a missing
// home, relocation, and bindTestHome, the one setup every CLI test that reads
// or admits uses. The registry's own rules are tested in internal/store.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"datum/internal/model"
	"datum/internal/query"
	"datum/internal/store"
)

// bindTestHome gives the test its own Datum home (DATUM_HOME: registry,
// intake, staging and machine id together) and binds the project at root
// there. It never touches the real ~/.datum.
func bindTestHome(t *testing.T, root string) {
	t.Helper()
	// A home Datum creates itself, owner-only, as it creates ~/.datum.
	t.Setenv("DATUM_HOME", filepath.Join(t.TempDir(), "datum-home"))
	if _, err := store.Bind(context.Background(), root, root); err != nil {
		t.Fatalf("binding the test home: %v", err)
	}
}

// homeGit runs git in dir with no user or system config.
func homeGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// homeTask is cliFixture's task.create under fresh ids n and n+1.
func homeTask(t *testing.T, input []byte, n int) []byte {
	t.Helper()
	var events []model.Event
	if err := json.Unmarshal(input, &events); err != nil {
		t.Fatal(err)
	}
	typed, err := model.DecodeEvent(events[0])
	if err != nil {
		t.Fatal(err)
	}
	task := typed.(*model.TaskCreate)
	task.ID, task.Spec.AcceptanceCriteria[0].ID = cliID(n), cliID(n+1)
	event, err := model.EncodeEvent(task)
	if err != nil {
		t.Fatal(err)
	}
	data, err := model.Encode([]model.Event{event})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// homeBundles is the bundle count a read from dir reports, and its stderr.
func homeBundles(t *testing.T, dir string) (int, string) {
	t.Helper()
	out, errOut, code := cliRun(t, dir, nil, "", "show", "--json")
	if code != 0 {
		t.Fatalf("show from %s: %d %s", dir, code, errOut)
	}
	return readJSON[query.ShowAnswer](t, []byte(out)).Watermark.Bundles, errOut
}

func homeLedgerFiles(t *testing.T, root string) int {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(root, ".datum", "events", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return len(files)
}

func TestCLIUnboundProjectRefusesReadsAndAdmission(t *testing.T) {
	root, input := cliFixture(t)
	t.Setenv("DATUM_HOME", filepath.Join(t.TempDir(), "unbound-home"))
	for _, args := range [][]string{{"show"}, {"todo"}, {"history"}, {"admit", "--outcome", "accepted", "--reason", "r", string(cliID(3))},
		{"capture", "--admit", "--reason", "r", "--events", "-"}} {
		_, errOut, code := cliRun(t, root, input, "lane", args...)
		if code != 1 || !strings.Contains(errOut, "not bound") || !strings.Contains(errOut, "datum home PATH") {
			t.Fatalf("%v in an unbound project must refuse and say how to bind, got %d %q", args, code, errOut)
		}
	}
	// Plain capture routes intake by the declared id and needs no home.
	if out, errOut, code := cliRun(t, root, input, "lane", "capture", "--events", "-"); code != 0 || !strings.HasPrefix(out, "captured ") {
		t.Fatalf("control: plain capture: %d %q %q", code, out, errOut)
	}
	out, _, code := cliRun(t, root, nil, "", "home")
	if code != 0 || !strings.Contains(out, "unbound") {
		t.Fatalf("datum home must report unbound: %d %q", code, out)
	}
	if _, bound, err := store.Binding("test/cli"); bound || err != nil {
		t.Fatalf("no command may bind silently: %v %v", bound, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".datum", "events")); !os.IsNotExist(err) {
		t.Fatalf("an unbound project must not get a ledger: %v", err)
	}
	// Control: bind, and the same read answers.
	if out, errOut, code := cliRun(t, root, nil, "", "home", "."); code != 0 || !strings.HasPrefix(out, "bound test/cli to ") {
		t.Fatalf("bind: %d %q %q", code, out, errOut)
	}
	if _, errOut, code := cliRun(t, root, nil, "", "show"); code != 0 || errOut != "" {
		t.Fatalf("a read from the home itself names no other home: %d %q", code, errOut)
	}
}

func TestCLISecondWorktreeReadsAndAdmitsThroughTheHome(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root, input := cliFixture(t) // bound: root is the home
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := cliRun(t, root, input, "lane", "capture", "--admit", "--reason", "first", "--events", "-"); code != 0 {
		t.Fatalf("control: admit in the home: %s", errOut)
	}
	homeGit(t, root, "init", "--quiet")
	homeGit(t, root, "add", "datum.toml", ".datum/events")
	homeGit(t, root, "commit", "--quiet", "-m", "one bundle")
	worktree := filepath.Join(t.TempDir(), "wt")
	homeGit(t, root, "worktree", "add", "--quiet", "--detach", worktree)
	if homeLedgerFiles(t, worktree) != 1 {
		t.Fatal("control: the worktree carries its own copy of the first bundle")
	}
	out, errOut, code := cliRun(t, worktree, homeTask(t, input, 11), "lane", "capture", "--admit", "--reason", "second", "--events", "-")
	if code != 0 || !strings.Contains(out, "admitted") {
		t.Fatalf("admit from the worktree: %d %q %q", code, out, errOut)
	}
	if !strings.Contains(errOut, "home "+real) || !strings.Contains(errOut, worktree) {
		t.Fatalf("output from another checkout must name the home used, got %q", errOut)
	}
	if homeLedgerFiles(t, root) != 2 || homeLedgerFiles(t, worktree) != 1 {
		t.Fatalf("the admission must land in the home ledger only: home %d, worktree %d", homeLedgerFiles(t, root), homeLedgerFiles(t, worktree))
	}
	if n, errOut := homeBundles(t, worktree); n != 2 || !strings.Contains(errOut, "home "+real) {
		t.Fatalf("a read from the worktree must answer from the home (2 bundles), not its own copy: %d %q", n, errOut)
	}
	if n, _ := homeBundles(t, root); n != 2 {
		t.Fatalf("the home reads its own ledger: %d", n)
	}
}

func TestCLIMissingHomeRefuses(t *testing.T) {
	root, input := cliFixture(t)
	clone := t.TempDir()
	data, err := os.ReadFile(filepath.Join(root, "datum.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "datum.toml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if n, _ := homeBundles(t, clone); n != 0 {
		t.Fatalf("control: the clone reads the empty home: %d", n)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"show"}, {"capture", "--admit", "--reason", "r", "--events", "-"}} {
		_, errOut, code := cliRun(t, clone, input, "lane", args...)
		if code != 1 || !strings.Contains(errOut, "unavailable") {
			t.Fatalf("%v with the home gone must refuse, got %d %q", args, code, errOut)
		}
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("the missing home was recreated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(clone, ".datum", "events")); !os.IsNotExist(err) {
		t.Fatalf("the clone's own ledger was used as a fallback: %v", err)
	}
	out, _, code := cliRun(t, clone, nil, "", "home", "--json")
	var a struct {
		Bound, Available bool
		HomeRoot         *string `json:"home_root"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &a) != nil || !a.Bound || a.Available || a.HomeRoot == nil {
		t.Fatalf("datum home must report a bound, unavailable home: %d %q", code, out)
	}
}

func TestCLIRelocation(t *testing.T) {
	root, input := cliFixture(t)
	if _, errOut, code := cliRun(t, root, input, "lane", "capture", "--admit", "--reason", "first", "--events", "-"); code != 0 {
		t.Fatalf("control: %s", errOut)
	}
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	next := filepath.Join(parent, "next")
	if err := exec.Command("cp", "-R", root, next).Run(); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(parent, "other")
	if err := os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "datum.toml"), []byte("id = \"test/elsewhere\"\nledger = \".datum/events\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := cliRun(t, root, nil, "", "home", other); code != 1 || !strings.Contains(errOut, "test/elsewhere") {
		t.Fatalf("binding to another project's checkout must refuse: %d %q", code, errOut)
	}
	out, errOut, code := cliRun(t, root, nil, "", "home", next)
	if code != 0 || !strings.Contains(out, "continue the old history") {
		t.Fatalf("relocation with continuity: %d %q %q", code, out, errOut)
	}
	if n, errOut := homeBundles(t, root); n != 1 || !strings.Contains(errOut, "home "+next) {
		t.Fatalf("the old checkout now reads through the new home: %d %q", n, errOut)
	}
	// The old home is gone: move on again, and say continuity was not compared.
	last := filepath.Join(parent, "last")
	if err := os.Rename(next, last); err != nil {
		t.Fatal(err)
	}
	out, errOut, code = cliRun(t, last, nil, "", "home", "--json", ".")
	var a struct {
		Continuity string `json:"continuity"`
		Bundles    int    `json:"bundles"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &a) != nil || a.Continuity != "not-compared" || a.Bundles != 1 {
		t.Fatalf("relocation with the old home gone: %d %q %q", code, out, errOut)
	}
}

// TestCLICaptureAdmitReadsSourcesFromTheInvokingCheckout: with the home
// elsewhere, a source file that exists only in this checkout is captured.
func TestCLICaptureAdmitReadsSourcesFromTheInvokingCheckout(t *testing.T) {
	root, _ := cliFixture(t)
	clone := t.TempDir()
	data, err := os.ReadFile(filepath.Join(root, "datum.toml"))
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("words that exist only in the invoking checkout")
	if err := os.WriteFile(filepath.Join(clone, "datum.toml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "ruling.txt"), body, 0600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := cliRun(t, clone, sourceEvents(t, body, "ruling.txt"), "lane", "capture", "--admit", "--reason", "source from here", "--events", "-")
	if code != 0 || !strings.HasPrefix(out, "captured ") {
		t.Fatalf("the source must be read from the invoking checkout, not the home: %d %q %q", code, out, errOut)
	}
	if homeLedgerFiles(t, root) != 1 || homeLedgerFiles(t, clone) != 0 {
		t.Fatal("the admission must land in the home")
	}
}
