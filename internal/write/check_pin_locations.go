package write

// Local ref containment for unreachable ledger pins lives here. Ref presence
// never makes the pin reachable from HEAD or changes its problem class.

import (
	"context"
	"strings"

	"github.com/alex2481kobe/whosaidso/internal/evidence"
)

// GitPinLocation lists local branches and remote-tracking refs, without fetching.
// An empty Refs with no Error means no such ref contains this commit; Error
// means containment could not be measured.
type GitPinLocation struct {
	Commit string   `json:"commit"`
	Refs   []string `json:"refs"`
	Error  string   `json:"error,omitempty"`
}

func ledgerPinLocation(ctx context.Context, session *evidence.GitSession, root, commit string) GitPinLocation {
	location := GitPinLocation{Commit: commit, Refs: []string{}}
	out, err := session.Run(ctx, root, "for-each-ref", "--contains="+commit, "--format=%(refname)", "refs/heads/", "refs/remotes/")
	if err != nil {
		location.Error = err.Error()
		return location
	}
	for _, ref := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if ref != "" {
			location.Refs = append(location.Refs, ref)
		}
	}
	return location
}
