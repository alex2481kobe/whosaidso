package query

// This file turns reduced facts into answer shapes: actors, a revision's task
// view, sources and the history origins of a record. Selecting which facts to
// read is the views'; reading pending intake stays in query.go.

import (
	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
)

func actor(a model.Actor) any {
	if a.ID == "" {
		return Unknown{State: "UNKNOWN", Reason: a.UnknownReason}
	}
	return a
}

// taskOf is the described revision's task view, so a closure node for an
// exact older revision never shows the current revision's prerequisites.
func taskOf(s reduce.Snapshot, ref model.RecordRef) *Task {
	p, ok := s.TaskAt(ref)
	if !ok {
		return nil
	}
	outcome := string(p.Outcome)
	if outcome == "" {
		outcome = "UNKNOWN"
	}
	t := &Task{Revision: p.Task.Revision, Status: p.Status, Outcome: outcome,
		Attempts: p.Attempts, AttemptHolders: []Holder{}, Blockers: p.Blockers, Prerequisites: p.Prerequisites,
		Reasons: p.Reasons, WaitingActors: p.WaitingActors, ExpectedNextActor: actor(p.NextActor), CommitsDenied: p.CommitsDenied}
	if p.Closure != nil {
		t.Closure = &TaskClosure{Closure: p.Closure, AuthorityCited: p.Closure.Authority != nil}
	}
	for _, attempt := range p.Attempts {
		if attempt.Live() {
			t.AttemptHolders = append(t.AttemptHolders, Holder{attempt.Key, actor(attempt.Actor)})
		}
	}
	return t
}

// sourcesIn lists every captured source naming the identity as a referent.
func sourcesIn(all []reduce.Source, id reduce.Ident) []reduce.Source {
	out := []reduce.Source{}
	for _, source := range all {
		for _, target := range source.Intake.Referents {
			if target.Project == id.Project && target.RecordID == id.ID {
				out = append(out, source)
				break
			}
		}
	}
	return out
}

// History is all revisions plus events that explicitly reference those
// revisions, including receipts naming a claim through their criterion reference
// or owned by this task's attempts.
// It is not a recursive traversal of neighboring records or inferred sources.
func historyOrigins(s reduce.Snapshot, id reduce.Ident) map[reduce.Origin]bool {
	origins := map[reduce.Origin]bool{}
	for _, record := range s.Records() {
		if record.Key.Project != id.Project || record.Key.ID != id.ID {
			continue
		}
		origins[record.Origin] = true
		ref := model.RecordRef{Project: id.Project, RecordID: id.ID, Revision: record.Key.Revision}
		for _, edge := range s.RecordReferrers(ref) {
			origins[edge.Origin] = true
		}
	}
	if src, ok := sourceOf(s.Sources(), id); ok {
		origins[src.Origin] = true
	}
	for _, invocation := range s.Invocations() {
		criterion := invocation.Start.CriterionRef.Value
		namesClaim := criterion != nil && criterion.Claim.Project == id.Project && criterion.Claim.RecordID == id.ID
		if namesClaim || invocation.Attempt.Project == id.Project && invocation.Attempt.Task == id.ID {
			origins[invocation.Started] = true
			if invocation.Sealed != nil {
				origins[*invocation.Sealed] = true
			}
		}
	}
	return origins
}
