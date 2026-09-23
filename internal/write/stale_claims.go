package write

// The R14.2 stale-claims read lives here: for each current claim that has
// been observed, whether code under its scope changed between the commit its
// last run recorded and this checkout's HEAD, so re-measuring happens when it
// matters. It needs git at read time, so it is a separate read and never part
// of the fold or of any other answer. Admission's code-change check lives in
// gate_code_change.go.

import (
	"context"

	"datum/internal/evidence"
	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

// StaleClaim answers one claim. Stale is TRUE when scoped files changed since
// the last run, FALSE when git compared and found none, and UNKNOWN when the
// comparison could not be made (an unknown or dirty run head, no scope source
// paths, no readable HEAD, a commit git cannot find), with Reason saying which.
type StaleClaim struct {
	Claim        model.RecordRef     `json:"claim"`
	LastRun      model.InvocationRef `json:"last_run"`
	Stale        reduce.Truth        `json:"stale"`
	RunHead      *model.GitHead      `json:"run_head,omitempty"`
	CurrentHead  *model.GitHead      `json:"current_head,omitempty"`
	ChangedPaths []string            `json:"changed_paths"`
	Reason       string              `json:"reason,omitempty"`
}

// StaleClaims reads every current claim with at least one completed local
// observation; an unobserved claim has no last run to be stale against. The
// last run is the one whose seal the ledger admitted last.
//
// BLIND TO: uncommitted changes (HEAD is compared, not the working tree), and
// code outside the claim's scope source paths.
func StaleClaims(ctx context.Context, project store.Project, snapshot reduce.Snapshot) []StaleClaim {
	current, _ := runGitState(ctx, project, gateGit)
	out := []StaleClaim{}
	for _, claim := range snapshot.Claims() {
		last, ok := lastRun(claim.Observations)
		if !ok {
			continue
		}
		answer := StaleClaim{Claim: model.RecordRef{Project: claim.Claim.Project, RecordID: claim.Claim.ID, Revision: claim.Claim.Revision},
			LastRun: model.InvocationRef{Project: last.Key.Project, InvocationID: last.Key.InvocationID}, Stale: reduce.TruthUnknown, ChangedPaths: []string{}}
		out = append(out, staleAnswer(ctx, project, answer, last, claim.Spec.Scope.SourcePaths, current))
	}
	return out
}

func staleAnswer(ctx context.Context, project store.Project, answer StaleClaim, last reduce.Invocation, scope []string, current model.Availability[model.GitHead]) StaleClaim {
	identity := last.Seal.ExecutionSourceIdentity
	switch {
	case identity.Head.State != model.Known || identity.Dirty.State != model.Known || *identity.Dirty.Value:
		answer.Reason = "the last run's commit is not known clean, so what it measured cannot be compared"
		return answer
	case current.State != model.Known:
		answer.Reason = "this checkout's HEAD is unknown: " + current.Reason
		return answer
	case len(scope) == 0:
		answer.Reason = "the claim's scope names no source paths"
		return answer
	}
	answer.RunHead, answer.CurrentHead = identity.Head.Value, current.Value
	changed, err := evidence.ScopeChanges(ctx, gateGit, project.Root, *identity.Head.Value, *current.Value, scope)
	if err != nil {
		answer.Reason = err.Error()
		return answer
	}
	answer.ChangedPaths, answer.Stale = changed, reduce.TruthFalse
	if len(changed) > 0 {
		answer.Stale = reduce.TruthTrue
	}
	return answer
}

func lastRun(observations []reduce.Invocation) (reduce.Invocation, bool) {
	var last reduce.Invocation
	found := false
	for _, inv := range observations {
		if inv.Sealed == nil {
			continue
		}
		if !found || inv.Sealed.Sequence > last.Sealed.Sequence ||
			inv.Sealed.Sequence == last.Sealed.Sequence && inv.Sealed.EventIndex > last.Sealed.EventIndex {
			last, found = inv, true
		}
	}
	return last, found
}
