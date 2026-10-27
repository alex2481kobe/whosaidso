package write

// The ledger check (whosaidso check ledger), run before pushing a merge: is
// the ledger at this checkout's HEAD whole for anyone who clones it? In the
// checkout itself, with the cache off and nothing read from any home or
// intake, it replays the ledger and lists:
//   - bundles and cited content artifacts not committed at HEAD (git's
//     tracked tree, so a file only in the working tree counts as missing);
//   - cited content artifacts whose bytes the store does not hold
//     (HomeHoldsKeptArtifacts);
//   - git pins that do not resolve, or that HEAD cannot reach, so a squash,
//     a rebase or an unmerged branch would orphan them;
//   - set-aside bundles no setaside.resolve has dealt with.
// A recorded disposal excuses the bytes it names, as in admission.

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alex2481kobe/whosaidso/internal/evidence"
	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

// LedgerCheck is the finding. Clean is true only when the ledger replayed
// and every problem list is empty; resolved set-asides are listed, not owed.
type LedgerCheck struct {
	Commit          string                 `json:"commit"`
	Bundles         int                    `json:"bundles"`
	Clean           bool                   `json:"clean"`
	ReplayError     string                 `json:"replay_error,omitempty"`
	NotCommitted    []string               `json:"not_committed,omitempty"`
	Artifacts       string                 `json:"artifacts_missing,omitempty"`
	GitPins         []string               `json:"git_pins,omitempty"`
	SetAside        []reduce.SetAside      `json:"set_aside,omitempty"`
	Resolved        []reduce.SetAside      `json:"set_aside_resolved,omitempty"`
	Classes         map[string]LedgerClass `json:"classes"`
	GitPinLocations []GitPinLocation       `json:"git_pin_locations,omitempty"`
	CheckError      string                 `json:"check_error,omitempty"`
}

