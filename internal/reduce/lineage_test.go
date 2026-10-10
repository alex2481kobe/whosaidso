package reduce

// Tests for the chain structure (lineage.go): split chains, heads, descent,
// where an admission appends, and the structural refusals.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// link is a bare chain entry: only the fields the lineage reads.
func link(seq uint64, id, pred string, heads ...model.BundleRef) model.Bundle {
	b := model.Bundle{Sequence: seq, CommandID: newID(id), RequestDigest: newDigest(id), OtherHeads: heads}
	if pred != "" {
		b.Predecessor = newID(pred)
	}
	return b
}

func at(seq uint64, id string) model.BundleRef {
	return model.BundleRef{Sequence: seq, CommandID: newID(id)}
}

// lineOf adds every bundle and returns each one's placement.
func lineOf(t *testing.T, bundles ...model.Bundle) (*Lineage, []placement) {
	t.Helper()
	l := &Lineage{}
	out := make([]placement, 0, len(bundles))
	for _, b := range bundles {
		p, err := l.add(b)
		if err != nil {
			t.Fatalf("adding %v: %v", b.Ref(), err)
		}
		out = append(out, p)
	}
	return l, out
}

func TestLineageDescentAndHeadsOverASplitChain(t *testing.T) {
	// Genesis G, then branch A (A2, A3) and branch B (B2), merged by M4,
	// which names A3 as predecessor and B2 as the other head.
	l, places := lineOf(t,
		link(1, "G", ""),
		link(2, "A2", "G"),
		link(2, "B2", "G"),
		link(3, "A3", "A2"),
		link(4, "M4", "A3", at(2, "B2")),
		link(5, "N5", "M4"),
	)
	want := []bool{true, true, false, false, true, true}
	for i, p := range places {
		if p.descendsAll != want[i] {
			t.Errorf("bundle %d descends from everything before it = %v, want %v", i, p.descendsAll, want[i])
		}
	}
	if !reflect.DeepEqual(l.heads, []model.BundleRef{at(5, "N5")}) {
		t.Fatalf("after a merging admission the ledger has one head, got %v", l.heads)
	}
	next := l.Chain(model.Bundle{})
	if next.Sequence != 6 || next.Predecessor != newID("N5") || next.OtherHeads != nil {
		t.Fatalf("a ledger with one head appends exactly as before: %+v", next.Ref())
	}
}

func TestLineageChainJoinsEveryHeadAfterTheCanonicalLast(t *testing.T) {
	l, _ := lineOf(t, link(1, "G", ""), link(2, "A2", "G"), link(2, "B2", "G"), link(2, "C2", "G"), link(3, "B3", "B2"))
	next := l.Chain(model.Bundle{})
	if next.Sequence != 4 || next.Predecessor != newID("B3") ||
		!reflect.DeepEqual(next.OtherHeads, []model.BundleRef{at(2, "A2"), at(2, "C2")}) {
		t.Fatalf("got sequence %d after %s naming %v", next.Sequence, next.Predecessor, next.OtherHeads)
	}
	p, err := l.add(link(4, "M4", "B3", next.OtherHeads...))
	if err != nil || !p.descendsAll {
		t.Fatalf("the bundle Chain stamps must descend from everything: %v %+v", err, p)
	}
}

func TestLineageFirstBundleOfEachBranchArrivesInCanonicalOrder(t *testing.T) {
	// The set of files decides the order; listing them otherwise is refused,
	// never silently reordered.
	_, err := (&Lineage{}).addAll(link(1, "G", ""), link(2, "B2", "G"), link(2, "A2", "G"))
	wantFault(t, err, CodeLedgerDiscontinuity)
}

func TestLineageRefusesBrokenStructure(t *testing.T) {
	g := link(1, "G", "")
	cases := []struct {
		name    string
		code    string
		bundles []model.Bundle
		says    string // what the refusal names, so a reader can find the damage
	}{
		{"a hole in sequences", CodeLedgerDiscontinuity, []model.Bundle{g, link(2, "A2", "G"), link(2, "B2", "G"), link(4, "A4", "A2")}, "sequence 3 is missing"},
		{"predecessor names a missing bundle", CodeLedgerDiscontinuity, []model.Bundle{g, link(2, "A2", "G"), link(2, "B2", "X1")}, "names no bundle at sequence 1"},
		{"predecessor names a bundle of the wrong sequence", CodeLedgerDiscontinuity, []model.Bundle{g, link(2, "A2", "G"), link(3, "B3", "G")}, "names no bundle at sequence 2"},
		{"an other head names a missing bundle", CodeLedgerDiscontinuity, []model.Bundle{g, link(2, "A2", "G"), link(3, "M3", "A2", at(2, "X2"))}, "names no earlier bundle"},
		{"a second genesis", CodeLedgerFork, []model.Bundle{g, link(1, "H", "")}, "one ledger has one genesis"},
		{"the same command twice in one line", CodeDuplicateCommand, []model.Bundle{g, link(2, "A2", "G"), link(3, "A2", "A2")}, "published at sequences 2 and 3"},
		{"the same command twice through a merge", CodeDuplicateCommand,
			[]model.Bundle{g, link(2, "A2", "G"), link(2, "B2", "G"), link(3, "M3", "B2", at(2, "A2")), link(4, "A2", "M3")}, "published at sequences 2 and 4"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (&Lineage{}).addAll(tc.bundles...)
			wantFault(t, err, tc.code)
			if !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("the refusal must say %q: %v", tc.says, err)
			}
		})
	}
}

func TestLineageMarksTheSameCommandOnTwoBranchesAsADuplicate(t *testing.T) {
	// One machine's intake is shared across branches, so one admission can be
	// published on both. Neither descends from the other: not fatal here; the
	// fold sets the later one aside.
	_, places := lineOf(t, link(1, "G", ""), link(2, "A2", "G"), link(2, "Z2", "G"), link(3, "X", "A2"), link(3, "Z3", "Z2"), link(4, "X", "Z3"))
	dup := places[5].duplicate
	if dup == nil || dup.Sequence != 3 || places[5].descendsAll {
		t.Fatalf("the second copy must name the first and not descend from everything: %+v", places[5])
	}
	for i, p := range places[:5] {
		if p.duplicate != nil {
			t.Fatalf("bundle %d is not a duplicate: %+v", i, p)
		}
	}
}

// addAll adds bundles until one is refused.
func (l *Lineage) addAll(bundles ...model.Bundle) (*Lineage, error) {
	for _, b := range bundles {
		if err := l.Add(b); err != nil {
			return l, err
		}
	}
	return l, nil
}
