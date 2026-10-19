package reduce

// An act that cites an authority (a receipt for another holder's attempt, a
// closure, a prerequisite waiver) stands on that authority's words, a
// source.intake. The admission gate checks the words exist and name the act's
// target, so on one line of history they always do. A merge can set those
// words aside afterwards (their message order was taken on a branch merged
// first), and the act citing them is a separate bundle replay would otherwise
// keep applying. Like the other branch clashes (branch_clash.go), this is
// asked only of a bundle that did not see everything before it, the only kind
// whose words a merge can take away: that the words are applied, before the
// act or in its own bundle. What they say and name stays the gate's check.

import (
	"github.com/alex2481kobe/whosaidso/internal/model"
)

// authorityApplied refuses an act whose cited authority has no applied
// source.intake by that speaker carrying that source.
func (s *state) authorityApplied(b model.Bundle, idx int, field string, a *model.Authority) error {
	if a == nil || s.trial == nil {
		return nil
	}
	carries := func(src model.SourceIntake) bool {
		return src.Speaker == a.Actor && sameArtifact(src.SourceRef, a.SourceRef)
	}
	for _, event := range s.bundle.events {
		if src, ok := event.(*model.SourceIntake); ok && carries(*src) {
			return nil
		}
	}
	for _, src := range s.sources {
		if carries(src.Intake) {
			return nil
		}
	}
	return faultAt(CodeAuthorityUnavailable, b.Sequence, idx, field,
		"no applied source.intake carries the cited authority's words: the source was set aside, so the act citing it is too")
}
