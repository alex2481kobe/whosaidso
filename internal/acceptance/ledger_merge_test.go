// A ledger that two people extended on separate git branches must read and
// accept new records after the branches merge, with no extra step.
//
// What belongs here: ledgers that SPLIT (several bundles at one sequence), built
// the way a merge commit or a squash merge builds them, and what every read, the
// next admission and the views must then do. What does not: the ordinary
// linear-ledger rules (ledger_test.go, store_test.go) or the bundle fields'
// names, which the rules leave open; every check here goes through the CLI or
// the files a branch publishes.
//
// A branch is a copy of one ledger directory that admits on its own machine
// (its own HOME, so its own intake and its own project binding). A merge is the
// union of the branches' files in one directory; a merge commit and a squash
// merge both produce exactly that, and no git is involved. Every fork fixture
// first proves each branch reads cleanly alone, so a failure below is the merge
// and not the fixture.
package acceptance_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// mwReads are the read commands whose success a merged ledger must keep.
var mwReads = [][]string{
	{"todo"}, {"todo", "--json"}, {"show"}, {"show", "--json"}, {"history"}, {"history", "--json"},
}

// mwRerecord is the instruction a set-aside record's view must carry, however
// the sentence is worded.
var mwRerecord = regexp.MustCompile(`(?i)re-?record|record\w*\s+(it|them|this|that)\s+again`)

// mwLinearFields are the top-level keys every bundle of a ledger that never
// split has always carried.
var mwLinearFields = map[string]bool{"version": true, "project": true, "sequence": true, "command_id": true, "predecessor": true,
	"request_digest": true, "admitter": true, "recorded_at": true, "packets": true, "events": true}

// ---- fixtures ----------------------------------------------------------------

// mwWorld is a project on its own machine whose ids start at n, so two branches
// never mint the same id by accident.
func mwWorld(t *testing.T, n int) *flowWorld {
	t.Helper()
	w := flowNew(t)
	w.n = n
	return w
}

// mwBundles is the published bundle files of w's ledger, by name; the lock and
// other dotfiles are not part of what a branch carries.
func mwBundles(w *flowWorld) map[string][]byte {
	out := map[string][]byte{}
	for name, body := range w.ledger() {
		if !strings.HasPrefix(name, ".") {
			out[name] = body
		}
	}
	return out
}

// mwFiles lists published bundle names in canonical order.
func mwFiles(w *flowWorld) []string {
	var names []string
	for name := range mwBundles(w) {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func mwRemove(t *testing.T, w *flowWorld, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(w.root, ".whosaidso", "events", name)); err != nil {
		t.Fatal(err)
	}
}

// mwCopy publishes into dst every bundle file of src that dst lacks, as a git
// merge does. A name both hold must hold the same bytes.
func mwCopy(t *testing.T, src map[string][]byte, dst *flowWorld) {
	t.Helper()
	have := mwBundles(dst)
	for name, body := range src {
		if old, ok := have[name]; ok {
			if !bytes.Equal(old, body) {
				t.Fatalf("fixture: %s is in both branches with different bytes", name)
			}
			continue
		}
		dst.put(".whosaidso/events/"+name, body)
	}
}

// mwCheckout makes w's ledger hold exactly these bundle files, as switching
// branches in one checkout does; the machine's intake stays.
func mwCheckout(t *testing.T, w *flowWorld, files map[string][]byte) {
	t.Helper()
	for name := range mwBundles(w) {
		mwRemove(t, w, name)
	}
	mwCopy(t, files, w)
}

// mwFork is a new machine that has checked out src's ledger, ids from n.
func mwFork(t *testing.T, src *flowWorld, n int) *flowWorld {
	t.Helper()
	w := mwWorld(t, n)
	mwCopy(t, mwBundles(src), w)
	mwReadsOK(t, w, "a fresh checkout of the shared ledger")
	return w
}

// mwUnion assembles one directory from the branches' files, in the order given.
func mwUnion(t *testing.T, branches ...map[string][]byte) *flowWorld {
	t.Helper()
	m := mwWorld(t, 900)
	for _, b := range branches {
		mwCopy(t, b, m)
	}
	return m
}

// mwMerge is mwUnion over machines' ledgers.
func mwMerge(t *testing.T, branches ...*flowWorld) *flowWorld {
	t.Helper()
	var files []map[string][]byte
	for _, b := range branches {
		files = append(files, mwBundles(b))
	}
	return mwUnion(t, files...)
}

// mwReadAll binds the checkout as a fresh clone does, then runs every read and
// returns its output, or the first failure.
func mwReadAll(w *flowWorld) (map[string][]byte, error) {
	if _, err := w.cli(nil, "home", "."); err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, args := range mwReads {
		body, err := w.cli(nil, args...)
		if err != nil {
			return nil, err
		}
		out[strings.Join(args, " ")] = body
	}
	return out, nil
}

// mwReadsOK fails the test unless every read succeeds, and says which did not.
func mwReadsOK(t *testing.T, w *flowWorld, what string) map[string][]byte {
	t.Helper()
	before := mwBundles(w)
	out, err := mwReadAll(w)
	if err != nil {
		t.Fatalf("%s: every read must succeed on this ledger, but %v", what, err)
	}
	if !reflect.DeepEqual(before, mwBundles(w)) {
		t.Fatalf("%s: a read changed a published bundle file", what)
	}
	return out
}

func (w *flowWorld) mwClaimEvent(id model.ID, assertion string) *model.ClaimAssert {
	return &model.ClaimAssert{ID: id, Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}},
		Spec: model.ClaimSpec{Assertion: assertion, Falsifier: "the claim disappears on replay", Scope: w.scope, ExternalRefs: []model.ExternalReference{}}}
}

