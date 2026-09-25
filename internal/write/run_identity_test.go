package write

// What `whosaidso run` observes about where it executes: the persistent machine id
// and the project root's git HEAD and dirty state. Launch and report handling
// are tested in run_test.go.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func identityGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func identityProject(t *testing.T, root string) store.Project {
	return store.Project{ID: "test/identity", Root: root, Ledger: filepath.Join(root, ".whosaidso", "events")}
}

// identityRepo is a committed checkout whose whosaidso root is a subdirectory.
func identityRepo(t *testing.T) (repo, root string) {
	t.Helper()
	repo = t.TempDir()
	identityGit(t, repo, "init", "--quiet")
	root = filepath.Join(repo, "project")
	proofPut(t, root, "src/tool.sh", "echo measured\n")
	proofPut(t, repo, "outside.txt", "not the project\n")
	identityGit(t, repo, "add", ".")
	identityGit(t, repo, "commit", "--quiet", "-m", "fixture")
	return repo, root
}

func TestRunIdentityRecordsMachineHeadAndCleanState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo, root := identityRepo(t)
	id := RunExecutionIdentity(context.Background(), identityProject(t, root))
	machine, err := store.MachineID()
	if err != nil || id.MachineID.State != model.Known || *id.MachineID.Value != machine {
		t.Fatalf("the persistent machine id must be recorded: %+v vs %q, %v", id.MachineID, machine, err)
	}
	head := identityGit(t, repo, "rev-parse", "HEAD")
	if id.Head.State != model.Known || id.Head.Value.Commit != head || id.Head.Value.ObjectFormat != "sha1" {
		t.Fatalf("HEAD must be the checkout's commit %s: %+v", head, id.Head)
	}
	if id.Dirty.State != model.Known || *id.Dirty.Value {
		t.Fatalf("a clean checkout is known clean: %+v", id.Dirty)
	}
	if err := model.ValidateSchema(id); err != nil {
		t.Fatalf("the observed identity must satisfy the schema: %v", err)
	}
	if again := RunExecutionIdentity(context.Background(), identityProject(t, root)); *again.MachineID.Value != machine {
		t.Fatal("two runs on one machine must record one machine id")
	}

	t.Run("whosaidso's own record directories do not make the source dirty", func(t *testing.T) {
		proofPut(t, root, ".whosaidso/artifacts/runs/01ARZ3NDEKTSV4RRFFQ69G5FAX/stdout", "x")
		proofPut(t, root, ".whosaidso/events/000001.json", "{}")
		if id := RunExecutionIdentity(context.Background(), identityProject(t, root)); id.Dirty.State != model.Known || *id.Dirty.Value {
			t.Fatalf("records of runs are not the source a run executes: %+v", id.Dirty)
		}
	})
	t.Run("changes outside the whosaidso root do not make it dirty", func(t *testing.T) {
		proofPut(t, repo, "outside.txt", "edited\n")
		if id := RunExecutionIdentity(context.Background(), identityProject(t, root)); id.Dirty.State != model.Known || *id.Dirty.Value {
			t.Fatalf("the project root's state is what is recorded: %+v", id.Dirty)
		}
	})
}

// The excluded artifact store is the one beside the configured ledger.
func TestRunIdentityExcludesTheStoreBesideAConfiguredLedger(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, root := identityRepo(t)
	moved := store.Project{ID: "test/identity", Root: root, Ledger: filepath.Join(root, "custom", "events")}
	proofPut(t, root, "custom/artifacts/runs/01ARZ3NDEKTSV4RRFFQ69G5FAX/stdout", "x")
	proofPut(t, root, "custom/events/000001.json", "{}")
	if id := RunExecutionIdentity(context.Background(), moved); id.Dirty.State != model.Known || *id.Dirty.Value {
		t.Fatalf("the artifact store beside a configured ledger is not source: %+v", id.Dirty)
	}
}

func TestRunIdentityDirtyOrUnobservableIsUnknownWithAReason(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T) string
		head  bool // HEAD stays known
		why   string
	}{
		{"edited tracked file", func(t *testing.T) string {
			_, root := identityRepo(t)
			proofPut(t, root, "src/tool.sh", "echo edited\n")
			return root
		}, true, "differs from HEAD"},
		{"untracked file", func(t *testing.T) string {
			_, root := identityRepo(t)
			proofPut(t, root, "src/new.sh", "echo new\n")
			return root
		}, true, "differs from HEAD"},
		{"not a git checkout", func(t *testing.T) string { return t.TempDir() }, false, "not a readable git checkout"},
		{"no commit yet", func(t *testing.T) string {
			repo := t.TempDir()
			identityGit(t, repo, "init", "--quiet")
			return repo
		}, false, "no readable HEAD"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := RunExecutionIdentity(context.Background(), identityProject(t, tc.setup(t)))
			if id.Dirty.State != model.Unknown || !strings.Contains(id.Dirty.Reason, tc.why) {
				t.Fatalf("dirty must be UNKNOWN because %q: %+v", tc.why, id.Dirty)
			}
			if tc.head != (id.Head.State == model.Known) || !tc.head && !strings.Contains(id.Head.Reason, tc.why) {
				t.Fatalf("head known=%v expected, got %+v", tc.head, id.Head)
			}
			if err := model.ValidateSchema(id); err != nil {
				t.Fatalf("the observed identity must satisfy the schema: %v", err)
			}
		})
	}

	t.Run("an unreadable machine id is UNKNOWN, never regenerated", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		proofPut(t, home, ".whosaidso/"+store.MachineIDFile, "damaged\n")
		id := RunExecutionIdentity(context.Background(), identityProject(t, t.TempDir()))
		if id.MachineID.State != model.Unknown || !strings.Contains(id.MachineID.Reason, "machine id") {
			t.Fatalf("a damaged machine id must be UNKNOWN with the reason: %+v", id.MachineID)
		}
	})
}
