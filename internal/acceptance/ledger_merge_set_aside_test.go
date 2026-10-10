// Real clashes on a split ledger: the bundle that cannot apply is set aside,
// named in the views with the instruction to record it again, and never fatal.
// What belongs here: two branches changing the same record, a bundle depending
// on a set-aside one, and one packet admitted on two branches. What does not:
// damage that must stay fatal (ledger_merge_damage_test.go).
package acceptance_test

import (
	"bytes"
	"reflect"
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
	mwHistoryMarks(t, w, id)
}

// mwHistoryMarks fails unless history marks the events of the set-aside bundle
// in --json (a set_aside object on its rows) and in the text (a SET ASIDE line
// after its event line).
func mwHistoryMarks(t *testing.T, w *flowWorld, id model.ID) {
	t.Helper()
	out, err := w.cli(nil, "history", "--json")
	if err != nil {
		t.Errorf("whosaidso history --json: %v", err)
		return
	}
	marked := false
	for _, row := range flowList(flowDecode(t, out), "events") {
		if flowStr(row, "command_id") == string(id) && flowGet(row, "set_aside") != nil {
			marked = true
		}
	}
	if !marked {
		t.Errorf("whosaidso history --json lists no event of the set-aside bundle %s with a set_aside mark", id)
	}
	text, err := w.cli(nil, "history")
	if err != nil {
		t.Errorf("whosaidso history: %v", err)
		return
	}
	lines := strings.Split(string(text), "\n")
	textMarked := false
	for i, line := range lines {
		if strings.Contains(line, string(id)) && i+1 < len(lines) && strings.Contains(strings.ToUpper(lines[i+1]), "SET ASIDE") {
			textMarked = true
		}
	}
	if !textMarked {
		t.Errorf("whosaidso history marks no event line of the set-aside bundle %s with SET ASIDE:\n%s", id, text)
	}
}

// mwOnlyMarked fails unless history --json marks the events of exactly these
// bundles as set aside: applied bundles are never marked.
func mwOnlyMarked(t *testing.T, w *flowWorld, ids ...model.ID) {
	t.Helper()
	out, err := w.cli(nil, "history", "--json")
	if err != nil {
		t.Fatalf("whosaidso history --json: %v", err)
	}
	got := map[string]bool{}
	for _, row := range flowList(flowDecode(t, out), "events") {
		if flowGet(row, "set_aside") != nil {
			got[flowStr(row, "command_id")] = true
		}
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[string(id)] = true
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("history marks the events of bundles %v as set aside, want exactly %v; an applied bundle must never be marked", got, want)
	}
}

// A bundle that repeats another's events at the same sequence under a new
// command id is a valid split whose later bundle, in canonical order, is set
// aside: its events duplicate the other's. Which one is later is the command
// id's order and nothing else.
func TestLedgerMergeCopiedBundleAtTheSameSequenceSetsTheLaterOneAside(t *testing.T) {
	for _, copySortsLater := range []bool{true, false} {
		name := "copy_sorts_later"
		if !copySortsLater {
			name = "copy_sorts_earlier"
		}
		t.Run(name, func(t *testing.T) {
			w := mwWorld(t, 100)
			w.mwClaim("the shared base claim")
			claim := w.mwClaim("admitted once, then copied")
			files := mwFiles(w)
			original := files[len(files)-1]
			originalID := model.ID(original[9 : len(original)-5])
			copyID := recID(990) // sorts after every id this fixture mints
			if !copySortsLater {
				copyID = recID(1)
			}
			name, err := model.BundleName(2, copyID)
			if err != nil {
				t.Fatal(err)
			}
			mwEdit(t, w, original, name, func(f map[string]any) { f["command_id"] = string(copyID) })
			mwReadsOK(t, w, "an admission and its copy at one sequence")
			mwWantRecords(t, w, claim)
			later := copyID
			if !copySortsLater {
				later = originalID
			}
			if got := mwRecords(t, w)[claim]; got[1] != "1" {
				t.Errorf("the claim is at revision %s, want its single revision 1: exactly one copy applies", got[1])
			}
			mwAsserted(t, w, later)
			mwOnlyMarked(t, w, later)
		})
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
		mwOnlyMarked(t, m, revB)
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
	mwOnlyMarked(t, m, revB, followB)
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
