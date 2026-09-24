package reduce

// The ordered event inventories: they equal a sort of the admitted events at
// every prefix, and two Applys onto one snapshot never share an inventory slot.

import (
	"fmt"
	"reflect"
	"sort"
	"testing"

	"whosaidso/internal/model"
)

// lossLedger admits every inventoried event type at least once, spread over
// bundles and mixed with other events inside a bundle.
func lossLedger(t *testing.T) *ledgerBuilder {
	t.Helper()
	l := proofLedger(t, true)
	claim := ref(newID("CMA1"), 1)
	x, y := ref(newID("SPX"), 1), ref(newID("SPY"), 1)
	d := ref(newID("DCN"), 1)
	l.add(t, &model.ClaimAssert{ID: x.RecordID, Provenance: provenance("lane-a"), Spec: claimSpec()},
		&model.ClaimAssert{ID: y.RecordID, Provenance: provenance("lane-a"), Spec: claimSpec()},
		&model.DecisionOpen{ID: d.RecordID, Provenance: provenance("lane-a"), Spec: decisionSpec()})
	l.add(t, &model.Supersede{Prior: x, Replacement: y, Reason: "y restates x"}, withdrawal())
	l.add(t, &model.DecisionDispose{Decision: d, Disposition: "approved", Quote: "yes", Scope: testScope(), Authority: rulingAuthority("owner")},
		&model.Correction{Target: model.CorrectionTarget{Kind: "record", Record: &claim}, AffectedRevisions: []model.RecordRef{claim}, Reason: "counterexample", CorrectiveRef: blobRef("counterexample")})
	artifact := blobRef("result")
	l.add(t, &model.ArtifactDispose{Artifact: artifact, Digest: artifact.Content.SHA256, PreviousLocation: "out/result", SupportLoss: []model.SupportLoss{}, Authority: rulingAuthority("owner")})
	return l
}

func sortedOrigins(st *state, keep func(model.TypedEvent) bool) []Origin {
	out := []Origin{}
	for o, e := range st.events {
		if keep(e) {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].before(out[j]) })
	return out
}

func TestEventLogIsLedgerOrderAtEveryPrefix(t *testing.T) {
	bundles := lossLedger(t).bundles()
	is := func(types ...model.EventType) func(model.TypedEvent) bool {
		return func(e model.TypedEvent) bool {
			for _, typ := range types {
				if e.EventType() == typ {
					return true
				}
			}
			return false
		}
	}
	var s Snapshot
	for i, b := range bundles {
		var err error
		s, err = Apply(s, b)
		if err != nil {
			t.Fatalf("apply bundle %d: %v", i+1, err)
		}
		replayed := mustReplay(t, bundles[:i+1])
		for name, st := range map[string]*state{"apply": s.st, "replay": replayed.st} {
			l := st.log
			for list, pair := range map[string][2][]Origin{
				"all":               {l.all, sortedOrigins(st, func(model.TypedEvent) bool { return true })},
				"losses":            {l.losses, sortedOrigins(st, is("correction", "trust.withdraw", "supersede", "artifact.dispose"))},
				"proofs":            {l.proofs, sortedOrigins(st, is("proof.admit"))},
				"decisionDisposals": {l.decisionDisposals, sortedOrigins(st, is("decision.dispose"))},
				"supersessions":     {l.supersessions, sortedOrigins(st, is("supersede"))},
				"corrections":       {l.corrections, sortedOrigins(st, is("correction"))},
				"withdrawals":       {l.withdrawals, sortedOrigins(st, is("trust.withdraw"))},
				"disposals":         {l.disposals, sortedOrigins(st, is("artifact.dispose"))},
			} {
				got, want := append([]Origin{}, pair[0]...), pair[1]
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s prefix %d: %s inventory %v, sorted events %v", name, i+1, list, got, want)
				}
			}
		}
	}
	// The ledger really exercised every inventory.
	l := s.st.log
	for name, list := range map[string][]Origin{"proofs": l.proofs, "decisionDisposals": l.decisionDisposals, "supersessions": l.supersessions,
		"corrections": l.corrections, "withdrawals": l.withdrawals, "disposals": l.disposals} {
		if len(list) == 0 {
			t.Fatalf("fixture admitted no %s", name)
		}
	}
}

// Two different candidates applied to one snapshot must each see only their own
// events. Without fork's capacity cap the second append would land in the
// backing array the first result still reads. Prior loss counts vary so some
// base has spare capacity in its losses list.
func TestTwoAppliesOntoOneSnapshotShareNoInventory(t *testing.T) {
	instrument := ref(newID("HNSS"), 1)
	claim := ref(newID("CMA1"), 1)
	for prior := 0; prior < 6; prior++ {
		t.Run(fmt.Sprintf("prior%d", prior), func(t *testing.T) {
			l := proofLedger(t, true)
			for i := 0; i < prior; i++ {
				l.add(t, &model.Correction{Target: model.CorrectionTarget{Kind: "record", Record: &claim}, AffectedRevisions: []model.RecordRef{claim}, Reason: "earlier counterexample", CorrectiveRef: blobRef(fmt.Sprintf("earlier-%d", i))})
			}
			base := mustReplay(t, l.out)
			one, two := *l, *l
			one.out = append([]model.Bundle(nil), l.out...)
			two.out = append([]model.Bundle(nil), l.out...)
			// The withdrawal is the second event; the correction the first.
			b1 := one.add(t, &model.ClaimAssert{ID: newID("SPZ"), Provenance: provenance("lane-a"), Spec: claimSpec()}, withdrawal())
			b2 := two.add(t, &model.Correction{Target: model.CorrectionTarget{Kind: "record", Record: &claim}, AffectedRevisions: []model.RecordRef{claim}, Reason: "new counterexample", CorrectiveRef: blobRef("new")})
			s1, err := Apply(base, b1)
			if err != nil {
				t.Fatal(err)
			}
			s2, err := Apply(base, b2)
			if err != nil {
				t.Fatal(err)
			}
			if got := supportNow(t, s1, instrument); got.ActiveTrust != TruthFalse || len(got.Losses) != 1 {
				t.Fatalf("the first Apply lost its withdrawal to the second: %+v", got)
			}
			if len(s1.Corrections()) != prior || len(s2.Corrections()) != prior+1 || len(s2.TrustWithdrawals()) != 0 {
				t.Fatalf("forks share inventories: s1 %d corrections, s2 %d corrections and %d withdrawals",
					len(s1.Corrections()), len(s2.Corrections()), len(s2.TrustWithdrawals()))
			}
			if len(base.Corrections()) != prior || len(base.TrustWithdrawals()) != 0 || len(base.st.log.all) != len(base.st.events) {
				t.Fatal("Apply changed its input snapshot's inventories")
			}
		})
	}
}
