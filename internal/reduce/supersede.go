package reduce

// Supersession rules that depend on replayed state live here: a record revision
// is superseded at most once, a supersession never closes a cycle, and one that
// affects an owner ruling names the authority it acts under. Whether the prior
// was canonical before its admission set is an admission rule (internal/write).
// Support loss from supersession lives in support.go and support_graph.go.

import (
	"fmt"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

const (
	// CodeAlreadySuperseded supersedes a revision an earlier supersession covers.
	CodeAlreadySuperseded = "already-superseded"
	// CodeSupersedeCycle would make a record, through replacements, supersede itself.
	CodeSupersedeCycle = "supersede-cycle"
	// CodeAuthorityUnavailable is an owner act without a named authority.
	CodeAuthorityUnavailable = "authority-unavailable"
)

// supersededBy returns the replacements standing over ref. A supersession of
// revision N also covers earlier revisions of that record, never its own
// replacement (the same coverage query/closure.go reads).
func (s *state) supersededBy(ref model.RecordRef) []model.RecordRef {
	out := []model.RecordRef{}
	for _, o := range s.log.supersessions {
		e, ok := s.events[o].(*model.Supersede)
		if ok && ident(e.Prior) == ident(ref) && ref.Revision <= e.Prior.Revision && e.Replacement != ref {
			out = append(out, e.Replacement)
		}
	}
	return out
}

// rulingAffected: an owner ruling is an admitted decision.dispose, and a
// supersession affects every ruled revision it covers.
func (s *state) rulingAffected(prior model.RecordRef) bool {
	for _, o := range s.log.decisionDisposals {
		e, ok := s.events[o].(*model.DecisionDispose)
		if ok && ident(e.Decision) == ident(prior) && e.Decision.Revision <= prior.Revision {
			return true
		}
	}
	return false
}

func (s *state) supersedeRules(b model.Bundle, idx int, e *model.Supersede) error {
	if by := s.supersededBy(e.Prior); len(by) > 0 {
		return faultAt(CodeAlreadySuperseded, b.Sequence, idx, "prior",
			fmt.Sprintf("%s revision %d is already superseded by %s revision %d", e.Prior.RecordID, e.Prior.Revision, by[0].RecordID, by[0].Revision))
	}
	// Following replacements from the new replacement must never reach the prior.
	seen := map[model.RecordRef]bool{}
	queue := []model.RecordRef{e.Replacement}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if ident(next) == ident(e.Prior) && next.Revision <= e.Prior.Revision {
			return faultAt(CodeSupersedeCycle, b.Sequence, idx, "replacement",
				fmt.Sprintf("%s already stands, through supersession, over %s revision %d", e.Replacement.RecordID, e.Prior.RecordID, e.Prior.Revision))
		}
		if seen[next] {
			continue
		}
		seen[next] = true
		queue = append(queue, s.supersededBy(next)...)
	}
	if s.rulingAffected(e.Prior) && (e.Authority == nil || model.Blank(e.Authority.Actor.ID)) {
		return faultAt(CodeAuthorityUnavailable, b.Sequence, idx, "authority",
			"superseding an owner ruling needs a named authority")
	}
	return nil
}
