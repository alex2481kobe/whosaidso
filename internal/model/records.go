package model

// Authored record specifications, scope, and authority live here.
// Observed execution facts and executable proof criteria live in their own files.
// This file is below 200 lines to keep those separate contracts out of record specs.

import (
	"fmt"
)

// Scope is consumed by evaluation and proof for applicability and by the views
// for traversal. Source
// paths only select candidates; the authored applicability and limits do the work.
type Scope struct {
	SourcePaths []string    `json:"source_paths"`
	ContextRefs []RecordRef `json:"context_refs"`
	AppliesWhen string      `json:"applies_when" semantic:"text"`
	Limitations string      `json:"limitations" semantic:"text"`
}

func (s Scope) validate(p string) error { return relativePaths(s.SourcePaths, p+".source_paths") }

// Authority lets proof resolve an actor's exact words within their actual scope.
// SourceRef pins the complete source; Selector selects the ruling within it.
type Authority struct {
	Actor     Actor       `json:"actor"`
	SourceRef ArtifactRef `json:"source_ref"`
	Selector  Selector    `json:"selector"`
	Scope     Scope       `json:"scope"`
}

func (a Authority) validate(p string) error {
	if a.SourceRef.Selector.Kind != "whole" {
		return invalid(p+".source_ref.selector", "authority pins the whole source before selecting the ruling")
	}
	return nil
}

// TaskSpec feeds prerequisites/revisions, acceptance and continuation, and
// context/constraint reads. Revision events retain explicit provenance
// so pure replay never needs to fetch an intake packet to recover an author.
type TaskSpec struct {
	Intent             string                `json:"intent" semantic:"text"`
	Subject            string                `json:"subject" semantic:"text"`
	Scope              Scope                 `json:"scope"`
	NonGoals           []string              `json:"non_goals" semantic:"texts"`
	AcceptanceCriteria []AcceptanceCriterion `json:"acceptance_criteria"`
	ContextRefs        []RecordRef           `json:"context_refs"`
	ConstraintRefs     []RecordRef           `json:"constraint_refs"`
	Prerequisites      []Prerequisite        `json:"prerequisites"`
	NextActor          Actor                 `json:"next_actor"`
	// Accepter, when named, is the only actor whose packet may close
	// the task. Absent, anyone may close it; the closer is recorded either way.
	Accepter *Actor        `json:"accepter,omitempty"`
	Progress *TaskProgress `json:"progress,omitempty"`
}

// AcceptanceCriterion belongs to the task, independently of CLAIM proof criteria.
type AcceptanceCriterion struct {
	ID        ID       `json:"id"`
	Revision  Revision `json:"revision"`
	Criterion string   `json:"criterion" semantic:"text"`
}

// TaskProgress gives acceptance and the views witnessed progress and a concrete continuation.
type TaskProgress struct {
	Summary     string        `json:"summary,omitempty" semantic:"text"`
	NextAction  string        `json:"next_action,omitempty" semantic:"text"`
	WitnessRefs []ArtifactRef `json:"witness_refs"`
}

func (p TaskProgress) validate(at string) error {
	// Both fields are optional, so absent is fine. Present-but-whitespace is
	// not: a summary of " " reads as recorded progress and says nothing. This
	// is the same defect as a minLength rule a single space satisfies, which is
	// the bug this whole system exists to catch.
	if p.Summary != "" && Blank(p.Summary) {
		return invalid(at+".summary", "present but blank")
	}
	if p.NextAction != "" && Blank(p.NextAction) {
		return invalid(at+".next_action", "present but blank")
	}
	if Blank(p.Summary) && Blank(p.NextAction) {
		return invalid(at, "progress needs a summary or next action")
	}
	return nil
}

// Prerequisite gives task reduction a typed predicate at an exact revision. A consumer that
// accepts a waiver must declare it here and supply the authority for doing so.
type Prerequisite struct {
	Kind         string     `json:"kind"`
	Target       RecordRef  `json:"target"`
	WaiverPolicy string     `json:"waiver_policy"`
	Authority    *Authority `json:"authority,omitempty"`
}

