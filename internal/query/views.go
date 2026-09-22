package query

// Per-record views for the read presets: instruments, claims and decisions,
// plus the rendered label. Each view is built from one exact admitted revision
// and says only what the reducer projected. Preset routing, runs, closure and
// continuation live in their own files; nothing here reads prose for meaning.

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"datum/internal/model"
	"datum/internal/reduce"
)

// Attention is a fact the reader must not miss, surfaced first in a preset.
type Attention struct {
	Kind   string          `json:"kind"`
	Ref    model.RecordRef `json:"ref"`
	Label  string          `json:"label"`
	Reason string          `json:"reason"`
}

// Validation is KNOWN only when a validation ref/version was recorded. Anything
// else is UNKNOWN with a reason; it is never blank and never assumed.
type Validation struct {
	State   string             `json:"state"`
	Ref     *model.ArtifactRef `json:"ref,omitempty"`
	Version string             `json:"version,omitempty"`
	Reason  string             `json:"reason,omitempty"`
}

type InstrumentView struct {
	Ref               model.RecordRef          `json:"ref"`
	Label             string                   `json:"label"`
	Validation        Validation               `json:"validation"`
	Trust             reduce.Truth             `json:"trust"`
	Withdrawals       []reduce.TrustWithdrawal `json:"withdrawals"`
	QuestionAnswered  string                   `json:"question_answered"`
	BlindTo           string                   `json:"blind_to"`
	NotAnswered       string                   `json:"not_answered"`
	ConfigSurface     []string                 `json:"config_surface"`
	DangerousDefaults []string                 `json:"dangerous_defaults"`
	ValidRange        string                   `json:"valid_range"`
	ImplementationRef model.ArtifactRef        `json:"implementation_ref"`
	Author            any                      `json:"author"`
}

type ClaimView struct {
	Ref            model.RecordRef             `json:"ref"`
	Label          string                      `json:"label"`
	Status         reduce.ClaimStatus          `json:"status"`
	Standing       string                      `json:"standing"`
	Missing        []string                    `json:"missing"`
	Assertion      string                      `json:"assertion"`
	Falsifier      string                      `json:"falsifier"`
	Scope          model.Scope                 `json:"scope"`
	ExternalRefs   []model.ExternalReference   `json:"external_refs"`
	Author         any                         `json:"author"`
	Observations   []RunView                   `json:"observations"`
	Proofs         []reduce.ProofAdmission     `json:"proofs"`
	Support        reduce.SupportFacts         `json:"support"`
	CurrentSupport reduce.Truth                `json:"current_support"`
	Supersessions  []reduce.Supersession       `json:"supersessions"`
	Corrections    []reduce.AdmittedCorrection `json:"corrections"`
}

type DecisionView struct {
	Ref            model.RecordRef              `json:"ref"`
	Label          string                       `json:"label"`
	Status         reduce.DecisionStatus        `json:"status"`
	Authorizes     string                       `json:"authorizes,omitempty"`
	Question       string                       `json:"question"`
	Options        []string                     `json:"options"`
	WaitingActor   any                          `json:"waiting_actor"`
	Scope          model.Scope                  `json:"scope"`
	Author         any                          `json:"author"`
	Rulings        []reduce.DecisionDisposition `json:"rulings"`
	CurrentSupport reduce.Truth                 `json:"current_support"`
	Supersessions  []reduce.Supersession        `json:"supersessions"`
	Corrections    []reduce.AdmittedCorrection  `json:"corrections"`
}

// label renders a record's name from its own semantic text. The model has no
// label override field, so there is nothing else to prefer. Never a link key.
func label(text string) string {
	line := strings.TrimSpace(strings.SplitN(text, "\n", 2)[0])
	if utf8.RuneCountInString(line) > 80 {
		line = string([]rune(line)[:79]) + "…"
	}
	if line == "" {
		return "UNKNOWN: no semantic text to label from"
	}
	return line
}

func readSupport(s reduce.Snapshot, ref model.RecordRef) reduce.SupportFacts {
	// This read checks no evidence bytes and no real-world scope.
	f, _ := s.Support(ref, reduce.SupportContext{EvidenceAvailable: reduce.TruthUnknown, ScopeApplicable: reduce.TruthUnknown})
	return f
}

func validation(v model.Availability[model.InstrumentValidation]) Validation {
	if v.State == model.Known && v.Value != nil {
		ref := v.Value.Ref
		return Validation{State: "KNOWN", Ref: &ref, Version: v.Value.Version}
	}
	reason := strings.TrimSpace(v.Reason)
	if reason == "" {
		reason = fmt.Sprintf("validation availability %q carries no recorded value or reason", v.State)
	}
	return Validation{State: "UNKNOWN", Reason: reason}
}

