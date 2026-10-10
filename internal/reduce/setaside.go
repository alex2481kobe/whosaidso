package reduce

// Where a split chain clashed: which bundles replay keeps but does not apply,
// why, and the reads that report them. The chain structure that decides
// whether a bundle saw everything before it is lineage.go's; the semantic
// rules a bundle can fail are everywhere else in this package.

import (
	"errors"
	"fmt"
	"sort"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// CodeUnseenFact is a bundle set aside because one of its events names a fact
// its branch never saw: published by a bundle it does not descend from.
const CodeUnseenFact = "unseen-fact"

// SetAside is a published bundle replay kept but did not apply, because it was
// recorded on a branch that did not see a bundle before it and it fails a
// rule against what is now there. It stays in the ledger; nothing it carries
// is in any answer. Code and Reason are the fault that set it aside.
type SetAside struct {
	Sequence  uint64   `json:"sequence"`
	CommandID model.ID `json:"command_id"`
	Code      string   `json:"code"`
	Reason    string   `json:"reason"`
}

// fold applies b's events, or sets b aside.
//
// A bundle that descends from every bundle before it was admitted against
// exactly this state, so any refusal is damage and stays fatal, as on a
// ledger that never split. Only a bundle that did not see everything before
// it can be set aside, and then for one of three reasons: its command id is
// already published on another branch, one of its events names a fact its
// branch never saw (unseen), or one of its events fails a rule here. Its
// events are tried (trial.go), so a refusal leaves no partial write.
func (s *state) fold(b model.Bundle, place placement) error {
	if place.descendsAll {
		return s.applyEvents(b)
	}
	if place.duplicate != nil {
		s.setAsideBundle(b, faultAt(CodeDuplicateCommand, b.Sequence, -1, "bundle.command_id",
			fmt.Sprintf("the same admission is already published at sequence %d on another branch", place.duplicate.Sequence)))
		return nil
	}
	if err := s.try(b); err != nil {
		s.setAsideBundle(b, err)
	}
	return nil
}

// unseen refuses, while a bundle is on trial, a fact one of its events names
// by identity (a record revision, criterion, invocation, hold or attempt)
// when any part of that fact was published by a bundle the tried one does not
// descend from. Its admitter never saw that fact: a revision number or id
// that matches one on its own branch can name different content here, and
// applying the event would rewrite or rely on what it never read. A bundle
// that names only what its own branch published is applied. Facts a rule
// derives (whether a task is ready, a family closed) are not named, and are
// judged against the state as merged.
func (s *state) unseen(b model.Bundle, idx int, path, what string, origins ...Origin) error {
	if s.trial == nil {
		return nil
	}
	for _, o := range origins {
		if !s.lineage.sees(s.trial.viewer, o.Sequence) {
			by := s.lineage.at(o.Sequence)
			return faultAt(CodeUnseenFact, b.Sequence, idx, path,
				fmt.Sprintf("%s was published at sequence %d by command %s, on a branch this bundle did not see",
					what, by.Sequence, by.CommandID))
		}
	}
	return nil
}

// unseenUnlessAny refuses, on trial, when none of the places a fact was
// published is one the bundle's branch saw; the answer does not depend on
// the order they are listed in.
func (s *state) unseenUnlessAny(b model.Bundle, idx int, path, what string, origins []Origin) error {
	if s.trial == nil {
		return nil
	}
	first := origins[0]
	for _, o := range origins {
		if s.lineage.sees(s.trial.viewer, o.Sequence) {
			return nil
		}
		if o.Sequence < first.Sequence {
			first = o
		}
	}
	return s.unseen(b, idx, path, what, first)
}

// optional is an origin that may not exist yet, as a list.
func optional(o *Origin) []Origin {
	if o == nil {
		return nil
	}
	return []Origin{*o}
}

func (s *state) setAsideBundle(b model.Bundle, err error) {
	s.setAside[b.Ref()] = SetAside{Sequence: b.Sequence, CommandID: b.CommandID, Code: faultCode(err), Reason: err.Error()}
}

// faultCode is the machine-readable code of a refusal.
func faultCode(err error) string {
	var f *model.Fault
	if errors.As(err, &f) {
		return f.Code
	}
	var c *Conflict
	if errors.As(err, &c) {
		return c.Code()
	}
	return CodeInvalidField
}

// SetAside returns every set-aside bundle in canonical order: empty on a
// ledger that never split or merged without a clash.
func (s Snapshot) SetAside() []SetAside {
	st := s.inner()
	out := make([]SetAside, 0, len(st.setAside))
	for _, v := range st.setAside {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		return model.BundleRef{Sequence: out[i].Sequence, CommandID: out[i].CommandID}.Before(
			model.BundleRef{Sequence: out[j].Sequence, CommandID: out[j].CommandID})
	})
	return out
}

// SetAsideBundle reports whether one bundle of the prefix was set aside.
func (s Snapshot) SetAsideBundle(ref model.BundleRef) (SetAside, bool) {
	v, ok := s.inner().setAside[ref]
	return v, ok
}
