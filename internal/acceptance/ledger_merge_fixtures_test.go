// Shared fixtures for the ledger branch-merge specification: how a branch, a
// merge and a checkout switch are built from real admissions, and the helpers
// every merge test reads through. What belongs here: fixture builders and
// read helpers. What does not: an assertion about what a rule requires; those
// live in the ledger_merge_*_test.go file for that rule.
//
// A branch is a copy of one ledger directory that admits on its own machine
// (its own HOME, so its own intake and its own project binding). A merge is the
// union of the branches' files in one directory; a merge commit and a squash
// merge both produce exactly that, and no git is involved. Every fork fixture
// first proves each branch reads cleanly alone, so a failure in a merge test is
// the merge and not the fixture.
package acceptance_test

import (
	"bytes"
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
	return w.mwReviseAt(claim, 1, assertion)
}

// mwReviseAt revises the claim from the given revision.
func (w *flowWorld) mwReviseAt(claim model.ID, revision model.Revision, assertion string) *model.ClaimRevise {
	return &model.ClaimRevise{Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}}, Target: w.ref(claim, revision),
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
