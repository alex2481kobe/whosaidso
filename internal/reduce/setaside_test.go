package reduce

// Tests for replaying a merged split chain (setaside.go): what applies, what
// is set aside and why, what stays fatal, and that every route to a
// snapshot (Replay, Apply, a restored image) agrees.

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// branch continues l on its own line of history, minting ids no other branch
// mints. Both branches share every bundle l had published.
func (l *ledgerBuilder) branch(names uint64) *ledgerBuilder {
	holders := map[model.ID]model.Actor{}
	for k, v := range l.holders {
		holders[k] = v
	}
	return &ledgerBuilder{project: l.project, seq: l.seq, prev: l.prev,
		out: append([]model.Bundle(nil), l.out...), holders: holders, names: names}
}

// merged is every bundle of the branches, once each, in canonical order: what
// a merge commit and a squash both leave in the ledger folder.
func merged(branches ...*ledgerBuilder) []model.Bundle {
	seen := map[model.BundleRef]bool{}
	var out []model.Bundle
	for _, br := range branches {
		for _, b := range br.out {
			if !seen[b.Ref()] {
				seen[b.Ref()] = true
				out = append(out, b)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref().Before(out[j].Ref()) })
	return out
}

func amendTask(id string, rev model.Revision, intent string) *model.TaskAmend {
	return &model.TaskAmend{Provenance: provenance("agent-a"), Target: ref(newID(id), rev), Replacement: taskSpec(withIntent(intent))}
}

func createTask(id string) *model.TaskCreate {
	return &model.TaskCreate{Provenance: provenance("agent-a"), ID: newID(id), Spec: taskSpec()}
}

func setAsideRefs(s Snapshot) []model.BundleRef {
	var out []model.BundleRef
	for _, v := range s.SetAside() {
		out = append(out, model.BundleRef{Sequence: v.Sequence, CommandID: v.CommandID})
	}
	return out
}

func TestMergedIndependentBranchesApplyEverything(t *testing.T) {
	base := goodLedger(t)
	a, b := base.branch(100), base.branch(200)
	a.add(t, createTask("TSKB"))
	a.add(t, createTask("TSKC"))
	b.add(t, createTask("TSKD"))
	s := mustReplay(t, merged(a, b))
	if got := s.SetAside(); len(got) != 0 {
		t.Fatalf("independent branches clash nowhere, set aside %+v", got)
	}
	for _, id := range []string{"TSKA", "TSKB", "TSKC", "TSKD"} {
		if _, ok := s.Current(Ident{Project: testProject, ID: newID(id)}); !ok {
			t.Errorf("%s from one of the branches is missing", id)
		}
	}
	// Two bundles share sequence 3; their events stay apart by position.
	b3, _ := s.Current(Ident{Project: testProject, ID: newID("TSKD")})
	a3, _ := s.Current(Ident{Project: testProject, ID: newID("TSKB")})
	if a3.Origin == b3.Origin || a3.Origin.Sequence != 3 || b3.Origin.Sequence != 4 {
		t.Fatalf("origins must be canonical positions: %+v %+v", a3.Origin, b3.Origin)
	}
	if w := s.Watermark(); w.Sequence != 4 || w.Bundles != 5 || w.CommandID != a.out[3].CommandID {
		t.Fatalf("watermark must name the canonical last and count every bundle read: %+v", w)
	}
}

func TestMergedClashSetsAsideTheLaterBundleAndWhatNamesAFactItNeverSaw(t *testing.T) {
	base := goodLedger(t)
	a, b := base.branch(100), base.branch(200)
	a.add(t, amendTask("TSKA", 1, "amended on a"))
	b.add(t, amendTask("TSKA", 1, "amended on b"))
	// b's next bundle amends the revision b made. Replay would accept it on
	// a's revision 2 by number alone; it must not apply to facts b never saw.
	b.add(t, amendTask("TSKA", 2, "built on b"))
	// Recorded on top of both, but naming nothing either published: it stands.
	b.add(t, createTask("TSKE"))
	s := mustReplay(t, merged(a, b))
	want := []model.BundleRef{b.out[2].Ref(), b.out[3].Ref()}
	if got := setAsideRefs(s); !reflect.DeepEqual(got, want) {
		t.Fatalf("set aside %v, want %v", got, want)
	}
	codes := []string{CodeRevisionConflict, CodeUnseenFact}
	for i, v := range s.SetAside() {
		if v.Code != codes[i] || v.Reason == "" {
			t.Errorf("set-aside %d is %q (%s), want %q with its fault", i, v.Code, v.Reason, codes[i])
		}
	}
	if got := s.SetAside()[1].Reason; !strings.Contains(got, string(a.out[2].CommandID)) {
		t.Errorf("an unseen fact must name the bundle that published it: %s", got)
	}
	task, _ := s.Current(Ident{Project: testProject, ID: newID("TSKA")})
	if task.Key.Revision != 2 || task.Task.Intent != "amended on a" {
		t.Fatalf("the earlier branch's amendment must stand alone: rev %d %q", task.Key.Revision, task.Task.Intent)
	}
	if _, ok := s.Current(Ident{Project: testProject, ID: newID("TSKE")}); !ok {
		t.Fatal("a bundle that names nothing it did not see must be applied")
	}
}

// A joining admission saw the clash and the set-aside bundle. Another branch
// merged later sorts before it and makes it miss one bundle, wherever that
// branch's id falls; the join names nothing from it, so it stays applied.
func TestAJoinStaysAppliedWhenAnotherBranchMergesLater(t *testing.T) {
	for _, names := range []uint64{50, 150, 300} { // before a, between a and b, after b
		base := goodLedger(t)
		a, b, c := base.branch(100), base.branch(200), base.branch(names)
		a.add(t, amendTask("TSKA", 1, "amended on a"))
		b.add(t, amendTask("TSKA", 1, "amended on b"))
		c.add(t, createTask("TSKC"))
		join := mergingBundle(t, mustReplay(t, merged(a, b)), createTask("TSKJ"))
		s := mustReplay(t, append(merged(a, b, c), join))
		if got := setAsideRefs(s); !reflect.DeepEqual(got, []model.BundleRef{b.out[2].Ref()}) {
			t.Fatalf("names %d: only the clash is set aside, got %v", names, got)
		}
		for _, id := range []string{"TSKC", "TSKJ"} {
			if _, ok := s.Current(Ident{Project: testProject, ID: newID(id)}); !ok {
				t.Fatalf("names %d: %s must be applied", names, id)
			}
		}
	}
}

func TestASetAsideBundleLeavesNoPartOfItself(t *testing.T) {
	base := goodLedger(t)
	a, b := base.branch(100), base.branch(200)
	a.add(t, amendTask("TSKA", 1, "amended on a"))
	// Its first event is fine on its own; its second clashes. All or nothing.
	b.add(t, createTask("TSKG"), amendTask("TSKA", 1, "amended on b"))
	s := mustReplay(t, merged(a, b))
	if got := setAsideRefs(s); !reflect.DeepEqual(got, []model.BundleRef{b.out[2].Ref()}) {
		t.Fatalf("set aside %v", got)
	}
	if _, ok := s.Current(Ident{Project: testProject, ID: newID("TSKG")}); ok {
		t.Fatal("an event before the clash in a set-aside bundle was applied")
	}
}

// The whole state, not one record: a set-aside bundle whose events write every
// kind of fact before its clash leaves the state exactly as a bundle carrying
// only the clash does.
func TestASetAsideBundleUndoesEveryKindOfWrite(t *testing.T) {
	claim := ref(newID("CMW1"), 1)
	busy := []model.TypedEvent{
		createTask("TSKW"),
		&model.ClaimAssert{ID: claim.RecordID, Provenance: provenance("agent-a"), Spec: claimSpec()},
		fixProofCriterion(claim),
		&model.TaskStart{Task: ref(newID("TSKW"), 1), Actor: model.Actor{ID: "agent-a"}, AttemptID: newID("ATTW")},
		&model.BlockerHold{Task: ref(newID("TSKW"), 1), BlockerID: newID("HDW1"),
			Reason: model.BlockerAwaitingAcceptance, Actor: model.Actor{ID: "owner"}, Criterion: "owner reads it"},
		&model.SourceIntake{SourceID: newID("SRCW"), OriginalDigest: newDigest("said"), Length: 4, Speaker: model.Actor{ID: "owner"}, SourceRef: blobRef("said"), Referents: []model.RecordRef{claim}},
	}
	clash := amendTask("TSKA", 1, "amended on b")
	fold := func(events ...model.TypedEvent) *state {
		base := goodLedger(t)
		a, b := base.branch(100), base.branch(200)
		a.add(t, amendTask("TSKA", 1, "amended on a"))
		b.add(t, events...)
		alone := mustReplay(t, b.out) // control: on its own branch every event applies
		if len(alone.SetAside()) != 0 {
			t.Fatal("control: the bundle must apply on its own branch")
		}
		st := mustReplay(t, merged(a, b)).inner()
		if len(st.setAside) != 1 {
			t.Fatalf("the bundle must be set aside, got %v", st.setAside)
		}
		st.setAside, st.watermark = nil, Watermark{} // the reason names the event index; the watermark counts events
		return st
	}
	if got, want := fold(append(busy, clash)...), fold(clash); !reflect.DeepEqual(got, want) {
		t.Fatal("a set-aside bundle left a write behind")
	}
}

func TestDuplicateCommandOnTwoBranchesIsSetAside(t *testing.T) {
	base := goodLedger(t)
	a, b := base.branch(100), base.branch(200)
	a.add(t, createTask("TSKB"))
	b.add(t, createTask("TSKD"))
	copied := a.out[2]
	copied.Sequence, copied.Predecessor = 4, b.out[2].CommandID
	all := append(merged(a, b), copied)
	s := mustReplay(t, all)
	got := s.SetAside()
	if len(got) != 1 || got[0].Sequence != 4 || got[0].Code != CodeDuplicateCommand {
		t.Fatalf("the later copy must be set aside as a duplicate admission: %+v", got)
	}
}

func TestBundleThatSawEverythingAndFailsStaysFatal(t *testing.T) {
	base := goodLedger(t)
	a, b := base.branch(100), base.branch(200)
	a.add(t, createTask("TSKB"))
	b.add(t, createTask("TSKD"))
	prefix := mustReplay(t, merged(a, b))
	// A merging bundle names every head, so it saw everything; a stale write
	// in it is damage, exactly as on a ledger that never split.
	bad := mergingBundle(t, prefix, amendTask("TSKA", 7, "stale"))
	if _, err := Replay(append(merged(a, b), bad)); err == nil {
		t.Fatal("a bundle that descends from everything and fails must stay fatal")
	}
	if _, err := Apply(prefix, bad); err == nil {
		t.Fatal("Apply must refuse it too")
	}
}

func TestFirstAdmissionAfterAMergeDescendsFromEverything(t *testing.T) {
	base := goodLedger(t)
	a, b := base.branch(100), base.branch(200)
	a.add(t, amendTask("TSKA", 1, "amended on a"))
	b.add(t, amendTask("TSKA", 1, "amended on b"))
	prefix := mustReplay(t, merged(a, b))
	m := mergingBundle(t, prefix, createTask("TSKF"))
	if m.Sequence != 4 || m.Predecessor != b.out[2].CommandID || !reflect.DeepEqual(m.OtherHeads, []model.BundleRef{a.out[2].Ref()}) {
		t.Fatalf("merging bundle is %v after %s naming %v", m.Ref(), m.Predecessor, m.OtherHeads)
	}
	after, err := Apply(prefix, m)
	if err != nil {
		t.Fatalf("the merging admission must apply: %v", err)
	}
	if got := setAsideRefs(after); !reflect.DeepEqual(got, []model.BundleRef{b.out[2].Ref()}) {
		t.Fatalf("only the clash stays set aside, got %v", got)
	}
	if next := after.Chain(model.Bundle{}); next.OtherHeads != nil || next.Predecessor != m.CommandID {
		t.Fatalf("after the merging admission the ledger has one head: %+v", next)
	}
	replayed := mustReplay(t, append(merged(a, b), m))
	if !reflect.DeepEqual(replayed, after) {
		t.Fatal("Apply after a merge and Replay of the same files disagree")
	}
}

func TestRestoreOfASplitLedgerAtEverySplitEqualsReplay(t *testing.T) {
	base := goodLedger(t)
	a, b := base.branch(100), base.branch(200)
	a.add(t, amendTask("TSKA", 1, "amended on a"))
	a.add(t, createTask("TSKB"))
	b.add(t, amendTask("TSKA", 1, "amended on b"))
	b.add(t, createTask("TSKD"))
	all := merged(a, b)
	prefix := mustReplay(t, all)
	all = append(all, mergingBundle(t, prefix, createTask("TSKF")))
	want := mustReplay(t, all)
	for k := 0; k <= len(all); k++ {
		got, err := RestoreSnapshot(mustEncode(t, mustReplay(t, all[:k])), all[k:])
		if err != nil {
			t.Fatalf("split %d: %v", k, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("restoring %d bundles and folding the rest differs from Replay", k)
		}
	}
}

// mergingBundle is the bundle an admission writes on prefix: one carrying
// events, stamped by the chain rule admission uses.
func mergingBundle(t *testing.T, prefix Snapshot, events ...model.TypedEvent) model.Bundle {
	t.Helper()
	raw := make([]model.Event, 0, len(events))
	for _, e := range events {
		enc, err := model.EncodeEvent(e)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, enc)
	}
	b := prefix.Chain(model.Bundle{Version: model.WireVersion, Project: testProject, CommandID: newID(fmt.Sprintf("CMDM%d", len(raw))),
		RequestDigest: newDigest("merge"), Admitter: model.Actor{ID: "coordinator"}, RecordedAt: baseTime.Add(time.Hour),
		Packets: []model.PacketRef{{CommandID: newID("PKTM"), Digest: newDigest("pktm")}}, Events: raw})
	return b
}
