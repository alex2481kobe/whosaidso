// Real clashes on a split ledger: the bundle that cannot apply is set aside,
// named in the views with the instruction to record it again, and never fatal.
// What belongs here: two branches changing the same record, a bundle depending
// on a set-aside one, and one packet admitted on two branches. What does not:
// damage that must stay fatal (ledger_merge_damage_test.go).
package acceptance_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// mwAsserted fails unless todo and history, as text and as --json, name the
// set-aside bundle's command id, and todo says to record it again.
func mwAsserted(t *testing.T, w *flowWorld, id model.ID) {
	t.Helper()
	for _, view := range []string{"todo", "history"} {
		for _, args := range [][]string{{view}, {view, "--json"}} {
			out, err := w.cli(nil, args...)
			if err != nil {
				t.Errorf("whosaidso %s: %v", strings.Join(args, " "), err)
				continue
			}
			if !bytes.Contains(out, []byte(id)) {
				t.Errorf("whosaidso %s does not name the set-aside bundle %s; nothing set aside may be hidden", strings.Join(args, " "), id)
			}
			if view == "todo" && !mwRerecord.Match(out) {
				t.Errorf("whosaidso %s names no instruction to re-record the set-aside bundle %s", strings.Join(args, " "), id)
			}
		}
	}
}

func TestLedgerMergeSameRecordRevisedOnBothBranchesSetsExactlyOneAside(t *testing.T) {
	base := mwWorld(t, 100)
	claim := base.mwClaim("the shared base claim")
	a := mwFork(t, base, 200)
	a.mwAdmit(a.mwReviseEvent(claim, "revised on branch A"))
	// An ordinary independent record rides along on A and must be untouched.
	otherA := a.mwClaim("independent record on branch A")
	b := mwFork(t, base, 300)
	revB := b.mwAdmit(b.mwReviseEvent(claim, "revised on branch B"))

	for _, order := range [][]*flowWorld{{a, b}, {b, a}} {
		m := mwMerge(t, order...)
		mwReadsOK(t, m, "two branches revising one claim")
		mwWantRecords(t, m, claim, otherA)
		// Both revisions replace revision 1. Replay applies the earlier in
		// canonical order (A's, the smaller command id at the same sequence)
		// and sets the other aside.
		rec := mwRecords(t, m)[claim]
		if rec[0] != "revised on branch A" || rec[1] != "2" {
			t.Errorf("claim reads %q at revision %s, want the earlier bundle's %q as revision 2; exactly one revision may apply", rec[0], rec[1], "revised on branch A")
		}
		mwAsserted(t, m, revB)
	}
}

func TestLedgerMergeSameTaskAmendedOnBothBranchesSetsExactlyOneAside(t *testing.T) {
	base := mwWorld(t, 100)
	task := base.task("the shared task")
	amend := func(w *flowWorld, intent string) model.ID {
		spec := task.spec
		spec.Intent = intent
		return w.mwAdmit(&model.TaskAmend{Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}}, Target: task.ref, Replacement: spec})
	}
	a := mwFork(t, base, 200)
	amend(a, "amended on branch A")
	b := mwFork(t, base, 300)
	amendB := amend(b, "amended on branch B")

	m := mwMerge(t, a, b)
	mwReadsOK(t, m, "two branches amending one task")
	rec := mwRecords(t, m)[task.ref.RecordID]
	if rec[1] != "2" || !strings.Contains(rec[0], "amended on branch A") {
		t.Errorf("task reads %q at revision %s, want the earlier bundle's amendment as revision 2; exactly one amendment may apply", rec[0], rec[1])
	}
	mwAsserted(t, m, amendB)
}

func TestLedgerMergeBundleDependingOnSetAsideBundleIsSetAsideToo(t *testing.T) {
	base := mwWorld(t, 100)
	claim := base.mwClaim("the shared base claim")
	a := mwFork(t, base, 200)
	a.mwAdmit(a.mwReviseEvent(claim, "revised on branch A"))
	indepA := a.mwClaim("independent record on branch A")
	b := mwFork(t, base, 300)
	// B's revision clashes with A's and, in the same bundle, creates a record
	// that B's next bundle then revises. When the first is set aside the second
	// has nothing to revise.
	dependent := b.id()
	revB := b.mwAdmit(b.mwReviseEvent(claim, "revised on branch B"), b.mwClaimEvent(dependent, "made by the bundle that clashes"))
	followB := b.mwAdmit(b.mwReviseEvent(dependent, "revised on B after its own creation"))

	m := mwMerge(t, a, b)
	mwReadsOK(t, m, "a dependent bundle on a branch whose earlier bundle clashed")
	records := mwRecords(t, m)
	if rec := records[claim]; rec[0] != "revised on branch A" {
		t.Errorf("claim reads %q, want A's revision applied", rec[0])
	}
	if _, ok := records[indepA]; !ok {
		t.Error("an independent bundle on branch A was lost")
	}
	if _, ok := records[dependent]; ok {
		t.Error("a record made by a set-aside bundle is shown as applied")
	}
	mwAsserted(t, m, revB)
	mwAsserted(t, m, followB)
}

// One machine's intake is shared by every branch it works on, so the same
// captured packet can be admitted on two branches.
func TestLedgerMergeSamePacketAdmittedOnBothBranchesSetsLaterAside(t *testing.T) {
	for _, sameCommand := range []bool{false, true} {
		name := "different_admission_ids"
		if sameCommand {
			name = "same_admission_id_at_different_sequences"
		}
		t.Run(name, func(t *testing.T) {
			base := mwWorld(t, 100)
			base.mwClaim("the shared base claim")
			machine := mwFork(t, base, 200)
			baseFiles := mwBundles(machine)
			claim := machine.id()
			packet := machine.capture(flowAgent, machine.mwClaimEvent(claim, "captured once"))

			// Branch one of the machine's checkout.
			first := machine.id()
			if sameCommand {
				machine.mwClaim("an earlier record only the first branch has")
			}
			if err := machine.mwAdmitPacket(first, packet); err != nil {
				t.Fatalf("control: the first branch admits the packet: %v", err)
			}
			branchOne := mwBundles(machine)

			// Switch to branch two, still on the same machine, and admit it again.
			second := machine.id()
			if sameCommand {
				second = first
			}
			mwCheckout(t, machine, baseFiles)
			if err := machine.mwAdmitPacket(second, packet); err != nil {
				t.Fatalf("control: the second branch admits the same packet: %v", err)
			}
			branchTwo := mwBundles(machine)

			for _, order := range [][]map[string][]byte{{branchOne, branchTwo}, {branchTwo, branchOne}} {
				m := mwUnion(t, order...)
				mwReadsOK(t, m, "one packet admitted on two branches")
				mwWantRecords(t, m, claim)
				mwAsserted(t, m, second)
			}
		})
	}
}
