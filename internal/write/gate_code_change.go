package write

// The git half of R14.2 lives here: a proof's recorded code change is admitted
// only when git, asked for the files under the claim's scope that differ
// between its two commits, answers exactly the recorded paths. The ledger
// half (the run's head, the scope, the current commit) is the reducer's and
// was checked when the proposal replayed. Git is asked in the home repository
// (project.Root), the ledger's; the read of stale claims, which asks the
// invoking checkout, lives in internal/query/stale.go.

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	"whosaidso/internal/evidence"
	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
)

// gateGit is the git the gate asks. Production runs the real binary.
var gateGit evidence.GitRunner = evidence.ExecGit

func gateCodeChange(ctx context.Context, project store.Project, after reduce.Snapshot, claim model.RecordRef, change model.CodeChange, path string) error {
	record, ok := after.Record(claim)
	if !ok || record.Claim == nil {
		return admissionFault("unknown-reference", "claim", "proof names no admitted claim")
	}
	changed, err := evidence.ScopeChanges(ctx, gateGit, project.Root, change.From, change.To, record.Claim.Scope.SourcePaths)
	if err != nil {
		return admissionFault("code-change-unverified", path, "git could not compare the commits: "+err.Error())
	}
	if len(changed) == 0 {
		return admissionFault("code-change-unverified", path, "no file under the claim's scope changed between the commits, so the run is still counterevidence")
	}
	recorded := append([]string{}, change.ChangedPaths...)
	sort.Strings(recorded)
	if !reflect.DeepEqual(recorded, changed) {
		return admissionFault("code-change-unverified", path+".changed_paths", fmt.Sprintf("git lists %v under the claim's scope, the proof records %v", changed, recorded))
	}
	return nil
}