func (s TaskSpec) validate(p string) error {
	if len(s.NonGoals) == 0 || len(s.AcceptanceCriteria) == 0 {
		return invalid(p, "non-goals and acceptance criteria must be explicit")
	}
	// Two unknowns never match, so an unknown accepter could never close.
	if s.Accepter != nil && Blank(s.Accepter.ID) {
		return invalid(p+".accepter", "an accepter is a known actor; leave it out to let anyone accept")
	}
	seen := map[ID]bool{}
	for i, c := range s.AcceptanceCriteria {
		if seen[c.ID] {
			return invalid(fmt.Sprintf("%s.acceptance_criteria[%d].id", p, i), "duplicate acceptance criterion")
		}
		seen[c.ID] = true
	}
	return nil
}
func (r Prerequisite) validate(p string) error {
	if err := oneOf(r.Kind, p+".kind", "task-success", "claim-proof", "decision-approved"); err != nil {
		return err
	}
	switch r.WaiverPolicy {
	case "forbid":
		if r.Authority != nil {
			return invalid(p+".authority", "forbid cannot carry waiver authority")
		}
	case "allow-with-authority":
		if r.Authority == nil {
			return invalid(p+".authority", "waiver policy requires authority")
		}
	default:
		return invalid(p+".waiver_policy", "unknown waiver policy")
	}
	return nil
}

// ClaimSpec feeds assertion history and proof applicability. External
// references are context for the views; their tags cannot stand in for local observations.
type ClaimSpec struct {
	Assertion    string              `json:"assertion" semantic:"text"`
	Falsifier    string              `json:"falsifier" semantic:"text"`
	Scope        Scope               `json:"scope"`
	ExternalRefs []ExternalReference `json:"external_refs"`
}
type ExternalReference struct {
	Tag       string       `json:"tag"`
	Citation  string       `json:"citation" semantic:"text"`
	SourceRef *ArtifactRef `json:"source_ref,omitempty"`
	RecordRef *RecordRef   `json:"record_ref,omitempty"`
}

func (r ExternalReference) validate(p string) error {
	return oneOf(r.Tag, p+".tag", "VERIFIED", "VENDOR CLAIM", "REPORTED MEASUREMENT")
}

// DecisionSpec feeds revision history, proof authority and the waiting views.
// A disposition can only enter through decision.dispose.
type DecisionSpec struct {
	Question     string   `json:"question" semantic:"text"`
	Options      []string `json:"options" semantic:"texts"`
	WaitingActor Actor    `json:"waiting_actor"`
	Scope        Scope    `json:"scope"`
}

func (s DecisionSpec) validate(p string) error {
	if len(s.Options) == 0 {
		return invalid(p+".options", "at least one option is required")
	}
	return nil
}

// InstrumentSpec supplies evaluation validity, run capture/configuration, proof
// trust, and instrument discovery in the views. No validation observation is inferred from a declaration.
type InstrumentSpec struct {
	QuestionAnswered  string                             `json:"question_answered" semantic:"text"`
	BlindTo           string                             `json:"blind_to" semantic:"text"`
	NotAnswered       string                             `json:"not_answered" semantic:"text"`
	ConfigSurface     []string                           `json:"config_surface" semantic:"texts"`
	DangerousDefaults []string                           `json:"dangerous_defaults" semantic:"texts"`
	ValidRange        string                             `json:"valid_range" semantic:"text"`
	ImplementationRef ArtifactRef                        `json:"implementation_ref"`
	Validation        Availability[InstrumentValidation] `json:"validation"`
}
type InstrumentValidation struct {
	Ref     ArtifactRef `json:"ref"`
	Version string      `json:"version" semantic:"text"`
}

func (s InstrumentSpec) validate(p string) error {
	seen := map[string]bool{}
	for i, n := range s.ConfigSurface {
		if stripInvisible(n) == "status" || seen[n] {
			return invalid(fmt.Sprintf("%s.config_surface[%d]", p, i), "reserved or duplicate configuration name")
		}
		seen[n] = true
	}
	return nil
}
