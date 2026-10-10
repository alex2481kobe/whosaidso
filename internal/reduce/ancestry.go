package reduce

// Which bundles each bundle descends from, answered in constant time without
// keeping an ancestor set per bundle. The chain structure that feeds it is
// lineage.go's.
//
// Two facts make it cheap. A bundle that descends from every bundle before it
// in canonical order (as every bundle of a ledger that never split does)
// descends from every lower position, so one number, its frontier, answers
// for all of them. Above the frontier, the bundles split into chains: a
// bundle continues its predecessor's chain when the predecessor is that
// chain's newest member, and starts a new one otherwise. Within a chain each
// member descends from the one before it, so descending from member i means
// descending from members 0..i, and a bundle needs only the highest member
// it reaches in each chain above its frontier: its clock, one mark per branch
// it has seen since the last point that saw everything. Its own chain needs no
// mark: it reaches every earlier member of it.
//
// A bundle that only continues its parent's chain reaches what the parent
// reaches and itself, so it names the parent's clock. Clocks live once in a
// table and nodes name them by index, so a long line growing beside a branch
// that never rejoins adds no marks, in memory or in the persisted image.

import (
	"sort"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// lineageNode is one bundle's place in the chain.
type lineageNode struct {
	Parents  []model.BundleRef
	Position uint64 // in canonical order, from 1
	// Frontier is the highest position this bundle descends from along with
	// every position below it: its own when it descends from everything
	// before it.
	Frontier uint64
	// Clock names the clock in Lineage.clocks, from 1; 0 is the empty clock.
	// A clock holds, per chain, the highest member above Frontier this bundle
	// descends from, sorted by chain; its own chain is answered by its mark,
	// so a mark there is never read.
	Clock uint32
}

// chainMark is a member of a chain: which chain, and its index there.
type chainMark struct {
	Chain uint32
	Index uint32
}

// placed is what the lineage keeps per position: the bundle and its mark.
type placed struct {
	Ref  model.BundleRef
	Mark chainMark
}

// nodeFor computes the place a bundle with these parents and chain mark takes
// at position pos, adding its clock to the table when it is a new one.
func (l *Lineage) nodeFor(parents []model.BundleRef, pos uint64, descendsAll bool, mark chainMark) lineageNode {
	n := lineageNode{Parents: parents, Position: pos, Frontier: pos}
	if descendsAll {
		return n
	}
	// A parent's own position counts only when that parent saw everything,
	// and then its frontier is that position.
	n.Frontier = 0
	for _, p := range parents {
		n.Frontier = max(n.Frontier, l.nodes[p].Frontier)
	}
	if len(parents) == 1 {
		if pn := l.nodes[parents[0]]; l.order[pn.Position-1].Mark.Chain == mark.Chain {
			n.Clock = pn.Clock // the parent is the previous member of its own chain
			return n
		}
	}
	var clock []chainMark
	for _, p := range parents {
		pn := l.nodes[p]
		if pn.Position <= n.Frontier {
			continue // it and all it descends from lie at or below the frontier
		}
		clock = raise(clock, l.order[pn.Position-1].Mark)
		for _, m := range l.clockOf(pn) {
			clock = raise(clock, m)
		}
	}
	if len(clock) > 0 {
		l.clocks = append(l.clocks, clock)
		n.Clock = uint32(len(l.clocks))
	}
	return n
}

// clockOf is the clock a recorded node names.
func (l *Lineage) clockOf(n lineageNode) []chainMark {
	if n.Clock == 0 {
		return nil
	}
	return l.clocks[n.Clock-1]
}

// raise records that a clock reaches mark, keeping the highest index per chain.
func raise(clock []chainMark, mark chainMark) []chainMark {
	i := sort.Search(len(clock), func(i int) bool { return clock[i].Chain >= mark.Chain })
	if i < len(clock) && clock[i].Chain == mark.Chain {
		if clock[i].Index < mark.Index {
			clock[i].Index = mark.Index
		}
		return clock
	}
	clock = append(clock, chainMark{})
	copy(clock[i+1:], clock[i:])
	clock[i] = mark
	return clock
}

// reaches reports whether the recorded bundle n is, or descends from, the
// bundle at position pos.
func (l *Lineage) reaches(n lineageNode, pos uint64) bool {
	if pos == n.Position || pos <= n.Frontier {
		return true
	}
	if pos > n.Position {
		return false
	}
	// A chain's members are numbered in position order, so a member before n
	// on n's own chain is one n descends from.
	mark := l.order[pos-1].Mark
	if mark.Chain == l.order[n.Position-1].Mark.Chain {
		return true
	}
	clock := l.clockOf(n)
	i := sort.Search(len(clock), func(i int) bool { return clock[i].Chain >= mark.Chain })
	return i < len(clock) && clock[i].Chain == mark.Chain && clock[i].Index >= mark.Index
}

// sees reports whether the recorded bundle viewer is, or descends from, the
// bundle at position pos: whether what that bundle published was in front of
// whoever admitted viewer.
func (l *Lineage) sees(viewer model.BundleRef, pos uint64) bool {
	return l.reaches(l.nodes[viewer], pos)
}

// at is the bundle at a position of the prefix.
func (l *Lineage) at(pos uint64) model.BundleRef { return l.order[pos-1].Ref }

// markFor assigns the chain member a bundle at pos becomes: the next member of
// its predecessor's chain when the predecessor is that chain's newest member,
// otherwise the first member of a new chain.
func (l *Lineage) markFor(parents []model.BundleRef, pos uint64) chainMark {
	if len(parents) > 0 {
		pn := l.nodes[parents[0]]
		pred := l.order[pn.Position-1].Mark
		if l.tips[pred.Chain] == pn.Position {
			l.tips[pred.Chain] = pos
			return chainMark{Chain: pred.Chain, Index: pred.Index + 1}
		}
	}
	l.tips = append(l.tips, pos)
	return chainMark{Chain: uint32(len(l.tips) - 1)}
}