func (w *flowWorld) mwClaim(assertion string) model.ID {
	w.t.Helper()
	id := w.id()
	w.mustAdmit(flowAgent, w.mwClaimEvent(id, assertion))
	return id
}

// mwAdmitPacket admits an already captured packet under an explicit command id.
func (w *flowWorld) mwAdmitPacket(id, packet model.ID) error {
	w.t.Helper()
	_, err := w.cli(nil, "admit", "--json", "--command-id", string(id), "--actor", "coordinator", "--outcome", "accepted", "--reason", "merge review", string(packet))
	return err
}

// mwAdmit captures the events as one packet and admits it, returning the
// admission's command id.
func (w *flowWorld) mwAdmit(events ...model.TypedEvent) model.ID {
	w.t.Helper()
	packet := w.capture(flowAgent, events...)
	id := w.id()
	if err := w.mwAdmitPacket(id, packet); err != nil {
		w.t.Fatalf("control: the branch must admit on its own: %v", err)
	}
	return id
}

func (w *flowWorld) mwReviseEvent(claim model.ID, assertion string) *model.ClaimRevise {
	return &model.ClaimRevise{Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}}, Target: w.ref(claim, 1),
		Replacement: w.mwClaimEvent(claim, assertion).Spec}
}

// mwRecords is every record show lists, by id, with its label and revision.
func mwRecords(t *testing.T, w *flowWorld) map[model.ID][2]string {
	t.Helper()
	out, err := w.cli(nil, "show", "--json")
	if err != nil {
		t.Fatalf("show --json: %v", err)
	}
	got := map[model.ID][2]string{}
	for _, rec := range flowList(flowDecode(t, out), "records") {
		got[model.ID(flowStr(rec, "ref", "record_id"))] = [2]string{flowStr(rec, "label"), flowStr(rec, "current_revision")}
	}
	return got
}

func mwWantRecords(t *testing.T, w *flowWorld, ids ...model.ID) {
	t.Helper()
	have := mwRecords(t, w)
	for _, id := range ids {
		if _, ok := have[id]; !ok {
			t.Errorf("record %s from one branch is missing after the merge; show lists %d records", id, len(have))
		}
	}
}

