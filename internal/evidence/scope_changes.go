package evidence

// Reading which paths under a scope differ between two commits lives here,
// for R14.2: the admission gate verifies a proof's recorded code change with
// it, and the stale-claims read asks it of a claim's last run. Both commits
// are read from the object database, never the working tree. Deciding what a
// change permits lives in internal/reduce and internal/write.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"datum/internal/model"
)

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