// CheckLedger checks project's ledger at its checkout's HEAD.
func CheckLedger(ctx context.Context, project store.Project) (answer *LedgerCheck, checkErr error) {
	session := evidence.NewGitSession(ctx, project.Root)
	defer func() {
		if err := session.Close(); err != nil && checkErr == nil {
			answer, checkErr = nil, err
		}
	}()
	git := func(args ...string) (string, error) {
		b, err := session.Run(ctx, project.Root, args...)
		return strings.TrimSpace(string(b)), err
	}
	top, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("the checkout is not in a git repository: %w", err)
	}
	if shallow, err := git("rev-parse", "--is-shallow-repository"); err != nil || shallow != "false" {
		return nil, fmt.Errorf("the repository is shallow (or unreadable: %v): git pins need the full history; run git fetch --unshallow", err)
	}
	head, err := git("rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return nil, fmt.Errorf("the checkout has no commit: %w", err)
	}
	a := &LedgerCheck{Commit: head, Classes: unmeasuredLedgerClasses()}
	a.Classes["replay_error"] = measuredLedgerClass(nil)
	state, err := store.Replayed(project)
	if err != nil {
		a.ReplayError = err.Error()
		a.Classes["replay_error"] = measuredLedgerClass([]string{a.ReplayError})
		return a, nil
	}
	bundles, err := state.Bundles()
	if err != nil {
		return nil, err
	}
	a.Bundles = len(bundles)
	ledgerDir, artifactDir := relTo(top, project.Ledger), relTo(top, filepath.Join(project.Root, project.ArtifactDir()))
	tracked, err := git("-C", top, "ls-tree", "-r", "--name-only", head, "--", ledgerDir, artifactDir)
	if err != nil {
		return nil, err
	}
	committed := map[string]bool{}
	for _, path := range strings.Split(tracked, "\n") {
		committed[path] = true
	}
	notCommitted := func(path, what string) {
		if !committed[path] {
			a.NotCommitted = append(a.NotCommitted, what+" "+path)
		}
	}
	problems, err := keptArtifactProblems(ctx, project, state)
	if err != nil {
		return nil, err
	}
	if err := keptArtifactsError(project, problems); err != nil {
		a.Artifacts = err.Error()
	}
	var missing []string
	for _, p := range problems {
		missing = append(missing, string(p.digest))
	}
	a.Classes["artifacts_missing"] = measuredLedgerClass(missing)
	var pinProblems []string
	snapshot := state.Snapshot()
	seen := map[string]bool{}
	located := map[string]bool{}
	for _, b := range bundles {
		name, err := model.BundleName(b.Sequence, b.CommandID)
		if err != nil {
			return nil, err
		}
		notCommitted(ledgerDir+"/"+name, "bundle")
		for i, raw := range b.Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				return nil, err
			}
			for _, ref := range admissionArtifacts(event) {
				if disposed(snapshot, ref) {
					continue
				}
				if ref.Kind == "content" && !seen[string(ref.Content.SHA256)] {
					seen[string(ref.Content.SHA256)] = true
					notCommitted(artifactDir+"/"+string(ref.Content.SHA256), "artifact")
				}
				if ref.Git == nil || seen[ref.Git.Commit+":"+ref.Git.Path] {
					continue
				}
				seen[ref.Git.Commit+":"+ref.Git.Path] = true
				pin := fmt.Sprintf("%s:%s cited by bundle %d event %d (%s)", ref.Git.Commit, ref.Git.Path, b.Sequence, i, raw.Type)
				gitOnly := model.ArtifactRef{Kind: "git", Selector: model.Selector{Kind: "whole"}, Git: ref.Git}
				resolveErr, batchErr := resolveLedgerPin(ctx, project, gitOnly)
				if batchErr != nil {
					a.CheckError = "git pin resolution UNKNOWN: " + batchErr.Error()
				}
				if resolveErr != nil {
					a.GitPins = append(a.GitPins, pin+": UNKNOWN: does not resolve: "+resolveErr.Error())
					pinProblems = append(pinProblems, ref.Git.Commit+":"+ref.Git.Path)
				}
				// A missing path still has a locatable commit. Missing or non-commit
				// objects cannot establish reachability, so do not invent locations.
				commitErr := evidence.RequireGitCommit(ctx, session.Run, project.Root, ref.Git.Commit)
				if commitErr == nil {
					if _, err := git("merge-base", "--is-ancestor", ref.Git.Commit, head); err != nil {
						var exit *exec.ExitError
						if !errors.As(err, &exit) || exit.ExitCode() != 1 || len(exit.Stderr) != 0 {
							a.CheckError = fmt.Sprintf("git pin ancestry unmeasured: %v", err)
							continue
						}
						if resolveErr == nil {
							a.GitPins = append(a.GitPins, pin+": HEAD cannot reach it (squashed, rebased or on an unmerged branch)")
							pinProblems = append(pinProblems, ref.Git.Commit+":"+ref.Git.Path)
						}
						if !located[ref.Git.Commit] {
							located[ref.Git.Commit] = true
							a.GitPinLocations = append(a.GitPinLocations, ledgerPinLocation(ctx, session, project.Root, ref.Git.Commit))
						}
					}
				}
			}
		}
	}
	var asides []string
	for _, v := range snapshot.SetAside() {
		if v.Resolved != nil {
			a.Resolved = append(a.Resolved, v)
		} else {
			a.SetAside = append(a.SetAside, v)
			asides = append(asides, fmt.Sprintf("%d:%s", v.Sequence, v.CommandID))
		}
	}
	a.Classes["set_asides"] = measuredLedgerClass(asides)
	if a.CheckError == "" {
		a.Classes["git_pins"] = measuredLedgerClass(pinProblems)
	}
	a.Classes["not_committed"] = measuredLedgerClass(a.NotCommitted)
	a.Clean = a.CheckError == "" && a.Artifacts == "" && len(a.NotCommitted) == 0 && len(a.GitPins) == 0 && len(a.SetAside) == 0
	return a, nil
}

// relTo is path relative to the repository top, slash-separated as git
// prints it; symlinks are resolved on both sides first (macOS /var, /tmp).
func relTo(top, path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	} else if dir, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		path = filepath.Join(dir, filepath.Base(path))
	}
	if resolved, err := filepath.EvalSymlinks(top); err == nil {
		top = resolved
	}
	rel, err := filepath.Rel(top, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
