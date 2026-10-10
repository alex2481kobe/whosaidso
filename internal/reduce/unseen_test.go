package reduce

// Tests for the unseen-fact rule (setaside.go): a bundle that did not see
// everything before it is set aside when an event names, by identity, a fact
// published on a branch it never saw. Each case is the shared-intake one: the
// same packet admitted on both branches, so the second copy is set aside and
// the next bundle on its branch names a fact that now exists only as the
// other branch published it.

import (
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestAnEventNamingAFactFromAnUnseenBranchIsSetAside(t *testing.T) {
	claim := ref(newID("CMA1"), 1)
	proofBase := func(t *testing.T) *ledgerBuilder {
		l := goodLedger(t)
		l.add(t, &model.InstrumentDeclare{ID: newID("HNSS"), Provenance: provenance("agent-a"), Spec: proofInstrument()},
			&model.ClaimAssert{ID: claim.RecordID, Provenance: provenance("agent-a"), Spec: claimSpec()})
		return l
	}
	run := proofEnvelope(claim, newID("RNA"))
	cases := []struct {
		name   string
		base   func(*testing.T) *ledgerBuilder
		shared model.TypedEvent // admitted on both branches
		next   model.TypedEvent // names what shared published
	}{
		{"an attempt", func(t *testing.T) *ledgerBuilder {
			l := goodLedger(t)
			l.add(t, createTask("TSKS"))
			return l
		}, &model.TaskStart{Task: ref(newID("TSKS"), 1), Actor: model.Actor{ID: "agent-a"}, AttemptID: newID("ATTS")},
			&model.AttemptTerminal{Task: ref(newID("TSKS"), 1), AttemptID: newID("ATTS"), Outcome: model.AttemptSuccess,
				Reason: "done", NextAction: "accept it", DeliveryRefs: []model.ArtifactRef{}}},
		{"a hold", goodLedger, &model.BlockerHold{Task: ref(newID("TSKA"), 1), BlockerID: newID("HDX1"),
			Reason: model.BlockerAwaitingAcceptance, Actor: model.Actor{ID: "owner"}, Criterion: "owner reads it"},
			clearAt(newID("TSKA"), 1, newID("HDX1"))},
		{"a criterion", proofBase, fixProofCriterion(claim), &model.InvocationStart{Envelope: run}},
		{"an invocation", func(t *testing.T) *ledgerBuilder {
			l := proofBase(t)
			l.add(t, fixProofCriterion(claim))
			l.add(t, &model.InvocationStart{Envelope: run})
			return l
		}, sealProof(run, 0), admitProof(claim, run.InvocationID)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := tc.base(t)
			a, b := base.branch(100), base.branch(200)
			a.add(t, tc.shared)
			b.add(t, tc.shared)
			named := b.add(t, tc.next)
			if s := mustReplay(t, b.out); len(s.SetAside()) != 0 {
				t.Fatal("control: on its own branch every bundle applies")
			}
			got := mustReplay(t, merged(a, b)).SetAside()
			if len(got) != 2 || got[1].CommandID != named.CommandID || got[1].Code != CodeUnseenFact {
				t.Fatalf("the bundle naming the other branch's copy must be set aside as unseen: %+v", got)
			}
		})
	}
}
