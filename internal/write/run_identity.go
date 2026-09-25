package write

// Observing a run's execution identity lives here: the persistent machine id and
// the invoking checkout's git HEAD and dirty state, each KNOWN only when observed.
// Launching the process, the producer report and comparability rules do not.

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/alex2481kobe/whosaidso/internal/evidence"
	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

// RunExecutionIdentity observes where and from what a run executes. Every field
// it cannot observe is UNKNOWN with the reason, never a default: an unknown
// machine must not compare equal to any other machine.
//
// BLIND TO: source the run reads from outside the project root, and changes
// inside WhoSaidSo's own ledger and artifact directories, which are records of runs
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
	identity.Head, identity.Dirty = runGitState(ctx, project)
	return identity
}

// runGitState applies the run's policy to the raw observation of the invoking
// checkout, which is what executes, whichever checkout holds the home ledger:
// a HEAD it cannot read leaves dirty UNKNOWN for the same reason, and a dirty
// tree is recorded UNKNOWN, never dirty=true, because no source was captured.
func runGitState(ctx context.Context, project store.Project) (model.Availability[model.GitHead], model.Availability[bool]) {
	root := project.ExecRoot()
	head := evidence.CheckoutHead(ctx, evidence.ExecGit, root)
	if head.State != model.Known {
		return head, model.Availability[bool]{State: model.Unknown, Reason: head.Reason}
	}
	// The project directory only, less WhoSaidSo's own record folders, at the same
	// project-relative place in this checkout as in the home.
	pathspec := []string{"."}
	ledger, err := filepath.Rel(project.Root, project.Ledger)
	if err != nil {
		ledger = "."
	}
	for _, own := range []string{filepath.Join(root, ledger), filepath.Join(root, filepath.FromSlash(project.ArtifactDir()))} {
		if rel, err := filepath.Rel(root, own); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			pathspec = append(pathspec, ":(exclude)"+filepath.ToSlash(rel))
		}
	}
	dirty := evidence.CheckoutDirty(ctx, evidence.ExecGit, root, pathspec...)
	if dirty.State == model.Known && *dirty.Value {
		return head, model.Availability[bool]{State: model.Unknown, Reason: "the checkout differs from HEAD, and whosaidso run captures no source content, which recording dirty=true requires"}
	}
	return head, dirty
}
