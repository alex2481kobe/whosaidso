package reduce

// Replay-order supersession rules: one supersession per covered revision, no
// cycles, named authority when an owner ruling is affected, and the superseded
// record staying in the snapshot. Disposal loss listing (DisposalLoss) is here
// too because it is the reducer half of the gate's loss accounting.

import (
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func supersedeLedger(t *testing.T) (*ledgerBuilder, model.RecordRef, model.RecordRef, model.RecordRef) {
	t.Helper()
	l := newLedger()
	a, b, c := ref(newID("SPA"), 1), ref(newID("SPB"), 1), ref(newID("SPC"), 1)
	for _, r := range []model.RecordRef{a, b, c} {
		l.add(t, &model.ClaimAssert{ID: r.RecordID, Provenance: provenance("agent"), Spec: claimSpec()})
	}
	return l, a, b, c
}

func TestSupersedeOnceAndNeverIntoACycle(t *testing.T) {
	l, a, b, c := supersedeLedger(t)
	l.add(t, &model.Supersede{Prior: a, Replacement: b, Reason: "b restates a"})
	s := mustReplay(t, l.out)
	if _, ok := s.Record(a); !ok || len(s.Supersessions()) != 1 {
		t.Fatal("the superseded record left the snapshot")
	}
	for _, tc := range []struct {
		name string
		e    *model.Supersede
		code string
	}{
		{"twice", &model.Supersede{Prior: a, Replacement: c, Reason: "again"}, CodeAlreadySuperseded},
		{"direct cycle", &model.Supersede{Prior: b, Replacement: a, Reason: "back"}, CodeSupersedeCycle},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := *l
			bad.out = append([]model.Bundle(nil), l.out...)
			bad.add(t, tc.e)
			_, err := Replay(bad.out)
			wantFault(t, err, tc.code)
		})
	}
	// A longer cycle: a → b, b → c, then c → a.
	l.add(t, &model.Supersede{Prior: b, Replacement: c, Reason: "c restates b"})
	mustReplay(t, l.out)
	l.add(t, &model.Supersede{Prior: c, Replacement: a, Reason: "full circle"})
	_, err := Replay(l.out)
	wantFault(t, err, CodeSupersedeCycle)
}

func TestSupersedeOwnerRulingNeedsNamedAuthority(t *testing.T) {
	l := newLedger()
	d, e := ref(newID("SPD"), 1), ref(newID("SPE"), 1)
	l.add(t, &model.DecisionOpen{ID: d.RecordID, Provenance: provenance("agent"), Spec: decisionSpec()},
		&model.DecisionOpen{ID: e.RecordID, Provenance: provenance("agent"), Spec: decisionSpec()})
	// No ruling yet: nothing an owner ruled is affected.
	free := *l
	free.out = append([]model.Bundle(nil), l.out...)
	free.add(t, &model.Supersede{Prior: d, Replacement: e, Reason: "restated before any ruling"})
	mustReplay(t, free.out)

	l.add(t, &model.DecisionDispose{Decision: d, Disposition: "approved", Quote: "yes", Scope: testScope(), Authority: rulingAuthority("owner")})
	for name, authority := range map[string]*model.Authority{"absent": nil, "unnamed": {Actor: model.Actor{UnknownReason: "nobody recorded who"}, SourceRef: blobRef("r"), Selector: model.Selector{Kind: "whole"}, Scope: testScope()}} {
		t.Run(name, func(t *testing.T) {
			bad := *l
			bad.out = append([]model.Bundle(nil), l.out...)
			bad.add(t, &model.Supersede{Prior: d, Replacement: e, Reason: "restated", Authority: authority})
			_, err := Replay(bad.out)
			wantFault(t, err, CodeAuthorityUnavailable)
		})
	}
	l.add(t, &model.Supersede{Prior: d, Replacement: e, Reason: "restated", Authority: ptrProof(rulingAuthority("owner"))})
	s := mustReplay(t, l.out)
	if p, ok := s.DecisionAt(d); !ok || p.Status != StatusDecided || len(p.Dispositions) != 1 {
		t.Fatalf("the superseded ruling is no longer visible: %+v", p)
	}
}

func TestDisposalLossNamesEveryDependentRevision(t *testing.T) {
	l := proofLedger(t, true)
	claim := ref(newID("CMA1"), 1)
	artifact := blobRef("result")
	e := model.ArtifactDispose{Artifact: artifact, Digest: artifact.Content.SHA256, PreviousLocation: "out/result", SupportLoss: []model.SupportLoss{}, Authority: rulingAuthority("owner")}
	lost := mustReplay(t, l.out).DisposalLoss(e)
	found := false
	for _, r := range lost {
		found = found || r == claim
	}
	if !found {
		t.Fatalf("the proven claim citing the artifact is not in the loss: %+v", lost)
	}
	unrelated := blobRef("never-cited")
	e.Artifact, e.Digest = unrelated, unrelated.Content.SHA256
	if lost := mustReplay(t, l.out).DisposalLoss(e); len(lost) != 0 {
		t.Fatalf("an uncited artifact reports loss: %+v", lost)
	}
}