// mwHeads returns the highest sequence and its last command id in canonical
// order (sequence, then command id) from the bundle files alone.
func mwHeads(w *flowWorld) (uint64, model.ID) {
	var seq uint64
	var id model.ID
	for name := range mwBundles(w) {
		if m := flowBundleName.FindStringSubmatch(name); m != nil {
			s, _ := strconv.ParseUint(m[1], 10, 64)
			if s > seq || (s == seq && model.ID(m[2]) > id) {
				seq, id = s, model.ID(m[2])
			}
		}
	}
	return seq, id
}

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
		var seqs []uint64
		var ids []model.ID
		for _, ev := range flowList(flowDecode(t, got["history --json"]), "events") {
			s, _ := strconv.ParseUint(flowStr(ev, "origin", "sequence"), 10, 64)
			seqs, ids = append(seqs, s), append(ids, model.ID(flowStr(ev, "command_id")))
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

// ---- real clashes: set aside, named, never fatal ------------------------------

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

// ---- damage stays fatal --------------------------------------------------------

// mwSplit is base, then A and B each with claims, merged: files sort base,
// A's sequence 2, B's sequence 2, A's sequence 3.
func mwSplit(t *testing.T) *flowWorld {
	t.Helper()
	base := mwWorld(t, 100)
	base.mwClaim("the shared base claim")
	a := mwFork(t, base, 200)
	a.mwClaim("recorded on branch A")
	a.mwClaim("recorded on branch A after its first")
	b := mwFork(t, base, 300)
	b.mwClaim("recorded on branch B")
	m := mwMerge(t, a, b)
	mwReadsOK(t, m, "the control: the undamaged split ledger")
	return m
}

// mwLinear is a ledger that never split.
func mwLinear(t *testing.T) *flowWorld {
	t.Helper()
	w := mwWorld(t, 100)
	for _, text := range []string{"one", "two", "three"} {
		w.mwClaim(text)
	}
	mwReadsOK(t, w, "the control: the undamaged linear ledger")
	return w
}

// mwEdit rewrites one published bundle file's top-level fields in place, or
// under a new name when rename is given.
func mwEdit(t *testing.T, w *flowWorld, name, rename string, edit func(map[string]any)) {
	t.Helper()
	var fields map[string]any
	dec := json.NewDecoder(bytes.NewReader(w.ledger()[name]))
	dec.UseNumber()
	if err := dec.Decode(&fields); err != nil {
		t.Fatal(err)
	}
	edit(fields)
	body, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if rename != "" {
		name = rename
	}
	w.put(".whosaidso/events/"+name, body)
}

// mwRefuses fails unless every read and every admission refuses. A code names
// the fault the refusal must carry; "" means only that it is not the fork
// refusal a merge used to cause, and "*" accepts any fault.
func mwRefuses(t *testing.T, w *flowWorld, code, what string) {
	t.Helper()
	for _, args := range mwReads {
		out, err := w.cli(nil, args...)
		switch {
		case err == nil:
			t.Errorf("%s: whosaidso %s succeeded; damage must stop every read, not be set aside", what, strings.Join(args, " "))
		case code == "":
			if strings.Contains(err.Error(), "ledger-fork") {
				t.Errorf("%s: whosaidso %s refused as a fork, which a merge is not: %v", what, strings.Join(args, " "), err)
			}
		case code != "*" && !strings.Contains(err.Error(), code):
			t.Errorf("%s: whosaidso %s refused without %s: %v", what, strings.Join(args, " "), code, err)
		}
		if err != nil && bytes.Contains(out, []byte(`"view"`)) {
			t.Errorf("%s: whosaidso %s refused after printing an answer:\n%s", what, strings.Join(args, " "), out)
		}
	}
	packet := w.capture(flowAgent, w.mwClaimEvent(w.id(), "after damage"))
	before := mwBundles(w)
	if err := w.mwAdmitPacket(w.id(), packet); err == nil {
		t.Errorf("%s: admission appended to a damaged ledger", what)
	}
	if !reflect.DeepEqual(before, mwBundles(w)) {
		t.Errorf("%s: a refused admission changed the ledger", what)
	}
}

func TestLedgerMergeStructuralDamageStaysFatal(t *testing.T) {
	cases := []struct {
		name, code string
		damage     func(t *testing.T, w *flowWorld, files []string)
	}{
		{"sequence hole", "ledger-discontinuity", func(t *testing.T, w *flowWorld, files []string) {
			last := files[len(files)-1]
			mwEdit(t, w, last, "00000009"+last[8:], func(f map[string]any) { f["sequence"] = json.Number("9") })
			mwRemove(t, w, last)
		}},
		{"predecessor names a bundle that is not there", "ledger-discontinuity", func(t *testing.T, w *flowWorld, files []string) {
			last := files[len(files)-1]
			mwEdit(t, w, last, last, func(f map[string]any) { f["predecessor"] = string(recID(777)) })
		}},
		{"predecessor names a bundle two sequences down", "ledger-discontinuity", func(t *testing.T, w *flowWorld, files []string) {
			last := files[len(files)-1]
			mwEdit(t, w, last, last, func(f map[string]any) { f["predecessor"] = files[0][9 : len(files[0])-5] })
		}},
		{"body sequence disagrees with its filename", "ledger-corrupt", func(t *testing.T, w *flowWorld, files []string) {
			mid := files[len(files)-2]
			mwEdit(t, w, mid, mid, func(f map[string]any) { f["sequence"] = json.Number("7") })
		}},
		{"bundle of another project", "ledger-corrupt", func(t *testing.T, w *flowWorld, files []string) {
			mid := files[len(files)-2]
			mwEdit(t, w, mid, mid, func(f map[string]any) { f["project"] = "someone/else" })
		}},
		{"unreadable bundle", "ledger-corrupt", func(t *testing.T, w *flowWorld, files []string) {
			w.put(".whosaidso/events/"+files[len(files)-2], []byte("{ not a bundle"))
		}},
		{"second genesis", "*", func(t *testing.T, w *flowWorld, files []string) {
			mid := files[len(files)-2]
			mwEdit(t, w, mid, "00000001"+mid[8:], func(f map[string]any) { delete(f, "predecessor"); f["sequence"] = json.Number("1") })
			mwRemove(t, w, mid)
		}},
	}
	for _, ledger := range []struct {
		name  string
		build func(*testing.T) *flowWorld
	}{{"linear ledger", mwLinear}, {"split ledger", mwSplit}} {
		for _, c := range cases {
			t.Run(ledger.name+"/"+c.name, func(t *testing.T) {
				w := ledger.build(t)
				c.damage(t, w, mwFiles(w))
				mwRefuses(t, w, c.code, c.name)
			})
		}
	}
}

// mwDuplicate publishes a copy of bundle `from` as the next sequence after it
// under a new command id: a bundle that descends from it and whose packet is
// admitted a second time, which no replay accepts.
func mwDuplicate(t *testing.T, w *flowWorld, from string, id model.ID) {
	t.Helper()
	var prev struct {
		Sequence  uint64
		CommandID model.ID `json:"command_id"`
	}
	if err := json.Unmarshal(w.ledger()[from], &prev); err != nil {
		t.Fatal(err)
	}
	name, err := model.BundleName(prev.Sequence+1, id)
	if err != nil {
		t.Fatal(err)
	}
	mwEdit(t, w, from, name, func(f map[string]any) {
		f["sequence"] = json.Number(strconv.FormatUint(prev.Sequence+1, 10))
		f["command_id"], f["predecessor"] = string(id), string(prev.CommandID)
	})
}

func TestLedgerMergeSemanticFailureThatSawEverythingIsDamageNotAClash(t *testing.T) {
	t.Run("ledger that never split", func(t *testing.T) {
		w := mwWorld(t, 100)
		w.mwClaim("one")
		w.mwClaim("two")
		mwReadsOK(t, w, "control: a never-split ledger")
		files := mwFiles(w)
		mwDuplicate(t, w, files[1], recID(801))
		mwRefuses(t, w, "", "a hand-written bundle that readmits a packet on a ledger that never split")
	})
	t.Run("split ledger, bundle that descends from every bundle before it", func(t *testing.T) {
		w := mwSplit(t)
		w.mwClaim("after the merge") // names every head
		mwReadsOK(t, w, "control: the merged ledger with its merging admission")
		files := mwFiles(w)
		mwDuplicate(t, w, files[len(files)-1], recID(802))
		mwRefuses(t, w, "", "a failing bundle that saw both branches")
	})
}

// ---- a ledger that never split reads as before ---------------------------------

func TestLedgerMergeLinearLedgerCarriesNoNewFieldAndNoNewKeys(t *testing.T) {
	w := mwWorld(t, 100)
	w.mwClaim("one")
	w.mwClaim("two")
	keys := func(raw []byte) []string {
		var top map[string]json.RawMessage
		if err := json.Unmarshal(raw, &top); err != nil {
			t.Fatal(err)
		}
		var got []string
		for k := range top {
			got = append(got, k)
		}
		sort.Strings(got)
		return got
	}
	check := func(when string) {
		for name, body := range mwBundles(w) {
			for _, key := range keys(body) {
				if !mwLinearFields[key] {
					t.Errorf("%s: %s carries the field %q; a ledger that never split must stay readable by earlier versions, which refuse unknown fields", when, name, key)
				}
			}
		}
		out := mwReadsOK(t, w, when)
		for _, view := range []string{"todo", "history", "show"} {
			text := strings.ToLower(string(out[view]))
			if strings.Contains(text, "set aside") || strings.Contains(text, "set-aside") || mwRerecord.MatchString(text) {
				t.Errorf("%s: `whosaidso %s` mentions set-aside records on a ledger that never split:\n%s", when, view, text)
			}
		}
		want := map[string][]string{
			"todo --json":    {"attention", "awaiting_acceptance", "blocked", "in_flight", "omitted", "open_decisions", "packets_not_accepted", "project", "ready", "result", "totals", "view", "watermark"},
			"history --json": {"events", "project", "result", "reviews", "view", "watermark"},
		}
		for cmd, wantKeys := range want {
			if got := keys(out[cmd]); !reflect.DeepEqual(got, wantKeys) {
				t.Errorf("%s: `whosaidso %s` has top-level keys %v, want exactly %v; a linear ledger answers as it always did", when, cmd, got, wantKeys)
			}
		}
		var todo map[string]json.RawMessage
		_ = json.Unmarshal(out["todo --json"], &todo)
		if got := keys(todo["totals"]); len(got) != 9 {
			t.Errorf("%s: todo totals has counters %v, want the 9 it has always had", when, got)
		}
		if got := keys(todo["watermark"]); !reflect.DeepEqual(got, []string{"bundles", "events", "head", "sequence"}) {
			t.Errorf("%s: todo watermark has keys %v, want bundles, events, head, sequence", when, got)
		}
	}
	check("before another admission")
	w.mwClaim("three")
	check("after one more admission")
}
