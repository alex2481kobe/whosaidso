package store

// Tests for a ledger whose chain split on separate branches and was merged by
// git: the merged folder is just more files, whichever way git merged them, so
// each fixture writes the files a merge leaves. Replay semantics (what is set
// aside) are reduce's; these check the store's read and admission.

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// splitBundle is raw ledger bytes for one bundle of a split chain.
func splitBundle(t *testing.T, p Project, sequence uint64, command, predecessor model.ID, heads ...model.BundleRef) {
	t.Helper()
	b := model.Bundle{
		Version: model.WireVersion, Project: p.ID, Sequence: sequence, CommandID: command,
		Predecessor: predecessor, OtherHeads: heads, RequestDigest: digestFor(int(sequence)),
		Admitter: model.Actor{ID: "coordinator"},
		Events:   []model.Event{{Type: "review.admit", Data: json.RawMessage(`{"note":"raw"}`)}},
	}
	data, err := model.Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	name, err := model.BundleName(sequence, command)
	if err != nil {
		t.Fatal(err)
	}
	writeLedgerFile(t, p, name, data)
}

func refOf(sequence uint64, command model.ID) model.BundleRef {
	return model.BundleRef{Sequence: sequence, CommandID: command}
}

func refsOf(bundles []model.Bundle) []model.BundleRef {
	out := make([]model.BundleRef, len(bundles))
	for i, b := range bundles {
		out[i] = b.Ref()
	}
	return out
}

func TestSplitChainReadsInCanonicalOrderAndTheNextAdmissionJoinsEveryHead(t *testing.T) {
	a, b, c := admissionID(20), admissionID(30), admissionID(40)
	cases := []struct {
		name  string
		write func(t *testing.T, p Project)
		order []model.BundleRef
		heads []model.BundleRef // other heads the next admission names; its predecessor is the canonical last
	}{
		{"two branches of equal length", func(t *testing.T, p Project) {
			splitBundle(t, p, 2, b, admissionID(1))
			splitBundle(t, p, 2, a, admissionID(1))
		}, []model.BundleRef{refOf(1, admissionID(1)), refOf(2, a), refOf(2, b)}, []model.BundleRef{refOf(2, a)}},
		{"branches of unequal length", func(t *testing.T, p Project) {
			splitBundle(t, p, 2, b, admissionID(1))
			splitBundle(t, p, 3, admissionID(31), b)
			splitBundle(t, p, 4, admissionID(32), admissionID(31))
			splitBundle(t, p, 2, a, admissionID(1))
		}, []model.BundleRef{refOf(1, admissionID(1)), refOf(2, a), refOf(2, b), refOf(3, admissionID(31)), refOf(4, admissionID(32))},
			[]model.BundleRef{refOf(2, a)}},
		{"three branches", func(t *testing.T, p Project) {
			splitBundle(t, p, 2, c, admissionID(1))
			splitBundle(t, p, 2, a, admissionID(1))
			splitBundle(t, p, 2, b, admissionID(1))
			splitBundle(t, p, 3, admissionID(41), c)
		}, []model.BundleRef{refOf(1, admissionID(1)), refOf(2, a), refOf(2, b), refOf(2, c), refOf(3, admissionID(41))},
			[]model.BundleRef{refOf(2, a), refOf(2, b)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := ledgerProject(t)
			admitControl(t, p, 1)
			tc.write(t, p)
			bundles, err := ReadPrefix(p)
			if err != nil {
				t.Fatalf("a merged split chain must read: %v", err)
			}
			if got := refsOf(bundles); !reflect.DeepEqual(got, tc.order) {
				t.Fatalf("read order %v, want canonical %v", got, tc.order)
			}
			last := tc.order[len(tc.order)-1]
			joined, err := Transact(context.Background(), p, admissionID(90), digestFor(90), proposeFor(90))
			if err != nil {
				t.Fatalf("admission after a merge: %v", err)
			}
			if joined.Sequence != last.Sequence+1 || joined.Predecessor != last.CommandID || !reflect.DeepEqual(joined.OtherHeads, tc.heads) {
				t.Fatalf("merging admission is sequence %d after %s naming %v; want %d after %s naming %v",
					joined.Sequence, joined.Predecessor, joined.OtherHeads, last.Sequence+1, last.CommandID, tc.heads)
			}
			// It descends from everything, so the ledger has one head again and
			// the next admission is written exactly as on a ledger that never split.
			next := admitControl(t, p, 91)
			if next.Sequence != joined.Sequence+1 || next.Predecessor != joined.CommandID || next.OtherHeads != nil {
				t.Fatalf("admission after the merging one: %+v", next)
			}
			data, err := json.Marshal(next)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if _, written := fields["other_heads"]; written {
				t.Fatalf("a ledger with one head must not write other_heads: %s", data)
			}
		})
	}
}

func TestSplitChainRefusesBrokenStructure(t *testing.T) {
	a, b := admissionID(20), admissionID(30)
	cases := []struct {
		name  string
		code  string
		write func(t *testing.T, p Project)
	}{
		{"a hole in sequences", "ledger-discontinuity", func(t *testing.T, p Project) {
			splitBundle(t, p, 2, a, admissionID(1))
			splitBundle(t, p, 4, b, a)
		}},
		{"predecessor names a missing bundle", "ledger-discontinuity", func(t *testing.T, p Project) {
			splitBundle(t, p, 2, a, admissionID(1))
			splitBundle(t, p, 2, b, admissionID(77))
		}},
		{"predecessor names a bundle of the wrong sequence", "ledger-discontinuity", func(t *testing.T, p Project) {
			splitBundle(t, p, 2, a, admissionID(1))
			splitBundle(t, p, 3, b, admissionID(1))
		}},
		{"an other head names a missing bundle", "ledger-discontinuity", func(t *testing.T, p Project) {
			splitBundle(t, p, 2, a, admissionID(1))
			splitBundle(t, p, 3, b, a, refOf(2, admissionID(77)))
		}},
		{"an other head of the same sequence", "ledger-corrupt", func(t *testing.T, p Project) {
			splitBundle(t, p, 2, a, admissionID(1))
			splitBundle(t, p, 2, b, admissionID(1), refOf(2, a))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := ledgerProject(t)
			admitControl(t, p, 1)
			tc.write(t, p)
			_, err := ReadPrefix(p)
			requireFault(t, err, tc.code)
			_, err = Transact(context.Background(), p, admissionID(90), digestFor(90), proposeFor(90))
			requireFault(t, err, tc.code)
		})
	}
}
