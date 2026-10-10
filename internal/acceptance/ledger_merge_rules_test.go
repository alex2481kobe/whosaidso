// The rules that decide WHICH bundles of a split ledger apply: a bundle built
// on a set-aside one is set aside even when it would pass alone, a set-aside
// bundle applies no part of itself, a command id published on two branches
// keeps only the earlier, one published twice on one line is damage, and the
// canonical order is the only order. What belongs here: those rules and the
// help topic that explains them. What does not: the first clash cases
// (ledger_merge_set_aside_test.go) or the optional bundle field
// (ledger_merge_field_test.go).
package acceptance_test

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

// mwAsideCodes is the todo --json set_aside list: command id to fault code.
func mwAsideCodes(t *testing.T, w *flowWorld) map[model.ID]string {
	t.Helper()
	out, err := w.cli(nil, "todo", "--json")
	if err != nil {
		t.Fatalf("todo --json: %v", err)
	}
	got := map[model.ID]string{}
	for _, e := range flowList(flowDecode(t, out), "set_aside") {
		got[model.ID(flowStr(e, "command_id"))] = flowStr(e, "code")
	}
	return got
}

// References are (record id, revision) with no digest, so B's amendment that
// expects revision 2 of a claim would land on A's revision 2 when replayed
// beside it. It was recorded on top of B's own set-aside revision, so it must
// be set aside, not retargeted.
func TestLedgerMergeAmendmentBuiltOnSetAsideRevisionIsNotRetargetedToTheOtherBranch(t *testing.T) {
	base := mwWorld(t, 100)
	claim := base.mwClaim("the shared base claim")
	a := mwFork(t, base, 200)
	a.mwAdmit(a.mwReviseEvent(claim, "revised on branch A"))
	b := mwFork(t, base, 300)
	b1 := b.mwAdmit(b.mwReviseEvent(claim, "revised on branch B"))
	// Valid on B alone: it amends B's own revision 2.
	b2 := b.mwAdmit(b.mwReviseAt(claim, 2, "revised on branch B again"))
	if got := mwRecords(t, b)[claim]; got[0] != "revised on branch B again" || got[1] != "3" {
		t.Fatalf("control: branch B alone must read its own revision 3, got %v", got)
	}

	m := mwMerge(t, a, b)
	mwReadsOK(t, m, "a branch whose first amendment clashed and whose second expects its revision")
	if got := mwRecords(t, m)[claim]; got[0] != "revised on branch A" || got[1] != "2" {
		t.Errorf("claim reads %q at revision %s, want A's revision 2: B's second amendment expected B's revision 2 and must not apply to A's", got[0], got[1])
	}
	mwAsserted(t, m, b1)
	mwAsserted(t, m, b2)
	mwOnlyMarked(t, m, b1, b2)
	if got := mwAsideCodes(t, m)[b2]; got != reduce.CodeBuiltOnSetAside {
		t.Errorf("bundle %s is set aside with code %q, want %q: it passed every rule alone and was set aside only for what it was built on", b2, got, reduce.CodeBuiltOnSetAside)
	}
}

