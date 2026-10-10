// A ledger that two people extended on separate git branches must read and
// accept new records after the branches merge, with no extra step.
//
// What belongs here: merges with no clash (independent, unequal and three
// branches), binding a merged checkout, the first admission after a merge and
// the read cache. What does not: clashes (ledger_merge_set_aside_test.go),
// damage and linear ledgers (ledger_merge_damage_test.go), or fixtures
// (ledger_merge_fixtures_test.go).
package acceptance_test

import (
	"bytes"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// ---- a ledger that split reads, and every record survives -------------------

func TestLedgerMergeIndependentBranchesReadAndKeepEveryRecord(t *testing.T) {
	base := mwWorld(t, 100)
	baseClaim := base.mwClaim("the shared base claim")
	// B records first in time but mints the greater command ids, so neither
	// file order, creation order nor recorded time can pass for command order.
	b := mwFork(t, base, 300)
	claimB := b.mwClaim("recorded on branch B")
	a := mwFork(t, base, 200)
	claimA := a.mwClaim("recorded on branch A")
	claimA2 := a.mwClaim("recorded on branch A after its first")

	var answers []map[string][]byte
	for _, order := range [][]*flowWorld{{a, b}, {b, a}} {
		m := mwMerge(t, order...)
		got := mwReadsOK(t, m, "merged branches")
		answers = append(answers, got)
		mwWantRecords(t, m, baseClaim, claimA, claimA2, claimB)
		// The canonical order is sequence, then command id; recorded time and
		// the branch a bundle came from decide nothing.
		fileSeq := map[model.ID]uint64{}
		for name := range mwBundles(m) {
			if mm := flowBundleName.FindStringSubmatch(name); mm != nil {
				fileSeq[model.ID(mm[2])], _ = strconv.ParseUint(mm[1], 10, 64)
			}
		}
		var seqs []uint64
		var ids []model.ID
		for _, ev := range flowList(flowDecode(t, got["history --json"]), "events") {
			// origin.sequence is a position in the replayed order; the bundle's
			// own sequence is in its file name.
			id := model.ID(flowStr(ev, "command_id"))
			seqs, ids = append(seqs, fileSeq[id]), append(ids, id)
		}
		if len(ids) < 8 {
			t.Fatalf("history lists %d events, want those of all 4 bundles", len(ids))
		}
		for i := 1; i < len(ids); i++ {
			if seqs[i] < seqs[i-1] || (seqs[i] == seqs[i-1] && ids[i] < ids[i-1]) {
				t.Errorf("history lists sequence %d command %s after sequence %d command %s; one canonical order is sequence ascending, then command id ascending", seqs[i], ids[i], seqs[i-1], ids[i-1])
			}
		}
		_, head := mwHeads(m)
		mark := flowDecode(t, got["todo --json"])["watermark"]
		if flowStr(mark, "head", "command_id") != string(head) {
			t.Errorf("the todo watermark names head %s, want the canonical last bundle %s", flowStr(mark, "head", "command_id"), head)
		}
	}
	if len(answers) == 2 {
		for name, want := range answers[0] {
			if !bytes.Equal(want, answers[1][name]) {
				t.Errorf("`whosaidso %s` answers differently depending on which branch was assembled first; the answer must depend only on the set of files", name)
			}
		}
	}
}

func TestLedgerMergeUnequalAndThreeBranchesKeepEveryRecord(t *testing.T) {
	base := mwWorld(t, 100)
	all := []model.ID{base.mwClaim("the shared base claim")}
	var branches []*flowWorld
	for i, length := range []int{1, 4, 2} {
		b := mwFork(t, base, 200+100*i)
		for j := 0; j < length; j++ {
			all = append(all, b.mwClaim("branch "+strconv.Itoa(i)+" record "+strconv.Itoa(j)))
		}
		branches = append(branches, b)
	}
	var first map[string][]byte
	for _, order := range [][]int{{0, 1, 2}, {2, 1, 0}, {1, 2, 0}} {
		var picked []*flowWorld
		for _, i := range order {
			picked = append(picked, branches[i])
		}
		m := mwMerge(t, picked...)
		answers := mwReadsOK(t, m, "three unequal branches")
		mwWantRecords(t, m, all...)
		if first == nil {
			first = answers
			continue
		}
		for name, want := range first {
			if !bytes.Equal(want, answers[name]) {
				t.Errorf("`whosaidso %s` differs between merge orders %v; the answer must depend only on the set of files", name, order)
			}
		}
	}
}

// A machine already bound to one branch's checkout relocates to the merged
// checkout: every bundle it had is still there, byte for byte, even though the
// other branch's bundles sort among them.
func TestLedgerMergeMachineBoundToOneBranchCanBindTheMergedCheckout(t *testing.T) {
	base := mwWorld(t, 100)
	base.mwClaim("the shared base claim")
	b := mwFork(t, base, 200)
	b.mwClaim("recorded on branch B")
	a := mwFork(t, base, 300)
	a.mwClaim("recorded on branch A")

	merged := mwMerge(t, a, b)
	merged.home = a.home
	if _, err := merged.cli(nil, "home", "."); err != nil {
		t.Fatalf("binding the merged checkout, which holds every bundle of the bound one: %v", err)
	}
	mwReadsOK(t, merged, "the merged checkout on the machine that was bound to branch A")
}

// ---- admission after a merge -------------------------------------------------

func TestLedgerMergeFirstAdmissionAppendsAfterCanonicalLastAndLaterReadsStayClean(t *testing.T) {
	base := mwWorld(t, 100)
	base.mwClaim("the shared base claim")
	a := mwFork(t, base, 200)
	claimA := a.mwClaim("recorded on branch A")
	b := mwFork(t, base, 300)
	claimB := b.mwClaim("recorded on branch B")
	m := mwMerge(t, a, b)
	mwReadsOK(t, m, "the merged ledger before any new admission")

	known := mwBundles(m)
	seqBefore, headBefore := mwHeads(m)
	var otherHead model.ID
	for name := range known {
		if mm := flowBundleName.FindStringSubmatch(name); mm != nil && mm[1] == "00000002" && model.ID(mm[2]) != headBefore {
			otherHead = model.ID(mm[2])
		}
	}
	claimM := m.mwClaim("recorded after the merge")
	mwReadsOK(t, m, "after the first admission on a merged ledger")
	mwWantRecords(t, m, claimA, claimB, claimM)

	// newBundle is the top-level fields of the one file published since the
	// last call.
	newBundle := func() map[string]json.RawMessage {
		var added []string
		for name := range mwBundles(m) {
			if _, ok := known[name]; !ok {
				added = append(added, name)
			}
		}
		if len(added) != 1 {
			t.Fatalf("one admission must publish exactly one bundle file, got %v", added)
		}
		known = mwBundles(m)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(known[added[0]], &fields); err != nil {
			t.Fatal(err)
		}
		return fields
	}
	fields := newBundle()
	var sequence uint64
	var predecessor model.ID
	_ = json.Unmarshal(fields["sequence"], &sequence)
	_ = json.Unmarshal(fields["predecessor"], &predecessor)
	if sequence != seqBefore+1 || predecessor != headBefore {
		t.Errorf("the first admission after the merge is sequence %d after %s, want sequence %d after the canonical last bundle %s", sequence, predecessor, seqBefore+1, headBefore)
	}
	// The one optional field names the other head, so "this bundle saw both
	// branches" is decidable from the ledger alone; the field's name is open.
	var extra []string
	for key := range fields {
		if !mwLinearFields[key] {
			extra = append(extra, key)
		}
	}
	if len(extra) != 1 || !bytes.Contains(fields[extra[0]], []byte(otherHead)) {
		t.Errorf("a bundle admitted over two heads must carry exactly one optional field naming the head %s that is not its predecessor; it carries new fields %v", otherHead, extra)
	}

	// That bundle descends from everything, so the ledger has one head again:
	// the next admission needs no extra field and the ledger stays clean.
	m.mwClaim("recorded after the merge, second")
	mwReadsOK(t, m, "after the second admission")
	for key := range newBundle() {
		if !mwLinearFields[key] {
			t.Errorf("a bundle admitted over a single head carries the field %q; the optional field belongs only to bundles admitted over several heads", key)
		}
	}
}

func TestLedgerMergeBundleInsertedBeforeACachedPrefixIsRead(t *testing.T) {
	base := mwWorld(t, 100)
	base.mwClaim("the shared base claim")
	// B's command ids sort before A's, so B's bundles land in the middle of the
	// canonical order of a clone that already cached A's.
	b := mwFork(t, base, 200)
	claimB := b.mwClaim("recorded on branch B")
	a := mwFork(t, base, 300)
	claimA := a.mwClaim("recorded on branch A")
	claimA2 := a.mwClaim("recorded on branch A after its first")

	clone := mwFork(t, a, 700)
	mwReadsOK(t, clone, "the clone before the merge")
	mwReadsOK(t, clone, "the clone again, from its cache")
	mwCopy(t, mwBundles(b), clone)
	got := mwReadsOK(t, clone, "the clone after B's bundle landed before cached ones")
	mwWantRecords(t, clone, claimA, claimA2, claimB)

	want := mwReadsOK(t, mwMerge(t, a, b), "a fresh clone of the merged ledger")
	for name := range want {
		if !bytes.Equal(got[name], want[name]) {
			t.Errorf("`whosaidso %s` on a clone with a warm cache differs from a fresh clone of the same files; a bundle inserted before the cached prefix must invalidate it", name)
		}
	}
}
