package reduce

// The ledger's chain structure lives here, once: the canonical order bundles
// must arrive in, that no sequence number is missing, which bundles each one
// names as its parents, which bundles are heads (no later bundle descends from
// them), whether a bundle descends from everything before it, where the next
// admission appends, and one command id published twice. The fold and the
// store's raw ledger read both check a ledger through this, so they can never
// disagree about what a well-formed chain is. What an event means is not here.
//
// A chain may split: two branches that each admit from the same tail publish
// two bundles with one sequence number, and git merges both files. Every such
// ledger is read in one order, model.BundleRef.Before, which depends on the set
// of files alone.

import (
	"fmt"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// Lineage is the chain structure of a ledger prefix read in canonical order.
// The zero value is the empty ledger.
type Lineage struct {
	last  model.BundleRef
	heads []model.BundleRef // in canonical order; last is always the final one
	nodes map[model.BundleRef]lineageNode
	order []placed // by position: order[p-1] is the bundle at position p
	tips  []uint64 // by chain: the position of its newest member (ancestry.go)
	// clocks are the distinct clocks nodes name by index (ancestry.go).
	clocks [][]chainMark
	sites  map[model.ID][]CommandSite
}

// CommandSite is one bundle published under a command id: where, and with
// which request digest, so a repeated id can say whether the request repeated.
type CommandSite struct {
	Sequence uint64       `json:"sequence"`
	Digest   model.Digest `json:"digest"`
}

// placement is what the chain says about one added bundle.
type placement struct {
	// descendsAll: the bundle descends from every bundle before it in
	// canonical order, so the prefix it was admitted against is exactly the
	// prefix before it.
	descendsAll bool
	parents     []model.BundleRef
	// duplicate is an earlier bundle under the same command id that this one
	// does not descend from, if any.
	duplicate *CommandSite
}

func newLineage() Lineage {
	return Lineage{nodes: map[model.BundleRef]lineageNode{}, sites: map[model.ID][]CommandSite{}}
}

// Add checks that b may follow everything added so far and records it.
func (l *Lineage) Add(b model.Bundle) error {
	_, err := l.add(b)
	return err
}

func (l *Lineage) add(b model.Bundle) (placement, error) {
	ref := b.Ref()
	empty := l.last == model.BundleRef{}
	switch {
	case empty && b.Sequence != 1:
		return placement{}, discontinuity(b, "bundle.sequence", "sequence 1 is missing")
	case !empty && !l.last.Before(ref):
		return placement{}, discontinuity(b, "bundle.sequence",
			fmt.Sprintf("out of canonical order: follows sequence %d command %s", l.last.Sequence, l.last.CommandID))
	}
	parents, err := l.parentsOf(b, empty)
	if err != nil {
		return placement{}, err
	}
	place := placement{descendsAll: true, parents: parents}
	for _, h := range l.heads {
		if !containsRef(parents, h) {
			place.descendsAll = false
			break
		}
	}
	for _, site := range l.sites[b.CommandID] {
		earlier := l.nodes[model.BundleRef{Sequence: site.Sequence, CommandID: b.CommandID}]
		if l.anyReaches(parents, earlier.Position) {
			// One line of history admitted one command twice: no branch
			// explains it, so admission identity is ambiguous.
			return placement{}, duplicateCommand(b, site)
		}
		if place.duplicate == nil {
			site := site
			place.duplicate = &site
		}
	}
	l.record(b, parents, place.descendsAll)
	return place, nil
}

// anyReaches reports whether a bundle with these parents would descend from
// the earlier bundle at position pos: whether one of them is or does.
func (l *Lineage) anyReaches(parents []model.BundleRef, pos uint64) bool {
	for _, p := range parents {
		if l.reaches(l.nodes[p], pos) {
			return true
		}
	}
	return false
}

// parentsOf resolves the bundles b names as parents: none for the genesis
// bundle, otherwise the predecessor, which must be a bundle one sequence lower,
// and each other head, which must be an earlier bundle present here.
func (l *Lineage) parentsOf(b model.Bundle, empty bool) ([]model.BundleRef, error) {
	if b.Sequence == 1 {
		switch {
		case b.Predecessor != "" || len(b.OtherHeads) > 0:
			return nil, discontinuity(b, "bundle.predecessor", "the genesis bundle has no predecessor")
		case !empty:
			// Two first admissions are two histories, not two branches of one.
			return nil, faultAt(CodeLedgerFork, b.Sequence, -1, "bundle.sequence",
				fmt.Sprintf("sequence 1 is published twice, also by command %s: one ledger has one genesis", l.last.CommandID))
		}
		return nil, nil
	}
	predecessor := model.BundleRef{Sequence: b.Sequence - 1, CommandID: b.Predecessor}
	if _, ok := l.nodes[predecessor]; !ok {
		// Sequence alone only counts files. The predecessor is what makes the
		// chain say which bundle each one was actually appended to. This is also
		// the contiguity rule: after a hole, no bundle one sequence lower exists,
		// and a fold over an incomplete prefix is wrong in a way no later event
		// repairs.
		if b.Sequence-1 > l.last.Sequence {
			return nil, discontinuity(b, "bundle.sequence", fmt.Sprintf("sequence %d is missing", l.last.Sequence+1))
		}
		return nil, discontinuity(b, "bundle.predecessor",
			fmt.Sprintf("predecessor %s names no bundle at sequence %d", b.Predecessor, b.Sequence-1))
	}
	parents := append(make([]model.BundleRef, 0, 1+len(b.OtherHeads)), predecessor)
	for i, h := range b.OtherHeads {
		if _, ok := l.nodes[h]; !ok || h.Sequence >= b.Sequence {
			return nil, discontinuity(b, fmt.Sprintf("bundle.other_heads[%d]", i),
				fmt.Sprintf("names no earlier bundle at sequence %d command %s", h.Sequence, h.CommandID))
		}
		parents = append(parents, h)
	}
	// A head is a bundle nothing else descends from. One the predecessor or
	// another named head already descends from was never a head when this
	// bundle was admitted, so naming it is not what admission writes.
	for i, h := range b.OtherHeads {
		hp := l.nodes[h].Position
		for j, other := range parents {
			if j != i+1 && l.reaches(l.nodes[other], hp) {
				return nil, discontinuity(b, fmt.Sprintf("bundle.other_heads[%d]", i),
					fmt.Sprintf("sequence %d command %s is not a head: sequence %d command %s descends from it",
						h.Sequence, h.CommandID, other.Sequence, other.CommandID))
			}
		}
	}
	return parents, nil
}

// record adds b: every parent stops being a head and b becomes one. A head of
// the prefix that b descends from is one of b's parents (any other path to it
// would pass through a bundle that descends from it), so removing the parents
// is the whole update.
func (l *Lineage) record(b model.Bundle, parents []model.BundleRef, descendsAll bool) {
	if l.nodes == nil {
		*l = newLineage()
	}
	ref := b.Ref()
	pos := uint64(len(l.order)) + 1
	mark := l.markFor(parents, pos)
	node := l.nodeFor(parents, pos, descendsAll, mark)
	heads := make([]model.BundleRef, 0, len(l.heads)+1)
	for _, h := range l.heads {
		if !containsRef(node.Parents, h) {
			heads = append(heads, h)
		}
	}
	l.heads = append(heads, ref) // b is last in canonical order so far
	l.order = append(l.order, placed{Ref: ref, Mark: mark})
	l.nodes[ref] = node
	l.sites[b.CommandID] = append(append([]CommandSite(nil), l.sites[b.CommandID]...),
		CommandSite{Sequence: b.Sequence, Digest: b.RequestDigest})
	l.last = ref
}

// Chain stamps b with where an admission appends: after the canonical last
// bundle, one sequence higher, naming it as predecessor and every other head
// in OtherHeads, so the new bundle descends from everything before it. A
// ledger with one head gets no OtherHeads, exactly as before chains could
// split.
func (l *Lineage) Chain(b model.Bundle) model.Bundle {
	b.Sequence, b.Predecessor, b.OtherHeads = l.last.Sequence+1, l.last.CommandID, nil
	for _, h := range l.heads {
		if h != l.last {
			b.OtherHeads = append(b.OtherHeads, h)
		}
	}
	return b
}

func containsRef(refs []model.BundleRef, r model.BundleRef) bool {
	for _, x := range refs {
		if x == r {
			return true
		}
	}
	return false
}

func discontinuity(b model.Bundle, path, detail string) error {
	return faultAt(CodeLedgerDiscontinuity, b.Sequence, -1, path, detail)
}

func duplicateCommand(b model.Bundle, prior CommandSite) error {
	requests := "matching request digests"
	if prior.Digest != b.RequestDigest {
		requests = "different request digests"
	}
	return faultAt(CodeDuplicateCommand, b.Sequence, -1, "bundle.command_id",
		fmt.Sprintf("command id %s is published at sequences %d and %d with %s; admission identity is ambiguous",
			b.CommandID, prior.Sequence, b.Sequence, requests))
}

// fields are the lineage's persisted parts, named for the image.
func (l *Lineage) fields(prefix string) []persistedField {
	return []persistedField{{prefix + ".last", &l.last}, {prefix + ".heads", &l.heads},
		{prefix + ".nodes", &l.nodes}, {prefix + ".order", &l.order}, {prefix + ".tips", &l.tips},
		{prefix + ".clocks", &l.clocks}, {prefix + ".sites", &l.sites}}
}

// clone forks the lineage. Heads and site lists are replaced, never appended
// to in place, and a node never changes once recorded, so copying the maps is
// a real fork. The position and clock lists are capped so the fork's first
// append reallocates (a clock is never changed once listed); chain tips are
// rewritten in place, so they are copied.
func (l Lineage) clone() Lineage {
	return Lineage{last: l.last, heads: l.heads, nodes: copyMap(l.nodes), order: l.order[:len(l.order):len(l.order)],
		tips: append([]uint64(nil), l.tips...), clocks: l.clocks[:len(l.clocks):len(l.clocks)], sites: copyMap(l.sites)}
}
