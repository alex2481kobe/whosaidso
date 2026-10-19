package main

// The home verb and routing, driven through the CLI: every checkout and
// worktree reads and admits its own ledger, registered or not; the viewer's
// registry entry; and bindTestHome, the one setup every CLI test that reads
// or admits uses. The registry's own rules are tested in internal/store.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/query"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

// bindTestHome gives the test its own WhoSaidSo home (WHOSAIDSO_HOME: registry,
// intake, staging and machine id together) and binds the project at root
// there. It never touches the real ~/.whosaidso.
func bindTestHome(t *testing.T, root string) {
	t.Helper()
	// A home WhoSaidSo creates itself, owner-only, as it creates ~/.whosaidso.
	t.Setenv("WHOSAIDSO_HOME", filepath.Join(t.TempDir(), "whosaidso-home"))
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
	files, err := filepath.Glob(filepath.Join(root, ".whosaidso", "events", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return len(files)
}

// TestCLIUnregisteredProjectReadsAndWritesItsCheckout: no registry entry is
// needed to read or admit, and no command registers one silently.
func TestCLIUnregisteredProjectReadsAndWritesItsCheckout(t *testing.T) {
	root, input := cliFixture(t)
	t.Setenv("WHOSAIDSO_HOME", filepath.Join(t.TempDir(), "unregistered-home"))
	if _, errOut, code := cliRun(t, root, input, "agent", "capture", "--admit", "--reason", "r", "--events", "-"); code != 0 {
		t.Fatalf("admission in an unregistered checkout: %d %q", code, errOut)
	}
	for _, args := range [][]string{{"show"}, {"todo"}, {"history"}} {
		if _, errOut, code := cliRun(t, root, nil, "", args...); code != 0 || errOut != "" {
			t.Fatalf("%v in an unregistered checkout: %d %q", args, code, errOut)
		}
	}
	if homeLedgerFiles(t, root) != 1 {
		t.Fatal("the admission must land in the invoking checkout")
	}
	out, _, code := cliRun(t, root, nil, "", "home")
	if code != 0 || !strings.Contains(out, "none registered") {
		t.Fatalf("whosaidso home must report no viewer entry: %d %q", code, out)
	}
	if _, registered, err := store.Binding("test/cli"); registered || err != nil {
		t.Fatalf("no command may register silently: %v %v", registered, err)
	}
	if out, errOut, code := cliRun(t, root, nil, "", "home", "."); code != 0 || !strings.HasPrefix(out, "registered test/cli at ") {
		t.Fatalf("control: register: %d %q %q", code, out, errOut)
	}
}

// TestCLIEachWorktreeRecordsInItsOwnLedger: reads and admission use the
// invoking checkout's own ledger, whatever this machine's registry names, so
// two worktrees (two lanes, or two profiles under one OS user sharing one
// home) each record on their own branch and never into the other's.
func TestCLIEachWorktreeRecordsInItsOwnLedger(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root, input := cliFixture(t) // registered: root is the viewer's checkout
	if _, errOut, code := cliRun(t, root, input, "agent", "capture", "--admit", "--reason", "first", "--events", "-"); code != 0 {
		t.Fatalf("control: admit in root: %s", errOut)
	}
	homeGit(t, root, "init", "--quiet")
	homeGit(t, root, "add", "whosaidso.toml", ".whosaidso/events")
	homeGit(t, root, "commit", "--quiet", "-m", "one bundle")
	worktree := filepath.Join(t.TempDir(), "wt")
	homeGit(t, root, "worktree", "add", "--quiet", "--detach", worktree)
	if homeLedgerFiles(t, worktree) != 1 {
		t.Fatal("control: the worktree carries its own copy of the first bundle")
	}
	out, errOut, code := cliRun(t, worktree, homeTask(t, input, 11), "agent", "capture", "--admit", "--reason", "second", "--events", "-")
	if code != 0 || !strings.Contains(out, "admitted") {
		t.Fatalf("admit from the worktree: %d %q %q", code, out, errOut)
	}
	if homeLedgerFiles(t, worktree) != 2 || homeLedgerFiles(t, root) != 1 {
		t.Fatalf("the admission must land in the invoking worktree only: worktree %d, root %d", homeLedgerFiles(t, worktree), homeLedgerFiles(t, root))
	}
	if n, errOut := homeBundles(t, worktree); n != 2 || errOut != "" {
		t.Fatalf("a read from the worktree answers from its own ledger and names no other: %d %q", n, errOut)
	}
	if n, _ := homeBundles(t, root); n != 1 {
		t.Fatalf("root reads its own ledger: %d", n)
	}
}

// TestCLIRegistration: home PATH refuses another project's checkout, and
// re-registering names the entry it replaced; a registered checkout that is
// gone is reported unavailable and changes no read.
func TestCLIRegistration(t *testing.T) {
	root, input := cliFixture(t)
	if _, errOut, code := cliRun(t, root, input, "agent", "capture", "--admit", "--reason", "first", "--events", "-"); code != 0 {
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
	if err := os.WriteFile(filepath.Join(other, "whosaidso.toml"), []byte("id = \"test/elsewhere\"\nledger = \".whosaidso/events\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := cliRun(t, root, nil, "", "home", other); code != 1 || !strings.Contains(errOut, "test/elsewhere") {
		t.Fatalf("registering another project's checkout must refuse: %d %q", code, errOut)
	}
	out, errOut, code := cliRun(t, root, nil, "", "home", "--json", next)
	var a struct {
		Previous string `json:"previous_root"`
		Viewer   string `json:"viewer_root"`
		Bundles  int    `json:"bundles"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &a) != nil || a.Viewer != next || a.Previous == "" || a.Bundles != 1 {
		t.Fatalf("re-registration: %d %q %q", code, out, errOut)
	}
	if err := os.RemoveAll(next); err != nil {
		t.Fatal(err)
	}
	if n, errOut := homeBundles(t, root); n != 1 || errOut != "" {
		t.Fatalf("a gone registered checkout changes no read: %d %q", n, errOut)
	}
	out, _, code = cliRun(t, root, nil, "", "home", "--json")
	var h struct {
		Registered, Available bool
	}
	if code != 0 || json.Unmarshal([]byte(out), &h) != nil || !h.Registered || h.Available {
		t.Fatalf("whosaidso home must report a registered, unavailable checkout: %d %q", code, out)
	}
}

// TestCLICaptureAdmitReadsSourcesFromTheInvokingCheckout: with the registry
// naming another checkout, a source file that exists only in this checkout
// is captured, and the admission lands here.
func TestCLICaptureAdmitReadsSourcesFromTheInvokingCheckout(t *testing.T) {
	root, _ := cliFixture(t)
	clone := t.TempDir()
	data, err := os.ReadFile(filepath.Join(root, "whosaidso.toml"))
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("words that exist only in the invoking checkout")
	if err := os.WriteFile(filepath.Join(clone, "whosaidso.toml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "ruling.txt"), body, 0600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := cliRun(t, clone, sourceEvents(t, body, "ruling.txt"), "agent", "capture", "--admit", "--reason", "source from here", "--events", "-")
	if code != 0 || !strings.HasPrefix(out, "captured ") {
		t.Fatalf("the source must be read from the invoking checkout, not the home: %d %q %q", code, out, errOut)
	}
	if homeLedgerFiles(t, root) != 0 || homeLedgerFiles(t, clone) != 1 {
		t.Fatal("the admission must land in the invoking checkout")
	}
}

// whosaidso home PATH refuses to bind a home every read would refuse: intake
// with loose permissions (the store's own owner-only rule) or a ledger this
// binary cannot decode. Nothing is bound; the reason names the refusal.
func TestCLIHomeRefusesToBindWhatItCannotServe(t *testing.T) {
	root, input := cliFixture(t)
	cliControl(t, root, input) // one admitted bundle in root's ledger
	unbound := func(name string) {
		t.Helper()
		t.Setenv("WHOSAIDSO_HOME", filepath.Join(t.TempDir(), name))
	}
	// Loose intake permissions: a captured packet's directory made 0755.
	unbound("loose")
	if _, errs, code := cliRun(t, root, input, "agent", "capture", "--command-id", string(cliID(60))); code != 0 {
		t.Fatalf("control: plain capture: %d %s", code, errs)
	}
	project, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	intake, err := store.IntakeDir(project)
	if err != nil {
		t.Fatal(err)
	}
	packet := filepath.Join(intake, string(cliID(60)))
	if err := os.Chmod(packet, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, errs, code := cliRun(t, root, nil, "", "home", "."); code != 1 || !strings.Contains(errs, "not registered") || !strings.Contains(errs, "insecure-permissions") {
		t.Fatalf("a home whose intake is not owner-only must not bind: %d %q", code, errs)
	}
	if _, bound, _ := store.Binding("test/cli"); bound {
		t.Fatal("a refused home was bound")
	}
	if err := os.Chmod(packet, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, errs, code := cliRun(t, root, nil, "", "home", "."); code != 0 || !strings.HasPrefix(out, "registered test/cli") {
		t.Fatalf("control: owner-only intake binds: %d %q", code, errs)
	}
	// An undecodable ledger: an admitted event with a field this binary does not know.
	unbound("undecodable")
	ledger := filepath.Join(root, ".whosaidso", "events")
	files, err := os.ReadDir(ledger)
	if err != nil || len(files) == 0 {
		t.Fatalf("the fixture ledger: %v", err)
	}
	var bundle string
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".json") {
			bundle = filepath.Join(ledger, f.Name())
		}
	}
	data, err := os.ReadFile(bundle)
	if err != nil || !strings.Contains(string(data), `"intent"`) {
		t.Fatalf("the fixture bundle holds the task intent: %v", err)
	}
	if err := os.WriteFile(bundle, []byte(strings.Replace(string(data), `"intent"`, `"purpose"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errs, code := cliRun(t, root, nil, "", "home", "."); code != 1 || !strings.Contains(errs, "not registered") || !strings.Contains(errs, "purpose") {
		t.Fatalf("a ledger this binary cannot decode must not bind: %d %q", code, errs)
	}
	if _, bound, _ := store.Binding("test/cli"); bound {
		t.Fatal("an undecodable home was bound")
	}
}
