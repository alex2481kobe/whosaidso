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

// CodeBuiltOnSetAside is a bundle set aside because a bundle it was appended
// to was set aside.
const CodeBuiltOnSetAside = "built-on-set-aside"

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
// it can be set aside, and then for any of three reasons: a bundle it was
// appended to is set aside (it was recorded on top of facts this ledger does
// not hold, and a reference by revision number could otherwise land on the
// other branch's revision of the same record), its command id is already
// published on another branch, or one of its events fails a rule here. Its
// events are tried on a fork, so a refusal leaves no partial write.
func (s *state) fold(b model.Bundle, place placement) error {
	if place.descendsAll {
		return s.applyEvents(b)
	}
	if parent, ok := s.setAsideParent(place.parents); ok {
		s.setAsideBundle(b, faultAt(CodeBuiltOnSetAside, b.Sequence, -1, "bundle.predecessor",
			fmt.Sprintf("it was recorded on top of sequence %d command %s, which is set aside", parent.Sequence, parent.CommandID)))
		return nil
	}
	if place.duplicate != nil {
		s.setAsideBundle(b, faultAt(CodeDuplicateCommand, b.Sequence, -1, "bundle.command_id",
			fmt.Sprintf("the same admission is already published at sequence %d on another branch", place.duplicate.Sequence)))
		return nil
	}
	trial := s.clone()
	trial.proofRefusals = s.proofRefusals
	if err := trial.applyEvents(b); err != nil {
		s.setAsideBundle(b, err)
		return nil
	}
	*s = *trial
	return nil
}

func (s *state) setAsideParent(parents []model.BundleRef) (model.BundleRef, bool) {
	for _, p := range parents {
		if _, ok := s.setAside[p]; ok {
			return p, true
		}
	}
	return model.BundleRef{}, false
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
