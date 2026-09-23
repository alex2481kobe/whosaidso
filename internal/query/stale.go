package query

// The stale-claims read of `datum show --stale` (R14.2) lives here: which
// observed claims to answer, each one's last run, the HEAD-only comparison and
// the brief lines. The git facts come from the caller (StaleGit), observed in
// the invoking checkout through internal/evidence; this package never runs git.

import (
	"datum/internal/model"
	"datum/internal/reduce"
)

// StaleClaim answers one observed current claim. Stale is TRUE when scoped
// files changed between the commit its last run recorded and the checkout's
// HEAD, FALSE when git compared and found none, and UNKNOWN when the
// comparison could not be made (an unknown or dirty run head, no readable
// HEAD, no scope source paths, a commit git cannot find), with Reason saying which.
type StaleClaim struct {
	Claim        model.RecordRef     `json:"claim"`
	LastRun      model.InvocationRef `json:"last_run"`
	Stale        reduce.Truth        `json:"stale"`
	RunHead      *model.GitHead      `json:"run_head,omitempty"`
	CurrentHead  *model.GitHead      `json:"current_head,omitempty"`
	ChangedPaths []string            `json:"changed_paths"`
	Reason       string              `json:"reason,omitempty"`
}

// StaleGit is the git the stale read needs, supplied by the caller from the
// invoking checkout: its HEAD, and which files under a scope differ between
// two commits. The checkout's dirty state is deliberately absent: the
// comparison is HEAD-only, and its blind spot says so.
type StaleGit struct {
	Head    model.Availability[model.GitHead]
	Changes func(from, to model.GitHead, scope []string) ([]string, error)
}

// staleClaims answers every current claim with at least one completed local
// observation; an unobserved claim has no last run to be stale against. The
// last run is the one whose seal the ledger admitted last.
//
// BLIND TO: uncommitted changes (HEAD is compared, not the working tree), and
// code outside the claim's scope source paths.
func staleClaims(s reduce.Snapshot, git *StaleGit) []StaleClaim {
	out := []StaleClaim{}
	for _, claim := range s.Claims() {
		last, ok := lastRun(claim.Observations)
		if !ok {
			continue
		}
		answer := StaleClaim{Claim: model.RecordRef{Project: claim.Claim.Project, RecordID: claim.Claim.ID, Revision: claim.Claim.Revision},
			LastRun: model.InvocationRef{Project: last.Key.Project, InvocationID: last.Key.InvocationID}, Stale: reduce.TruthUnknown, ChangedPaths: []string{}}
		out = append(out, staleAnswer(answer, last, claim.Spec.Scope.SourcePaths, git))
	}
	return out
}

func staleAnswer(answer StaleClaim, last reduce.Invocation, scope []string, git *StaleGit) StaleClaim {
	identity := last.Seal.ExecutionSourceIdentity
	switch {
	case identity.Head.State != model.Known || identity.Dirty.State != model.Known || *identity.Dirty.Value:
		answer.Reason = "the last run's commit is not known clean, so what it measured cannot be compared"
		return answer
	case git.Head.State != model.Known:
		answer.Reason = "this checkout's HEAD is unknown: " + git.Head.Reason
		return answer
	case len(scope) == 0:
		answer.Reason = "the claim's scope names no source paths"
		return answer
	}
	answer.RunHead, answer.CurrentHead = identity.Head.Value, git.Head.Value
	changed, err := git.Changes(*identity.Head.Value, *git.Head.Value, scope)
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

func briefStale(b *briefWriter, indent int, v cur) {
	b.line(indent, "CLAIM", v.at("claim", "record_id"), "rev", v.at("claim", "revision"), "stale", v.at("stale"),
		"last run", v.at("last_run", "invocation_id"), "changed paths", count(v.at("changed_paths")))
	if v.at("reason").ok() {
		b.line(indent+1, prefix(v.at("reason")))
	}
}
