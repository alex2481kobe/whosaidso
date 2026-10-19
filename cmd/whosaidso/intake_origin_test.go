package main

// Intake is shared by every checkout of a project on one machine, and each
// checkout admits into its own ledger: todo lists a pending packet as this
// checkout's to review only when this checkout captured it, or when no origin
// was noted (packets captured before origins were). Where origins are noted
// is internal/store/intake_origin.go.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/query"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func TestCLITodoTellsThisCheckoutsIntakeFromAnothers(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root, input := cliFixture(t)
	homeGit(t, root, "init", "--quiet")
	homeGit(t, root, "add", "whosaidso.toml")
	homeGit(t, root, "commit", "--quiet", "-m", "project")
	worktree := filepath.Join(t.TempDir(), "wt")
	homeGit(t, root, "worktree", "add", "--quiet", "--detach", worktree)
	packet := cliID(20)
	if _, errOut, code := cliRun(t, worktree, input, "agent", "capture", "--command-id", string(packet), "--events", "-"); code != 0 {
		t.Fatalf("control: capture in the worktree: %s", errOut)
	}
	todo := func(dir string) *query.TodoAnswer {
		t.Helper()
		out, errOut, code := cliRun(t, dir, nil, "", "todo", "--json")
		if code != 0 {
			t.Fatalf("todo in %s: %d %s", dir, code, errOut)
		}
		return readJSON[*query.TodoAnswer](t, []byte(out))
	}
	realWorktree, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	here := todo(worktree)
	if here.Totals.IntakeUnreviewed != 1 || here.Totals.IntakeElsewhere != 0 || len(here.PacketsNotAccepted) != 1 ||
		here.PacketsNotAccepted[0].Disposition != "pending" || here.PacketsNotAccepted[0].CapturedIn != realWorktree {
		t.Fatalf("the capturing worktree owes the review: %+v %+v", here.Totals, here.PacketsNotAccepted)
	}
	there := todo(root)
	if there.Totals.IntakeUnreviewed != 0 || there.Totals.IntakeElsewhere != 1 || len(there.PacketsNotAccepted) != 1 ||
		there.PacketsNotAccepted[0].Disposition != "captured-elsewhere" || there.PacketsNotAccepted[0].CapturedIn != realWorktree {
		t.Fatalf("another checkout lists the packet as captured elsewhere, never as its own to review: %+v %+v", there.Totals, there.PacketsNotAccepted)
	}
	out, _, _ := cliRun(t, root, nil, "", "todo")
	if !strings.Contains(out, "intake unreviewed: none") || !strings.Contains(out, "intake captured in other checkouts") || !strings.Contains(out, realWorktree) {
		t.Fatalf("todo text must name the capturing checkout:\n%s", out)
	}
	// A packet with no noted origin reads as before: pending wherever it is read.
	home, err := store.Home()
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := store.IntakeDir(store.Project{ID: "test/cli"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(home, "intake-origin", filepath.Base(inbox), string(packet))); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, worktree} {
		if a := todo(dir); a.Totals.IntakeUnreviewed != 1 || a.PacketsNotAccepted[0].CapturedIn != "" {
			t.Fatalf("%s: a packet with no origin is pending here: %+v", dir, a.Totals)
		}
	}
}
