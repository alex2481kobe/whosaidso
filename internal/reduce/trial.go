package reduce

// A bundle on trial, and the one helper every state write of the fold goes
// through. Replay tries a bundle that did not see everything before it and
// keeps it only if every event applies (setaside.go). While it is on trial
// each write remembers what it replaced, so a refusal is undone in time and
// memory proportional to the bundle's own writes, never by copying the state.

import "github.com/alex2481kobe/whosaidso/internal/model"

// journal is how to undo one bundle's writes. Map writes are undone entry by
// entry, newest first. The event log only grows, so its slice headers taken
// before the trial restore it; so does a dry run's refusal count.
type journal struct {
	undo     []func()
	log      eventLog
	refusals int
}

// put writes m[k] = v. On trial it first remembers the entry it replaces.
func put[K comparable, V any](s *state, m map[K]V, k K, v V) {
	if j := s.trial; j != nil {
		old, had := m[k]
		j.undo = append(j.undo, func() {
			if had {
				m[k] = old
			} else {
				delete(m, k)
			}
		})
	}
	m[k] = v
}

// try applies b's events, or undoes every write they made and returns the
// refusal, leaving the state exactly as it was.
func (s *state) try(b model.Bundle) error {
	j := &journal{log: s.log}
	if s.proofRefusals != nil {
		j.refusals = len(*s.proofRefusals)
	}
	s.trial = j
	err := s.applyEvents(b)
	s.trial = nil
	if err == nil {
		return nil
	}
	for i := len(j.undo) - 1; i >= 0; i-- {
		j.undo[i]()
	}
	s.log = j.log
	if s.proofRefusals != nil {
		*s.proofRefusals = (*s.proofRefusals)[:j.refusals]
	}
	return err
}
