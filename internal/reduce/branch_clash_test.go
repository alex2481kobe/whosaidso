package reduce

// Clashes two branches make (branch_clash.go): a duplicate source order and a
// competing disposition set the later bundle aside, while the same pair on
// one line still replays; setaside.resolve records who dealt with a
// set-aside bundle, first one kept, repeats and strays no-ops.

import (
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func ownerSource(id string, order uint64) *model.SourceIntake {
	return &model.SourceIntake{SourceID: newID(id), OriginalDigest: newDigest(id), Length: uint64(len(id)), Speaker: model.Actor{ID: "owner"},
		Order: order, SourceRef: blobRef(id), Referents: []model.RecordRef{}}
}

func onlySetAside(t *testing.T, s Snapshot, code string) SetAside {
	t.Helper()
	aside := s.SetAside()
	if len(aside) != 1 || aside[0].Code != code {
		t.Fatalf("want one bundle set aside for %s, got %+v", code, aside)
	}
	return aside[0]
}

func TestDuplicateSourceOrderClashesOnlyAcrossBranches(t *testing.T) {
	line := goodLedger(t)
	line.add(t, ownerSource("SRCA", 7))
	line.add(t, ownerSource("SRCB", 7))
	if got := mustReplay(t, line.out).SetAside(); len(got) != 0 {
		t.Fatalf("a duplicate order on one line replays as before: %+v", got)
	}
	base := goodLedger(t)
	a, b := base.branch(100), base.branch(200)
	a.add(t, ownerSource("SRCA", 7))
	b.add(t, ownerSource("SRCB", 7))
	onlySetAside(t, mustReplay(t, merged(a, b)), CodeDuplicateSourceOrder)
	c := base.branch(300)
	c.add(t, ownerSource("SRCC", 8))
	if got := mustReplay(t, merged(a, c)).SetAside(); len(got) != 0 {
		t.Fatalf("different orders never clash: %+v", got)
	}
}

func TestCompetingDispositionClashesOnlyAcrossBranches(t *testing.T) {
	d := ref(newID("DCSA"), 1)
	open := func() *ledgerBuilder {
		l := goodLedger(t)
		l.add(t, &model.DecisionOpen{ID: d.RecordID, Provenance: provenance("author"), Spec: decisionSpec()})
		return l
	}
	line := open()
	line.add(t, dispose(d, "approved"))
	line.add(t, dispose(d, "rejected"))
	if got := mustReplay(t, line.out).SetAside(); len(got) != 0 {
		t.Fatalf("two dispositions on one line both stand, as before: %+v", got)
	}
	base := open()
	a, b := base.branch(100), base.branch(200)
	a.add(t, dispose(d, "approved"))
	b.add(t, dispose(d, "rejected"))
	s := mustReplay(t, merged(a, b))
	onlySetAside(t, s, CodeCompetingDisposition)
	if p, _ := s.DecisionAt(d); len(p.Dispositions) != 1 {
		t.Fatalf("only the disposition first in order applies, never the last: %+v", p.Dispositions)
	}
}

func TestSetAsideResolveKeepsTheFirstAndIgnoresRepeatsAndStrays(t *testing.T) {
	base := goodLedger(t)
	a, b := base.branch(100), base.branch(200)
	a.add(t, ownerSource("SRCA", 7))
	b.add(t, ownerSource("SRCB", 7))
	all := merged(a, b)
	aside := onlySetAside(t, mustReplay(t, all), CodeDuplicateSourceOrder)
	after := &ledgerBuilder{project: testProject, seq: all[len(all)-1].Sequence, prev: all[len(all)-1].CommandID, out: all, names: 300}
	ref := model.BundleRef{Sequence: aside.Sequence, CommandID: aside.CommandID}
	after.add(t, &model.SetAsideResolve{Bundle: ref, Reason: "the same message captured twice; it stays out"})
	after.add(t, &model.SetAsideResolve{Bundle: ref, Reason: "a repeat"},
		&model.SetAsideResolve{Bundle: model.BundleRef{Sequence: 1, CommandID: all[0].CommandID}, Reason: "not set aside"})
	s := mustReplay(t, after.out)
	got := onlySetAside(t, s, CodeDuplicateSourceOrder)
	if got.Resolved == nil || got.Resolved.Reason != "the same message captured twice; it stays out" {
		t.Fatalf("the first resolution is kept: %+v", got.Resolved)
	}
	if _, err := Apply(mustReplay(t, after.out[:len(after.out)-1]), after.out[len(after.out)-1]); err != nil {
		t.Fatalf("a repeated or stray resolution is never refused: %v", err)
	}
}
