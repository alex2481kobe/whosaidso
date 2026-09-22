package query

// This file turns reduced facts into answer shapes: actors, records and the
// history origins a record description needs. Selecting which facts to read,
// and reading pending intake, stay in query.go.

import (
	"datum/internal/model"
	"datum/internal/reduce"
)

func actor(a model.Actor) any {
	if a.ID == "" {
		return Unknown{State: "UNKNOWN", Reason: a.UnknownReason}
	}
	return a
}

func describe(s reduce.Snapshot, fact reduce.Record) Record {
	id := reduce.Ident{Project: fact.Key.Project, ID: fact.Key.ID}
	r := Record{Fact: fact, Author: s.EventAuthor(fact.Origin), Sources: []reduce.Source{}}
	if p, ok := s.Task(id); ok {
		outcome := string(p.Outcome)
		if outcome == "" {
			outcome = "UNKNOWN"
		}
		r.Task = &Task{Revision: p.Task.Revision, Status: p.Status, Outcome: outcome, Closure: p.Closure,
			Attempts: p.Attempts, AttemptHolders: []Holder{}, Blockers: p.Blockers, Prerequisites: p.Prerequisites,
			Reasons: p.Reasons, WaitingActors: p.WaitingActors, ExpectedNextActor: actor(p.NextActor), CommitsDenied: p.CommitsDenied}
		for _, attempt := range p.Attempts {
			if attempt.Live() {
				r.Task.AttemptHolders = append(r.Task.AttemptHolders, Holder{attempt.Key, actor(attempt.Actor)})
			}
		}
	}
	ref := model.RecordRef{Project: fact.Key.Project, RecordID: fact.Key.ID, Revision: fact.Key.Revision}
	r.Supersessions = supersessionsOf(s, ref)
	// No evidence bytes or real-world scope were checked by this read slice.
	support, _ := s.Support(ref, reduce.SupportContext{EvidenceAvailable: reduce.TruthUnknown, ScopeApplicable: reduce.TruthUnknown})
	if p, ok := s.Claim(id); ok {
		p.Support = support
		r.Claim, r.CurrentSupport = &p, support.Current()
	}
	if p, ok := s.Decision(id); ok {
		p.Support = support
		r.Decision, r.CurrentSupport = &p, support.Current()
	}
	if p, ok := s.Instrument(id); ok {
		p.Support = support
		r.Instrument, r.CurrentSupport = &p, support.Current()
	}
	for _, source := range s.Sources() {
		for _, target := range source.Intake.Referents {
			if target.Project == id.Project && target.RecordID == id.ID {
				r.Sources = append(r.Sources, source)
				break
			}
		}
	}
	return r
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