// A set-aside bundle is kept whole and applied in no part. The event that
// clashes comes after the event that creates a record, so a fold that kept the
// earlier event would leave that record behind.
func TestLedgerMergeSetAsideBundleLeavesNoTraceOfItsEarlierEvents(t *testing.T) {
	base := mwWorld(t, 100)
	claim := base.mwClaim("the shared base claim")
	a := mwFork(t, base, 200)
	a.mwAdmit(a.mwReviseEvent(claim, "revised on branch A"))
	b := mwFork(t, base, 300)
	created := b.id()
	b1 := b.mwAdmit(b.mwClaimEvent(created, "created by the bundle that clashes"), b.mwReviseEvent(claim, "revised on branch B"))
	var types []string
	hist, err := b.cli(nil, "history", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range flowList(flowDecode(t, hist), "events") {
		if flowStr(row, "command_id") == string(b1) {
			types = append(types, flowStr(row, "event", "type"))
		}
	}
	if len(types) < 2 || types[0] != "claim.assert" || types[1] != "claim.revise" {
		t.Fatalf("fixture: the clashing event must come after the creating one in the bundle, got %v", types)
	}

	m := mwMerge(t, a, b)
	mwReadsOK(t, m, "a bundle with a later clashing event")
	records := mwRecords(t, m)
	if _, ok := records[created]; ok {
		t.Error("a record created by an earlier event of a set-aside bundle is shown; a set-aside bundle applies no part of itself")
	}
	if got := records[claim]; got[0] != "revised on branch A" {
		t.Errorf("claim reads %q, want A's revision", got[0])
	}
	mwAsserted(t, m, b1)
	mwOnlyMarked(t, m, b1)
}

// One admission command id published on two branches is one admission: the
// later bundle is set aside as a duplicate even though its events are valid.
func TestLedgerMergeCommandIdOnTwoBranchesKeepsOnlyTheEarlierBundle(t *testing.T) {
	x := recID(950)
	base := mwWorld(t, 100)
	base.mwClaim("the shared base claim")
	a := mwFork(t, base, 200)
	claimA := a.id()
	packetA := a.capture(flowAgent, a.mwClaimEvent(claimA, "admitted under x on branch A"))
	earlierA := a.mwClaim("a record only A has before x")
	if err := a.mwAdmitPacket(x, packetA); err != nil {
		t.Fatalf("control: branch A admits under x: %v", err)
	}
	b := mwFork(t, base, 300)
	claimB := b.id()
	packetB := b.capture(flowAgent, b.mwClaimEvent(claimB, "admitted under x on branch B"))
	if err := b.mwAdmitPacket(x, packetB); err != nil {
		t.Fatalf("control: branch B admits under x: %v", err)
	}

	for _, order := range [][]*flowWorld{{a, b}, {b, a}} {
		m := mwMerge(t, order...)
		mwReadsOK(t, m, "one command id on two branches, at sequences 3 and 2")
		records := mwRecords(t, m)
		if _, ok := records[claimB]; !ok {
			t.Error("the earlier bundle under the command id (B's, at sequence 2) is missing")
		}
		if _, ok := records[claimA]; ok {
			t.Error("the later bundle under the command id (A's, at sequence 3) was applied beside the earlier one")
		}
		mwWantRecords(t, m, earlierA)
		codes := mwAsideCodes(t, m)
		if len(codes) != 1 || codes[x] != reduce.CodeDuplicateCommand {
			t.Errorf("set aside is %v, want only %s with code %q", codes, x, reduce.CodeDuplicateCommand)
		}
	}
}

// The same command id twice on ONE line of history is not two branches: no
// branch explains it, so it stays fatal even on a ledger that split elsewhere.
func TestLedgerMergeCommandIdTwiceOnOneLineStaysFatalOnASplitLedger(t *testing.T) {
	w := mwSplit(t)
	files := mwFiles(w)
	a2 := files[1] // A's sequence 2
	a2ID := string(model.ID(a2[9 : len(a2)-5]))
	name, err := model.BundleName(3, model.ID(a2ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := mwBundles(w)[name]; ok {
		t.Fatalf("fixture: %s already exists", name)
	}
	mwEdit(t, w, a2, name, func(f map[string]any) { f["sequence"] = json.Number("3"); f["predecessor"] = a2ID })
	mwRefuses(t, w, reduce.CodeDuplicateCommand, "one command id published twice on one line")
}

// Replay and the ledger read share one canonical order: sequence, then command
// id. Bundles handed to Replay in any other order are refused, never sorted.
func TestLedgerMergeCanonicalOrderIsTheOnlyOrderReplayAccepts(t *testing.T) {
	m := mwSplit(t)
	p, err := store.Discover(m.root)
	if err != nil {
		t.Fatal(err)
	}
	bundles, err := store.ReadPrefix(p)
	if err != nil {
		t.Fatalf("the split ledger must read: %v", err)
	}
	if !sort.SliceIsSorted(bundles, func(i, j int) bool { return bundles[i].Ref().Before(bundles[j].Ref()) }) || len(bundles) != 4 {
		t.Fatalf("the ledger read returned %d bundles not in sequence-then-command-id order", len(bundles))
	}
	if _, err := reduce.Replay(bundles); err != nil {
		t.Fatalf("control: the canonical order must replay: %v", err)
	}
	// bundles are base, A2, B2, A3 (see mwSplit).
	for _, swap := range [][2]int{{1, 2}, {2, 3}, {1, 3}} {
		shuffled := append([]model.Bundle(nil), bundles...)
		shuffled[swap[0]], shuffled[swap[1]] = shuffled[swap[1]], shuffled[swap[0]]
		if _, err := reduce.Replay(shuffled); err == nil {
			t.Errorf("Replay accepted bundles with positions %d and %d exchanged; only the canonical order may replay", swap[0], swap[1])
		}
	}
}

// `whosaidso help branches` explains recording on branches in plain words.
func TestLedgerMergeHelpExplainsBranchesAndSetAsideRecords(t *testing.T) {
	w := mwWorld(t, 100)
	out, err := w.cli(nil, "help", "branches")
	if err != nil {
		t.Fatalf("whosaidso help branches: %v", err)
	}
	text := strings.ToLower(string(out))
	for name, ok := range map[string]bool{
		"merging":              strings.Contains(text, "merge"),
		"branches":             strings.Contains(text, "branch"),
		"what set aside means": strings.Contains(text, "set aside"),
		"how to re-record":     mwRerecord.MatchString(text),
	} {
		if !ok {
			t.Errorf("help branches does not cover %s:\n%s", name, out)
		}
	}
	index, err := w.cli(nil, "help")
	if err != nil || !strings.Contains(string(index), "branches") {
		t.Errorf("whosaidso help does not list the branches topic: %v\n%s", err, index)
	}
}
