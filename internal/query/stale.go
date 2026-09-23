package query

// The stale-claims section of `datum state --stale` (R14.2) lives here: its
// answer shape, the seam the caller fills, and its brief lines. Deciding
// staleness needs git, so it lives in internal/write (stale_claims.go) and is
// reached only when the caller asks; this package never runs git.

import (
	"datum/internal/model"
	"datum/internal/reduce"
)

// StaleClaim is one observed current claim: whether code under its scope
// changed between the commit its last run recorded and the checkout's HEAD.
// Its fields match write.StaleClaim exactly, so the caller converts one into
// the other and the compiler keeps the two shapes identical.
type StaleClaim struct {
	Claim        model.RecordRef     `json:"claim"`
	LastRun      model.InvocationRef `json:"last_run"`
	Stale        reduce.Truth        `json:"stale"`
	RunHead      *model.GitHead      `json:"run_head,omitempty"`
	CurrentHead  *model.GitHead      `json:"current_head,omitempty"`
	ChangedPaths []string            `json:"changed_paths"`
	Reason       string              `json:"reason,omitempty"`
}

// StaleCheck answers staleness against the exact snapshot the answer reads,
// so the section can never describe a different prefix than the rest.
type StaleCheck func(reduce.Snapshot) []StaleClaim

func briefStale(b *briefWriter, indent int, v cur) {
	b.line(indent, "CLAIM", v.at("claim", "record_id"), "rev", v.at("claim", "revision"), "stale", v.at("stale"),
		"last run", v.at("last_run", "invocation_id"), "changed paths", count(v.at("changed_paths")))
	if v.at("reason").ok() {
		b.line(indent+1, prefix(v.at("reason")))
	}
}