func instrumentView(s reduce.Snapshot, p reduce.InstrumentProjection) (InstrumentView, []Attention) {
	ref, spec := asRef(p.Instrument), p.Spec
	rec, _ := s.Record(ref)
	v := InstrumentView{Ref: ref, Label: label(spec.QuestionAnswered), Validation: validation(spec.Validation),
		Trust: readSupport(s, ref).ActiveTrust, Withdrawals: nonNil(p.Withdrawals),
		QuestionAnswered: spec.QuestionAnswered, BlindTo: spec.BlindTo, NotAnswered: spec.NotAnswered,
		ConfigSurface: nonNil(spec.ConfigSurface), DangerousDefaults: nonNil(spec.DangerousDefaults),
		ValidRange: spec.ValidRange, ImplementationRef: spec.ImplementationRef, Author: actor(rec.Provenance.Author)}
	var notes []Attention
	if v.Validation.State != "KNOWN" {
		notes = append(notes, Attention{"instrument-validation-unknown", ref, v.Label, v.Validation.Reason})
	}
	// UNKNOWN trust that only follows from UNKNOWN validation is already said above.
	if v.Trust == reduce.TruthFalse || v.Trust != reduce.TruthTrue && v.Validation.State == "KNOWN" {
		notes = append(notes, Attention{"instrument-trust-not-active", ref, v.Label, fmt.Sprintf("active trust is %s", v.Trust)})
	}
	return v, notes
}

// standing is derived from status alone, so an unproven claim cannot read as established.
func standing(p reduce.ClaimProjection, current reduce.Truth) (string, []string) {
	switch p.Status {
	case reduce.StatusProven:
		return fmt.Sprintf("PROVEN at revision %d; current support is %s", p.Claim.Revision, current), []string{}
	case reduce.StatusMeasured:
		return "MEASURED: observed locally, no admitted proof; not established", []string{"admitted proof"}
	}
	return "UNMEASURED: asserted only, never observed locally; not established", []string{"local observation", "admitted proof"}
}

func claimView(s reduce.Snapshot, p reduce.ClaimProjection) ClaimView {
	ref, spec := asRef(p.Claim), p.Spec
	rec, _ := s.Record(ref)
	support := readSupport(s, ref)
	v := ClaimView{Ref: ref, Label: label(spec.Assertion), Status: p.Status, Assertion: spec.Assertion,
		Falsifier: spec.Falsifier, Scope: spec.Scope, ExternalRefs: nonNil(spec.ExternalRefs),
		Author: actor(rec.Provenance.Author), Observations: []RunView{}, Proofs: nonNil(p.Proofs),
		Support: support, CurrentSupport: support.Current(),
		Supersessions: supersessionsOf(s, ref), Corrections: correctionsOf(s, ref)}
	v.Standing, v.Missing = standing(p, v.CurrentSupport)
	for _, inv := range p.Observations {
		v.Observations = append(v.Observations, runView(s, inv))
	}
	return v
}

func decisionView(s reduce.Snapshot, p reduce.DecisionProjection) DecisionView {
	ref, spec := asRef(p.Decision), p.Spec
	rec, _ := s.Record(ref)
	v := DecisionView{Ref: ref, Label: label(spec.Question), Status: p.Status, Question: spec.Question,
		Options: nonNil(spec.Options), WaitingActor: actor(spec.WaitingActor), Scope: spec.Scope,
		Author: actor(rec.Provenance.Author), Rulings: nonNil(p.Dispositions),
		CurrentSupport: readSupport(s, ref).Current(),
		Supersessions:  supersessionsOf(s, ref), Corrections: correctionsOf(s, ref)}
	if p.Status == reduce.StatusOpen {
		v.Authorizes = "nothing: an open decision authorises no work"
	}
	return v
}

func asRef(k reduce.RecordKey) model.RecordRef {
	return model.RecordRef{Project: k.Project, RecordID: k.ID, Revision: k.Revision}
}

func nonNil[T any](xs []T) []T {
	if xs == nil {
		return []T{}
	}
	return xs
}

func same(a, b model.RecordRef) bool { return a.Project == b.Project && a.RecordID == b.RecordID }

// supersessionsOf lists every supersession naming this record identity on either side.
func supersessionsOf(s reduce.Snapshot, ref model.RecordRef) []reduce.Supersession {
	out := []reduce.Supersession{}
	for _, e := range s.Supersessions() {
		if same(e.Supersede.Prior, ref) || same(e.Supersede.Replacement, ref) {
			out = append(out, e)
		}
	}
	return out
}

// correctionsOf lists every correction whose typed target or affected revisions name this record.
func correctionsOf(s reduce.Snapshot, ref model.RecordRef) []reduce.AdmittedCorrection {
	out := []reduce.AdmittedCorrection{}
	for _, c := range s.Corrections() {
		if len(correctionTouches(c.Correction, ref)) > 0 {
			out = append(out, c)
		}
	}
	return out
}

// correctionTouches returns every record ref the correction names, if any of them is ref's record.
func correctionTouches(c model.Correction, ref model.RecordRef) []model.RecordRef {
	named := append([]model.RecordRef{}, c.AffectedRevisions...)
	switch {
	case c.Target.Record != nil:
		named = append(named, *c.Target.Record)
	case c.Target.Criterion != nil:
		named = append(named, c.Target.Criterion.Claim)
	case c.Target.Support != nil:
		named = append(named, c.Target.Support.Dependent)
	}
	for _, n := range named {
		if same(n, ref) {
			return named
		}
	}
	return nil
}
