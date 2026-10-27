// Scalar read projections avoid copying specs, proofs or source payloads for
// list screens. Status comes from the same predicates as complete projections.
package reduce

import "github.com/alex2481kobe/whosaidso/internal/model"

type RecordHead struct {
	Key    RecordKey
	Kind   model.Kind
	Text   string
	Origin Origin
}
type RecordSummary struct {
	RecordHead
	Status    string
	NextActor *model.Actor
}
type SourceHead struct {
	Key     SourceKey
	Speaker model.Actor
	Length  uint64
	Origin  Origin
}

func headOf(r Record) RecordHead {
	text, _ := r.SemanticText()
	return RecordHead{Key: r.Key, Kind: r.Kind, Text: text, Origin: r.Origin}
}

func (s Snapshot) RecordHeads() []RecordHead {
	st := s.inner()
	out := []RecordHead{}
	for _, r := range st.recordsSorted() {
		if st.current[Ident{Project: r.Key.Project, ID: r.Key.ID}] == r.Key.Revision {
			out = append(out, headOf(r))
		}
	}
	return out
}
func (s Snapshot) RecordSummary(id Ident) (RecordSummary, bool) {
	st := s.inner()
	r, ok := st.currentRecord(id)
	if !ok {
		return RecordSummary{}, false
	}
	row := RecordSummary{RecordHead: headOf(r)}
	switch r.Kind {
	case model.Task:
		if p, ok := st.task(id); ok {
			row.Status = string(p.Status)
			actor := p.NextActor
			row.NextActor = &actor
		}
	case model.Claim:
		if p, ok := s.claimAt(asRef(r.Key)); ok {
			row.Status = string(p.Status)
		}
	case model.Decision:
		if p, ok := s.decisionAt(asRef(r.Key)); ok {
			row.Status = string(p.Status)
		}
	}
	return row, true
}
func (s Snapshot) SourceHeads() []SourceHead {
	out := []SourceHead{}
	for _, src := range s.inner().sourcesSorted() {
		out = append(out, SourceHead{Key: src.Key, Speaker: src.Intake.Speaker, Length: src.Intake.Length, Origin: src.Origin})
	}
	return out
}
