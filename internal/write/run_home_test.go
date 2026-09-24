package write

// The run side of the two roots: when a project's home is another checkout,
// a run executes in the invoking checkout and records that checkout's HEAD
// and dirty state, never the home's. The registry itself is tested in
// internal/store.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/store"
)

// splitProject is a home repository and a different invoking checkout, each
// committed at its own HEAD.
func splitProject(t *testing.T) (p store.Project, homeHead, checkoutHead string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(store.HomeEnv, "")
	homeRepo, home := identityRepo(t)
	checkoutRepo, checkout := identityRepo(t)
	proofPut(t, checkout, "src/tool.sh", "echo a different commit\n")
	identityGit(t, checkoutRepo, "commit", "--quiet", "-am", "checkout moves on")
	homeHead = identityGit(t, homeRepo, "rev-parse", "HEAD")
	checkoutHead = identityGit(t, checkoutRepo, "rev-parse", "HEAD")
	if homeHead == checkoutHead {
		t.Fatal("control: the two checkouts must sit at different commits")
	}
	p = store.Project{ID: "test/identity", Root: home, Ledger: filepath.Join(home, ".whosaidso", "events"), Checkout: checkout}
	return p, homeHead, checkoutHead
}

func TestRunIdentityRecordsTheInvokingCheckoutsHead(t *testing.T) {
	p, homeHead, checkoutHead := splitProject(t)
	id := RunExecutionIdentity(context.Background(), p)
	if id.Head.State != model.Known || id.Head.Value.Commit != checkoutHead {
		t.Fatalf("HEAD must be the invoking checkout's %s, not the home's %s: %+v", checkoutHead, homeHead, id.Head)
	}
	if id.Dirty.State != model.Known || *id.Dirty.Value {
		t.Fatalf("the clean invoking checkout is known clean: %+v", id.Dirty)
	}
	// The checkout's own copy of WhoSaidSo's record folders is not source either.
	proofPut(t, p.Checkout, ".whosaidso/events/000001.json", "{}")
	if id := RunExecutionIdentity(context.Background(), p); id.Dirty.State != model.Known || *id.Dirty.Value {
		t.Fatalf("the checkout's ledger copy is a record, not source: %+v", id.Dirty)
	}
	// A dirty home does not make the run dirty; a dirty checkout does.
	proofPut(t, p.Root, "src/tool.sh", "edited in the home\n")
	if id := RunExecutionIdentity(context.Background(), p); id.Dirty.State != model.Known || *id.Dirty.Value {
		t.Fatalf("the home's working tree is not what executes: %+v", id.Dirty)
	}
	proofPut(t, p.Checkout, "src/tool.sh", "edited in the checkout\n")
	if id := RunExecutionIdentity(context.Background(), p); id.Dirty.State != model.Unknown {
		t.Fatalf("an edited invoking checkout cannot be recorded clean: %+v", id.Dirty)
	}
}

func TestRunExecutesInTheInvokingCheckout(t *testing.T) {
	p, _, _ := splitProject(t)
	r := runTestRequest(p.Root, "ok")
	r.InstrumentRef.Project, r.ExecutionSourceIdentity.Project = p.ID, p.ID
	r.Argv, r.Dir = []string{"/bin/pwd", "-P"}, ""
	result, err := Run(context.Background(), p, r)
	if err != nil {
		t.Fatalf("control: run: %v", err)
	}
	want, err := filepath.EvalSymlinks(p.Checkout)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(result.StdoutTail)); got != want {
		t.Fatalf("the process must run in the invoking checkout %s, ran in %s (home %s)", want, got, p.Root)
	}
}
