package reduce

// Tests for constant-time ancestry (ancestry.go) against a plain walk of the
// parent links, over random ledgers that split, grow and rejoin.

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// randomSplitLedger grows n bundles after a genesis: each continues a random
// earlier bundle and sometimes also names other heads, as a merging admission
// does. Named heads are never ancestors of the predecessor or of each other.
// It returns the bundles in canonical order and each one's parents.
func randomSplitLedger(r *rand.Rand, n int) ([]model.Bundle, map[model.BundleRef][]model.BundleRef) {
	parents := map[model.BundleRef][]model.BundleRef{}
	var all []model.Bundle
	add := func(b model.Bundle, ps []model.BundleRef) {
		all = append(all, b)
		parents[b.Ref()] = ps
	}
	add(model.Bundle{Sequence: 1, CommandID: newID("G0")}, nil)
	for i := 1; i <= n; i++ {
		// Favour recent bundles so branches grow long, not just wide.
		pred := all[len(all)-1-r.Intn(min(len(all), 6))]
		b := model.Bundle{Sequence: pred.Sequence + 1, CommandID: newID(fmt.Sprintf("B%d", i)), Predecessor: pred.CommandID}
		ps := []model.BundleRef{pred.Ref()}
		if r.Intn(4) == 0 {
			for _, c := range r.Perm(len(all))[:min(len(all), 3)] {
				h := all[c].Ref()
				if h.Sequence >= b.Sequence || containsRef(ps, h) {
					continue
				}
				related := false
				for _, p := range ps {
					related = related || walkReaches(parents, p, h) || walkReaches(parents, h, p)
				}
				if !related {
					ps = append(ps, h)
					b.OtherHeads = append(b.OtherHeads, h)
				}
			}
			sort.Slice(b.OtherHeads, func(i, j int) bool { return b.OtherHeads[i].Before(b.OtherHeads[j]) })
		}
		add(b, ps)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Ref().Before(all[j].Ref()) })
	return all, parents
}

// walkReaches is the oracle: from is target, or target is reachable from it.
func walkReaches(parents map[model.BundleRef][]model.BundleRef, from, target model.BundleRef) bool {
	if from == target {
		return true
	}
	for _, p := range parents[from] {
		if walkReaches(parents, p, target) {
			return true
		}
	}
	return false
}

func TestAncestryAgreesWithAWalkOfTheParents(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		r := rand.New(rand.NewSource(seed))
		bundles, parents := randomSplitLedger(r, 60)
		l := &Lineage{}
		for _, b := range bundles {
			if err := l.Add(b); err != nil {
				t.Fatalf("seed %d: control ledger refused at %v: %v", seed, b.Ref(), err)
			}
		}
		for _, x := range bundles {
			n := l.nodes[x.Ref()]
			for _, y := range bundles {
				want := walkReaches(parents, x.Ref(), y.Ref())
				if got := l.reaches(n, l.nodes[y.Ref()].Position); got != want {
					t.Fatalf("seed %d: %v reaches %v: got %v, want %v", seed, x.Ref(), y.Ref(), got, want)
				}
			}
		}
	}
}

func TestALedgerThatNeverSplitKeepsNoClock(t *testing.T) {
	l, _ := lineOf(t, link(1, "G", ""), link(2, "A2", "G"), link(3, "A3", "A2"))
	for _, b := range l.order {
		if n := l.nodes[b.Ref]; n.Clock != 0 || n.Frontier != n.Position || b.Mark.Chain != 0 {
			t.Fatalf("a linear bundle needs only its frontier: %+v %+v", n, b.Mark)
		}
	}
}

// A line that keeps growing beside a branch that never rejoins must not copy
// its clock per bundle: continuing a chain names the parent's clock, so the
// table does not grow, and ancestry still answers through the shared clock.
func TestContinuingAChainAddsNoClock(t *testing.T) {
	// G; Z and H0..H3 off G at sequence 2; J1..J3 join H1..H3 into H0's line
	// one at a time while Z stays out; then the line grows.
	bundles := []model.Bundle{link(1, "G", ""), link(2, "Z", "G")}
	for i := 0; i < 4; i++ {
		bundles = append(bundles, link(2, fmt.Sprintf("H%d", i), "G"))
	}
	pred := "H0"
	for i := 1; i < 4; i++ {
		name := fmt.Sprintf("J%d", i)
		bundles = append(bundles, link(uint64(2+i), name, pred, at(2, fmt.Sprintf("H%d", i))))
		pred = name
	}
	sort.Slice(bundles, func(i, j int) bool { return bundles[i].Ref().Before(bundles[j].Ref()) })
	l, _ := lineOf(t, bundles...)
	joined := len(l.clocks)
	if joined == 0 {
		t.Fatal("control: joining branches above the frontier keeps a clock")
	}
	for i := 0; i < 50; i++ {
		name := fmt.Sprintf("T%d", i)
		if err := l.Add(link(uint64(6+i), name, pred)); err != nil {
			t.Fatal(err)
		}
		pred = name
	}
	if len(l.clocks) != joined {
		t.Fatalf("50 plain continuations grew the clock table from %d to %d", joined, len(l.clocks))
	}
	tip := l.nodes[at(55, pred)]
	for i := 0; i < 4; i++ {
		if !l.reaches(tip, l.nodes[at(2, fmt.Sprintf("H%d", i))].Position) {
			t.Fatalf("the line's tip must reach H%d through the shared clock", i)
		}
	}
	if l.reaches(tip, l.nodes[at(2, "Z")].Position) {
		t.Fatal("the line's tip must not reach the branch that never rejoined")
	}
}
