package reduce

// setaside.resolve: someone dealt with a set-aside bundle. The first
// resolution in ledger order is kept on the set-aside entry; one naming a
// bundle that is not set aside, or already resolved, changes nothing and is
// never refused, so resolutions recorded on two branches merge cleanly. A
// resolved bundle stays in the ledger and stays unapplied; reads count it
// apart from those still owed (internal/query/view_todo.go).

import "github.com/alex2481kobe/whosaidso/internal/model"

// SetAsideResolution is the admitted setaside.resolve that dealt with a
// set-aside bundle.
type SetAsideResolution struct {
	Reason string `json:"reason"`
	Origin Origin `json:"origin"`
}

func (s *state) resolveSetAside(o Origin, e *model.SetAsideResolve) {
	entry, ok := s.setAside[e.Bundle]
	if !ok || entry.Resolved != nil {
		return
	}
	entry.Resolved = &SetAsideResolution{Reason: e.Reason, Origin: o}
	put(s, s.setAside, e.Bundle, entry)
}
