package evidence

// The raw git observer against real repositories. CheckoutHead and
// CheckoutDirty report what git says and conclude nothing: dirty is KNOWN
// either way. ScopeChanges, in a repository whose datum root is a
// subdirectory: only scoped files between the two commits, relative to the
// root, renames as both names, scope paths taken literally, an empty scope
// asking nothing, and commits git cannot find refused rather than read as "no
// change".

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"datum/internal/model"
)

func TestScopeChangesListsOnlyScopedFilesBetweenTwoCommits(t *testing.T) {
	repo := newRepo(t)
	head := func(c string) model.GitHead { return model.GitHead{ObjectFormat: "sha1", Commit: c} }
	a := head(commitFile(t, repo, "project/src/a.go", "v1\n"))
	commitFile(t, repo, "project/src/*.go", "a file literally named star\n")
	commitFile(t, repo, "project/notes.txt", "outside the scope\n")
	commitFile(t, repo, "outside/src/a.go", "outside the datum root\n")
	gitRun(t, repo, "mv", "project/src/a.go", "project/src/b.go")
	gitRun(t, repo, "commit", "--quiet", "-m", "rename")
	b := head(gitRun(t, repo, "rev-parse", "HEAD"))
	root := filepath.Join(repo, "project")
	ctx := context.Background()

	got, err := ScopeChanges(ctx, nil, root, a, b, []string{"src"})
	if want := []string{"src/*.go", "src/a.go", "src/b.go"}; err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("control: want %v, got %v, %v", want, got, err)
	}
	// A scope path is literal: "src/*.go" names one file, never a glob.
	if got, err := ScopeChanges(ctx, nil, root, a, b, []string{"src/*.go"}); err != nil || !reflect.DeepEqual(got, []string{"src/*.go"}) {
		t.Fatalf("a literal scope path matched as a glob: %v, %v", got, err)
	}
	if got, err := ScopeChanges(ctx, nil, root, a, b, []string{"docs"}); err != nil || len(got) != 0 {
		t.Fatalf("an untouched scope must list nothing: %v, %v", got, err)
	}
	if got, err := ScopeChanges(ctx, nil, root, a, b, nil); err != nil || len(got) != 0 {
		t.Fatalf("an empty scope selects nothing, never the whole repository: %v, %v", got, err)
	}
	// A tree is not a commit, though git would happily diff against it.
	tree := head(gitRun(t, repo, "rev-parse", b.Commit+"^{tree}"))
	wantFault(t, errOnly(ScopeChanges(ctx, nil, root, a, tree, []string{"src"})), "unavailable")
	missing := head(strings.Repeat("0", 40))
	wantFault(t, errOnly(ScopeChanges(ctx, nil, root, a, missing, []string{"src"})), "unavailable")
	wantFault(t, errOnly(ScopeChanges(ctx, nil, root, model.GitHead{ObjectFormat: "sha256", Commit: strings.Repeat("0", 64)}, b, []string{"src"})), "invalid-field")
}

func errOnly(_ []string, err error) error { return err }

func TestCheckoutHeadIsTheCommitGitReportsOrUnknown(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	if got := CheckoutHead(ctx, nil, repo); got.State != model.Unknown || !strings.Contains(got.Reason, "no readable HEAD commit") {
		t.Fatalf("a repository with no commit has no HEAD: %+v", got)
	}
	commit := commitFile(t, repo, "project/src/a.go", "v1\n")
	got := CheckoutHead(ctx, nil, filepath.Join(repo, "project"))
	if got.State != model.Known || *got.Value != (model.GitHead{ObjectFormat: "sha1", Commit: commit}) {
		t.Fatalf("HEAD must be the checked-out commit %s: %+v", commit, got)
	}
	if got := CheckoutHead(ctx, nil, t.TempDir()); got.State != model.Unknown || !strings.Contains(got.Reason, "not a readable git checkout") {
		t.Fatalf("outside a checkout HEAD is UNKNOWN with the reason: %+v", got)
	}
}

func TestCheckoutDirtyReportsWhatGitSeesUnderThePathspec(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	commitFile(t, repo, "project/src/a.go", "v1\n")
	commitFile(t, repo, "outside.txt", "not the project\n")
	root := filepath.Join(repo, "project")
	dirty := func(pathspec ...string) model.Availability[bool] { return CheckoutDirty(ctx, nil, root, pathspec...) }
	if got := dirty(); got.State != model.Known || *got.Value {
		t.Fatalf("control: a clean checkout is known clean: %+v", got)
	}
	writeFile(t, repo, "outside.txt", "edited\n")
	if got := dirty(); got.State != model.Known || !*got.Value {
		t.Fatalf("with no pathspec the whole repository counts, and dirty is reported, never withheld: %+v", got)
	}
	if got := dirty("."); got.State != model.Known || *got.Value {
		t.Fatalf("a pathspec limits what counts: %+v", got)
	}
	writeFile(t, root, "records/one.json", "{}")
	if got := dirty(".", ":(exclude)records"); got.State != model.Known || *got.Value {
		t.Fatalf("an excluded path does not count: %+v", got)
	}
	writeFile(t, root, "src/new.go", "untracked\n")
	if got := dirty(".", ":(exclude)records"); got.State != model.Known || !*got.Value {
		t.Fatalf("an untracked file under the pathspec is dirty: %+v", got)
	}
	if got := CheckoutDirty(ctx, nil, t.TempDir()); got.State != model.Unknown || !strings.Contains(got.Reason, "status") {
		t.Fatalf("outside a checkout dirty is UNKNOWN with git's reason: %+v", got)
	}
}
