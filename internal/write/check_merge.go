package write

// The merge gate (whosaidso check merge): is the ledger at one commit whole
// for anyone who clones it? It clones the invoking repository with its full
// history into a temporary directory, checks out the commit, and there, with
// no cache and nothing read from any home or intake, replays the ledger and
// checks what other machines will need:
//   - every content artifact an admitted event cites resolves from the
//     committed artifact store alone (HomeHoldsKeptArtifacts);
//   - every git pin resolves in the clone and is reachable from the commit,
//     so a squash, a rebase or a deleted branch has not orphaned it;
//   - no bundle is set aside (a clash two branches made);
//   - every bundle in this checkout's ledger is in the commit (none left
//     uncommitted or on another branch).
// A recorded disposal excuses the bytes it names, as in admission.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alex2481kobe/whosaidso/internal/evidence"
	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

// MergeAnswer is the merge gate's finding. Clean is true only when every
// list is empty and the ledger replayed.
type MergeAnswer struct {
	Ref          string            `json:"ref"`
	Commit       string            `json:"commit"`
	Bundles      int               `json:"bundles"`
	Clean        bool              `json:"clean"`
	ReplayError  string            `json:"replay_error,omitempty"`
	Artifacts    string            `json:"artifacts_missing,omitempty"`
	GitPins      []string          `json:"git_pins,omitempty"`
	SetAside     []reduce.SetAside `json:"set_aside,omitempty"`
	NotCommitted []string          `json:"not_in_commit,omitempty"`
}

// CheckMerge runs the gate for ref (a commit-ish of the invoking checkout's
// repository). scratch is an empty directory the caller removes afterwards.
func CheckMerge(ctx context.Context, project store.Project, ref, scratch string) (*MergeAnswer, error) {
	git := evidence.ExecGit
	out := func(dir string, args ...string) (string, error) {
		b, err := git(ctx, dir, args...)
		return strings.TrimSpace(string(b)), err
	}
	top, err := out(project.Root, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("the checkout is not in a git repository: %w", err)
	}
	if shallow, err := out(top, "rev-parse", "--is-shallow-repository"); err != nil || shallow != "false" {
		return nil, fmt.Errorf("the repository is shallow (or unreadable: %v): a merge check needs the full history git pins resolve in; run git fetch --unshallow", err)
	}
	commit, err := out(top, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("%s names no commit: %w", ref, err)
	}
	rel, err := filepath.Rel(mustEval(top), mustEval(project.Root))
	if err != nil {
		return nil, err
	}
	clone := filepath.Join(scratch, "clone")
	for _, args := range [][]string{
		{"clone", "--quiet", "--no-local", "--no-checkout", top, clone},
		{"-C", clone, "fetch", "--quiet", top, commit},
		{"-C", clone, "checkout", "--quiet", "--detach", commit},
	} {
		dir := scratch
		if args[0] == "-C" {
			dir, args = args[1], args[2:]
		}
		if _, err := git(ctx, dir, args...); err != nil {
			return nil, err
		}
	}
	cloned, err := store.Discover(filepath.Join(clone, rel))
	if err != nil {
		return nil, fmt.Errorf("no ledger at %s (is whosaidso.toml committed?): %w", ref, err)
	}
	clonedLedger, _ := filepath.Rel(cloned.Root, cloned.Ledger)
	ledger, _ := filepath.Rel(project.Root, project.Ledger)
	if cloned.ID != project.ID || clonedLedger != ledger {
		return nil, fmt.Errorf("the commit's whosaidso.toml names project %q ledger %q, this checkout's %q ledger %q", cloned.ID, clonedLedger, project.ID, ledger)
	}
	a := &MergeAnswer{Ref: ref, Commit: commit}
	if err := mergeLedger(ctx, a, cloned, project, func(c string) bool {
		_, err := git(ctx, clone, "merge-base", "--is-ancestor", c, commit)
		return err == nil
	}); err != nil {
		return nil, err
	}
	a.Clean = a.ReplayError == "" && a.Artifacts == "" && len(a.GitPins) == 0 && len(a.SetAside) == 0 && len(a.NotCommitted) == 0
	return a, nil
}

// mergeLedger fills the answer from the cloned ledger.
func mergeLedger(ctx context.Context, a *MergeAnswer, cloned, here store.Project, reachable func(string) bool) error {
	state, err := store.Replayed(cloned)
	if err != nil {
		a.ReplayError = err.Error()
		return nil
	}
	bundles, err := state.Bundles()
	if err != nil {
		return err
	}
	a.Bundles = len(bundles)
	if err := HomeHoldsKeptArtifacts(ctx, cloned, state); err != nil {
		a.Artifacts = err.Error()
	}
	snapshot := state.Snapshot()
	resolver := evidence.NewResolverAt(cloned.Root, cloned.ArtifactDir())
	seen := map[model.GitPin]bool{}
	for _, b := range bundles {
		for i, raw := range b.Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				return err
			}
			for _, ref := range admissionArtifacts(event) {
				if ref.Git == nil || seen[*ref.Git] || disposed(snapshot, ref) {
					continue
				}
				seen[*ref.Git] = true
				pin := fmt.Sprintf("%s:%s cited by bundle %d event %d (%s)", ref.Git.Commit, ref.Git.Path, b.Sequence, i, raw.Type)
				gitOnly := model.ArtifactRef{Kind: "git", Selector: model.Selector{Kind: "whole"}, Git: ref.Git}
				if _, err := resolver.Resolve(ctx, gitOnly); err != nil {
					a.GitPins = append(a.GitPins, pin+": does not resolve: "+err.Error())
				} else if !reachable(ref.Git.Commit) {
					a.GitPins = append(a.GitPins, pin+": not reachable from the commit (squashed, rebased or on an unmerged branch)")
				}
			}
		}
	}
	a.SetAside = snapshot.SetAside()
	mine, err := store.ReadPrefix(here)
	if err != nil {
		return err
	}
	in := map[model.BundleRef]bool{}
	for _, b := range bundles {
		in[b.Ref()] = true
	}
	for _, b := range mine {
		if !in[b.Ref()] {
			a.NotCommitted = append(a.NotCommitted, fmt.Sprintf("sequence %d command %s", b.Sequence, b.CommandID))
		}
	}
	return nil
}

// mustEval resolves symlinks, so a checkout reached through one (macOS
// /var, /tmp) is placed at the same path in the clone; a path that cannot be
// resolved is used as given.
func mustEval(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}
