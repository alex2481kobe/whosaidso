// What a set-aside bundle does and does not take down with it, and what its
// history shows. What belongs here: a joining admission when another branch
// merges later, and record-filtered history over set-aside bundles. What does
// not: the first clash cases (ledger_merge_set_aside_test.go) or the optional
// field's shape (ledger_merge_field_test.go).
package acceptance_test

import (
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// A joining admission names every branch tip it saw, one of which may be a
// set-aside bundle. That is a statement that it saw it, not a record built on
// it: when an independent third branch merges later, the join still applies
// and only the bundle that clashed stays set aside. The third branch's command
// ids sort before, between and after the two clashing branches', because the
// canonical order is (sequence, command id).
func TestLedgerMergeJoiningAdmissionStaysAppliedWhenAnotherBranchMergesLater(t *testing.T) {
	for _, late := range []struct {
		name string
		n    int
	}{{"third_branch_sorts_first", 150}, {"third_branch_sorts_between", 250}, {"third_branch_sorts_last", 400}} {
		t.Run(late.name, func(t *testing.T) {
			base := mwWorld(t, 100)
			claim := base.mwClaim("the shared base claim")
			a := mwFork(t, base, 200)
			a.mwAdmit(a.mwReviseEvent(claim, "revised on branch A"))
			b := mwFork(t, base, 300)
			revB := b.mwAdmit(b.mwReviseEvent(claim, "revised on branch B"))

			joined := mwMerge(t, a, b)
			joinedClaim := joined.mwClaim("recorded by the joining admission")
			mwReadsOK(t, joined, "the joined ledger, before the third branch")
			mwOnlyMarked(t, joined, revB)
			mwWantRecords(t, joined, joinedClaim)

			c := mwFork(t, base, late.n)
			lateClaim := c.mwClaim("recorded on the late third branch")

			m := mwUnion(t, mwBundles(joined), mwBundles(c))
			mwReadsOK(t, m, "the joined ledger after an independent branch merged")
			mwWantRecords(t, m, joinedClaim, lateClaim)
			if got := mwRecords(t, m)[claim]; got[0] != "revised on branch A" || got[1] != "2" {
				t.Errorf("claim reads %q at revision %s, want A's revision 2", got[0], got[1])
			}
			mwOnlyMarked(t, m, revB)
			if codes := mwAsideCodes(t, m); len(codes) != 1 || codes[revB] == "" {
				t.Errorf("set aside is %v, want only %s: the joining bundle and the third branch are unrelated to the clash", codes, revB)
			}
		})
	}
}

// history of a record lists the events of set-aside bundles that touch it,
// marked as set aside, and a record that exists only in a set-aside bundle
// still has that history rather than an empty answer.
func TestLedgerMergeRecordHistoryShowsSetAsideEvents(t *testing.T) {
	base := mwWorld(t, 100)
	claim := base.mwClaim("the shared base claim")
	a := mwFork(t, base, 200)
	a.mwAdmit(a.mwReviseEvent(claim, "revised on branch A"))
	b := mwFork(t, base, 300)
	created := b.id()
	b1 := b.mwAdmit(b.mwClaimEvent(created, "created only by the bundle that clashes"), b.mwReviseEvent(claim, "revised on branch B"))

	m := mwMerge(t, a, b)
	mwReadsOK(t, m, "two branches amending one claim, one also creating a record")
	if _, ok := mwRecords(t, m)[created]; ok {
		t.Fatal("fixture: the record created by the set-aside bundle must not be applied")
	}
	for name, record := range map[string]model.ID{"the amended record": claim, "the record created only in the set-aside bundle": created} {
		t.Run(name, func(t *testing.T) {
			mwRecordHistoryMarks(t, m, record, b1)
		})
	}
}
