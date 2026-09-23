package write

// Observing a run's execution identity lives here: the persistent machine id and
// the project root's git HEAD and dirty state, each KNOWN only when observed.
// Launching the process, the producer report and comparability rules do not.

import (
	"context"
	"path/filepath"
	"strings"

	"datum/internal/evidence"
	"datum/internal/model"
	"datum/internal/store"
)

// RunExecutionIdentity observes where and from what a run executes. Every field
// it cannot observe is UNKNOWN with the reason, never a default: an unknown
// machine must not compare equal to any other machine.
//
// BLIND TO: source the run reads from outside the project root, and changes
// inside Datum's own ledger and artifact directories, which are records of runs
// rather than the source a run executes. SourceRefs stays empty: this observer
// captures no source content, which is also why a dirty checkout is recorded as
// dirty-UNKNOWN (the schema requires captured source content beside dirty=true).
func RunExecutionIdentity(ctx context.Context, project store.Project) model.ExecutionIdentity {
	identity := model.ExecutionIdentity{Project: project.ID, SourceRefs: []model.ArtifactRef{}}
	if machine, err := store.MachineID(); err != nil {
		identity.MachineID = model.Availability[model.ID]{State: model.Unknown, Reason: "the persistent machine id could not be read or created: " + err.Error()}
	} else {
		identity.MachineID = runKnown(machine)
	}
	identity.Head, identity.Dirty = runGitState(ctx, project, evidence.ExecGit)
	return identity
}

func runGitState(ctx context.Context, project store.Project, git evidence.GitRunner) (model.Availability[model.GitHead], model.Availability[bool]) {
	unknown := func(reason string) (model.Availability[model.GitHead], model.Availability[bool]) {
		return model.Availability[model.GitHead]{State: model.Unknown, Reason: reason}, model.Availability[bool]{State: model.Unknown, Reason: reason}
	}
	root := project.Root
	inside, err := git(ctx, root, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(string(inside)) != "true" {
		return unknown("the project root is not a readable git checkout, so no HEAD or dirty state was observed")
	}
	format, err := git(ctx, root, "rev-parse", "--show-object-format")
	if err != nil {
		return unknown("git could not report the repository's object format: " + err.Error())
	}
	commit, err := git(ctx, root, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return unknown("the checkout has no readable HEAD commit")
	}
	head := model.GitHead{ObjectFormat: strings.TrimSpace(string(format)), Commit: strings.TrimSpace(string(commit))}
	if err := model.ValidateSchema(head); err != nil {
		return unknown("git reported a HEAD that is not a valid commit name: " + err.Error())
	}
	args := []string{"status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=none", "--", "."}
	for _, own := range []string{project.Ledger, filepath.Join(root, filepath.FromSlash(project.ArtifactDir()))} {
		if rel, err := filepath.Rel(root, own); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			args = append(args, ":(exclude)"+filepath.ToSlash(rel))
		}
	}
	status, err := git(ctx, root, args...)
	if err != nil {
		return runKnown(head), model.Availability[bool]{State: model.Unknown, Reason: "git could not report the checkout's status: " + err.Error()}
	}
	if len(status) > 0 {
		return runKnown(head), model.Availability[bool]{State: model.Unknown, Reason: "the checkout differs from HEAD, and datum run captures no source content, which recording dirty=true requires"}
	}
	return runKnown(head), runKnown(false)
}
