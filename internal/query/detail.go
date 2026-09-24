package query

// The one record-detail representation the views share (todo, continue,
// show): the admitted fact with its full spec and provenance, who wrote and
// who admitted it, and the kind's presentation (task status and reasons,
// claim standing, decision rulings, instrument validation and trust). A
// claim names its observation runs by id; the answer carries each run's body
// once, in its runs section. Which records a view selects lives in the view
// files; this file only describes one exact revision.

import (
	"fmt"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
)

// Detail is one exact admitted revision. Exactly one of Task, Claim,
// Decision or Instrument is set, by Fact.Kind.
type Detail struct {
	Ref             model.RecordRef      `json:"ref"`
	Label           string               `json:"label"`
	CurrentRevision model.Revision       `json:"current_revision"`
	Fact            reduce.Record        `json:"fact"`   // kind, spec, provenance, origin
	Author          reduce.PacketAuthor  `json:"author"` // who wrote the packet that carried Fact
	AdmittedBy      any                  `json:"admitted_by"`
	SelfAdmitted    string               `json:"self_admitted"` // true, false or UNKNOWN
	Task            *Task                `json:"task,omitempty"`
	Claim           *ClaimDetail         `json:"claim,omitempty"`
	Decision        *DecisionView        `json:"decision,omitempty"`
	Instrument      *InstrumentView      `json:"instrument,omitempty"`
	Support         *reduce.SupportFacts `json:"support,omitempty"` // not for tasks
	CurrentSupport  reduce.Truth         `json:"current_support,omitempty"`
	// SupportUnknownBecause says, for each premise that makes CurrentSupport
	// UNKNOWN, why this read could not decide it; empty otherwise.
	SupportUnknownBecause []string                    `json:"support_unknown_because,omitempty"`
	Supersessions         []reduce.Supersession       `json:"supersessions"` // either side; the superseded record stays shown
	Corrections           []reduce.AdmittedCorrection `json:"corrections"`
	Sources               []reduce.Source             `json:"sources"`
}

// ClaimDetail is the claim view with its observations named by invocation id.
// This Observations shadows the embedded one under the same key.
type ClaimDetail struct {
	ClaimView
	Observations []model.ID `json:"observations"`
}

// RunDetail is one run: its derived view (duration, scope check) and the
// admitted start and seal envelopes it was derived from.
type RunDetail struct {
	RunView
	Start model.InvocationEnvelope  `json:"start"`
	Seal  *model.InvocationEnvelope `json:"seal"`
}

func runDetail(s reduce.Snapshot, inv reduce.Invocation) RunDetail {
	return RunDetail{RunView: runView(s, inv), Start: inv.Start, Seal: inv.Seal}
}

// detailer describes records against lists it reads from the snapshot once,
// so describing every record costs one copy of each list, not one per record.
type detailer struct {
	s             reduce.Snapshot
	sources       []reduce.Source
	supersessions []reduce.Supersession
	corrections   []reduce.AdmittedCorrection
}

func newDetailer(s reduce.Snapshot) *detailer {
	return &detailer{s: s, sources: s.Sources(), supersessions: s.Supersessions(), corrections: s.Corrections()}
}

var kindLabel = map[model.Kind]func(reduce.Record) string{
	model.Task:       func(r reduce.Record) string { return r.Task.Intent },
	model.Claim:      func(r reduce.Record) string { return r.Claim.Assertion },
	model.Decision:   func(r reduce.Record) string { return r.Decision.Question },
	model.Instrument: func(r reduce.Record) string { return r.Instrument.QuestionAnswered },
}

// detail describes one exact revision. The attention it returns is the
// instrument's (UNKNOWN validation, inactive trust); other kinds have none.
func (d *detailer) detail(fact reduce.Record) (Detail, []Attention) {
	s, ref := d.s, asRef(fact.Key)
	id := reduce.Ident{Project: fact.Key.Project, ID: fact.Key.ID}
	v := Detail{Ref: ref, Fact: fact, Author: s.EventAuthor(fact.Origin),
		Supersessions: supersessionsIn(d.supersessions, ref), Corrections: correctionsIn(d.corrections, ref),
		Sources: sourcesIn(d.sources, id)}
	v.Label = "UNKNOWN: no semantic text to label from"
	if text, ok := kindLabel[fact.Kind]; ok {
		v.Label = label(text(fact))
	}
	v.CurrentRevision, _ = s.CurrentRevision(id)
	v.AdmittedBy, v.SelfAdmitted = admission(s, v.Author)
	var notes []Attention
	if fact.Kind == model.Task {
		v.Task = taskOf(s, ref)
		return v, notes
	}
	// No evidence bytes or real-world scope were checked by this read.
	support := readSupport(s, ref)
	v.Support, v.CurrentSupport = &support, support.Current()
	if v.CurrentSupport == reduce.TruthUnknown {
		v.SupportUnknownBecause = unknownPremises(support)
	}
	author := s.EventAuthor(fact.Origin).Author
	switch fact.Kind {
	case model.Claim:
		if p, ok := s.ClaimAt(ref); ok {
			c := &ClaimDetail{ClaimView: claimViewFrom(p, author, support, v.Supersessions, v.Corrections), Observations: []model.ID{}}
			for _, inv := range p.Observations {
				c.Observations = append(c.Observations, inv.Key.InvocationID)
			}
			v.Claim = c
		}
	case model.Decision:
		if p, ok := s.DecisionAt(ref); ok {
			dv := decisionViewFrom(p, author, v.CurrentSupport, v.Supersessions, v.Corrections)
			v.Decision = &dv
		}
	case model.Instrument:
		if p, ok := s.InstrumentAt(ref); ok {
			iv, n := instrumentViewFrom(p, author, support.ActiveTrust)
			v.Instrument, notes = &iv, n
		}
	}
	return v, notes
}

// admission names the actor whose review admitted the packet that carried the
// fact, and whether that actor also wrote it. An event no review attributes to
// a packet has neither: both are UNKNOWN with the reason, never guessed.
func admission(s reduce.Snapshot, author reduce.PacketAuthor) (any, string) {
	if author.Packet == "" {
		why := author.Author.UnknownReason
		if why == "" {
			why = "the ledger does not attribute this event to a reviewed packet"
		}
		return unknown(why), "UNKNOWN"
	}
	review, ok := s.Review(reduce.ReviewKey{Project: s.Project(), CommandID: author.Packet})
	if !ok {
		return unknown("no admitted review of packet " + string(author.Packet)), "UNKNOWN"
	}
	self := string(review.SelfAdmission)
	if review.SelfAdmission == model.SelfAdmissionUnknown || self == "" {
		self = "UNKNOWN"
	}
	return actor(review.Actor), self
}

// recordKey addresses one exact revision in an answer's records map.
func recordKey(ref model.RecordRef) string {
	return fmt.Sprintf("%s@%d", ref.RecordID, ref.Revision)
}
