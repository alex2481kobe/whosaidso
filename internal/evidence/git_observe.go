package evidence

// The one raw git observer lives here: a checkout's HEAD, whether its working
// tree differs from HEAD, and which paths under a scope differ between two
// commits. Every caller passes the root it means (the invoking checkout, or the
// home repository for admission's code-change check). What a caller concludes
// from these facts (continue reports dirty, a run records dirty as UNKNOWN,
// the stale read ignores dirty) lives with the caller, never here.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// CheckoutHead is the commit checked out at root. It is KNOWN only when git
// reports a valid commit name and object format; otherwise UNKNOWN with the
// reason, never a default.
func CheckoutHead(ctx context.Context, git GitRunner, root string) model.Availability[model.GitHead] {
	unknown := func(reason string) model.Availability[model.GitHead] {
		return model.Availability[model.GitHead]{State: model.Unknown, Reason: reason}
	}
	if git == nil {
		git = ExecGit
	}
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
	return model.Availability[model.GitHead]{State: model.Known, Value: &head}
}

// CheckoutDirty is whether the working tree at root differs from HEAD:
// tracked edits, untracked files and submodule changes, limited to pathspec
// when one is given (the whole repository when none is). It is KNOWN
// whichever way git answers, and UNKNOWN only when git cannot report status.
//
// BLIND TO: ignored files.
func CheckoutDirty(ctx context.Context, git GitRunner, root string, pathspec ...string) model.Availability[bool] {
	if git == nil {
		git = ExecGit
	}
	args := []string{"status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=none"}
	if len(pathspec) > 0 {
		args = append(append(args, "--"), pathspec...)
	}
	status, err := git(ctx, root, args...)
	if err != nil {
		return model.Availability[bool]{State: model.Unknown, Reason: "git could not report the checkout's status: " + err.Error()}
	}
	dirty := len(status) > 0
	return model.Availability[bool]{State: model.Known, Value: &dirty}
}

// ScopeChanges lists, sorted and relative to root, every file under the scope
// paths that differs between from and to: added, deleted or modified, renames
// as a deletion and an addition. An empty scope selects nothing and asks git
// nothing, because a diff with no pathspec would list the whole repository.
//
// BLIND TO: uncommitted changes, and anything outside root or the scope.
func ScopeChanges(ctx context.Context, git GitRunner, root string, from, to model.GitHead, scope []string) ([]string, error) {
	if len(scope) == 0 {
		return []string{}, nil
	}
	if git == nil {
		git = ExecGit
	}
	format, err := git(ctx, root, "rev-parse", "--show-object-format")
	if err != nil {
		return nil, fault("unavailable", "git", "the repository could not be read: "+err.Error())
	}
	for _, head := range []model.GitHead{from, to} {
		if got := strings.TrimSpace(string(format)); got != head.ObjectFormat {
			return nil, fault("invalid-field", "git.object_format", fmt.Sprintf("commit %s is %s, the repository uses %s", head.Commit, head.ObjectFormat, got))
		}
		resolved, err := git(ctx, root, "rev-parse", "--verify", "--quiet", head.Commit+"^{commit}")
		if err != nil || strings.TrimSpace(string(resolved)) != head.Commit {
			return nil, fault("unavailable", "git.commit", "commit not found in this repository: "+head.Commit)
		}
	}
	// Literal pathspecs: a scope path is a path, never a glob or magic.
	args := append([]string{"--literal-pathspecs", "diff", "--no-ext-diff", "--no-renames", "--name-only", "-z", "--relative",
		from.Commit, to.Commit, "--"}, scope...)
	out, err := git(ctx, root, args...)
	if err != nil {
		return nil, fault("unavailable", "git.diff", "the commits could not be compared: "+err.Error())
	}
	seen := map[string]bool{}
	changed := []string{}
	for _, name := range strings.Split(string(out), "\x00") {
		if name != "" && !seen[name] {
			seen[name] = true
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return changed, nil
}
