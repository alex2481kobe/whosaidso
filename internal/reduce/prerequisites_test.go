package reduce

// Tests for claim-proof and decision-approved prerequisites, and for the
// reducer's own refusal of a start on a task that is not READY.

import (
	"testing"

	"whosaidso/internal/model"
)

func prereqTask(kind string, target model.RecordRef) *model.TaskCreate {
	return &model.TaskCreate{Provenance: provenance("agent-b"), ID: newID("TSKB"),
		Spec: taskSpec(withPrerequisite(kind, target, "forbid", nil))}
}

func wantPrereq(t *testing.T, l *ledgerBuilder, truth Truth, status TaskStatus) {
	t.Helper()
	p := projectTask(t, l, newID("TSKB"))
	if len(p.Prerequisites) != 1 || p.Prerequisites[0].Truth != truth || p.Status != status {
		t.Fatalf("prerequisites %+v status %s, want %s and %s", p.Prerequisites, p.Status, truth, status)
	}
	if truth != TruthTrue && p.Prerequisites[0].Detail == "" {
		t.Fatal("an unmet prerequisite carries no stated reason")
	}
}

func TestClaimProofNeedsCurrentSupport(t *testing.T) {
	claim := ref(newID("CMA1"), 1)
	disposal := func() model.TypedEvent {
		artifact := blobRef("result")
		return &model.ArtifactDispose{Artifact: artifact, Digest: artifact.Content.SHA256, PreviousLocation: "result",
			SupportLoss: []model.SupportLoss{}, Authority: rulingAuthority("owner")}
	}
	for _, tc := range []struct {
		name   string
		proven bool
		after  []model.TypedEvent
		truth  Truth
		status TaskStatus
	}{
		{"proven and supported", true, nil, TruthTrue, StatusReady},
		{"measured but not proven", false, nil, TruthFalse, StatusBlocked},
		{"proven then trust withdrawn", true, []model.TypedEvent{withdrawal()}, TruthFalse, StatusBlocked},
		{"proven then evidence disposed", true, []model.TypedEvent{disposal()}, TruthFalse, StatusBlocked},
		{"proven revision no longer current", true, []model.TypedEvent{&model.ClaimRevise{Target: claim,
			Replacement: claimSpec(), Provenance: provenance("agent-a")}}, TruthFalse, StatusBlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := proofLedger(t, tc.proven)
			l.add(t, prereqTask("claim-proof", claim))
			for _, e := range tc.after {
				l.add(t, e)
			}
			wantPrereq(t, l, tc.truth, tc.status)
		})
	}
}

func dispose(d model.RecordRef, disposition string) *model.DecisionDispose {
	return &model.DecisionDispose{Decision: d, Disposition: disposition, Quote: "exact ruling", Scope: testScope(), Authority: rulingAuthority("owner")}
}

func TestDecisionApprovedNeedsApprovalInForce(t *testing.T) {
	d := ref(newID("DCSA"), 1)
	other := ref(newID("DCSZ"), 1)
	for _, tc := range []struct {
		name  string
		after []model.TypedEvent
		truth Truth
	}{
		{"approved", []model.TypedEvent{dispose(d, "approved")}, TruthTrue},
		{"approval overturned then restored", []model.TypedEvent{dispose(d, "rejected"), dispose(d, "approved")}, TruthTrue},
		{"open", nil, TruthFalse},
		{"rejected", []model.TypedEvent{dispose(d, "rejected")}, TruthFalse},
		{"withdrawn", []model.TypedEvent{dispose(d, "withdrawn")}, TruthFalse},
		{"approved then rejected", []model.TypedEvent{dispose(d, "approved"), dispose(d, "rejected")}, TruthFalse},
		{"approved revision revised", []model.TypedEvent{dispose(d, "approved"),
			&model.DecisionRevise{Target: d, Replacement: decisionSpec(), Provenance: provenance("author")}}, TruthFalse},
		{"approved revision superseded", []model.TypedEvent{dispose(d, "approved"),
			&model.Supersede{Prior: d, Replacement: other, Reason: "question changed", Authority: ptrProof(rulingAuthority("owner"))}}, TruthFalse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLedger()
			l.add(t, &model.DecisionOpen{ID: d.RecordID, Provenance: provenance("author"), Spec: decisionSpec()},
				&model.DecisionOpen{ID: other.RecordID, Provenance: provenance("author"), Spec: decisionSpec()})
			l.add(t, prereqTask("decision-approved", d))
			for _, e := range tc.after {
				l.add(t, e)
			}
			status := StatusBlocked
			if tc.truth == TruthTrue {
				status = StatusReady
			}
			wantPrereq(t, l, tc.truth, status)
		})
	}
}

// TestReplayRefusesStartUnlessReady: the READY rule lives in the fold, so a
// ledger carrying a start on a BLOCKED task fails replay whatever wrote it.
func TestReplayRefusesStartUnlessReady(t *testing.T) {
	start := &model.TaskStart{Task: ref(newID("TSKB"), 1), Actor: model.Actor{ID: "agent-b"}, AttemptID: newID("ATTB")}
	d := ref(newID("DCSA"), 1)
	decision := func(after ...model.TypedEvent) *ledgerBuilder {
		l := newLedger()
		l.add(t, &model.DecisionOpen{ID: d.RecordID, Provenance: provenance("author"), Spec: decisionSpec()})
		l.add(t, prereqTask("decision-approved", d))
		for _, e := range after {
			l.add(t, e)
		}
		return l
	}
	hold := &model.BlockerHold{Task: ref(newID("TSKB"), 1), BlockerID: newID("HDB1"),
		Reason: model.BlockerResume, Actor: model.Actor{ID: "coordinator"}, Criterion: "resume is authorised"}

	// Controls: READY tasks start.
	for _, l := range []*ledgerBuilder{decision(dispose(d, "approved")), blockedOverReadyLedger(t)} {
		l.add(t, start)
		if p := projectTask(t, l, newID("TSKB")); p.Status != StatusInFlight {
			t.Fatalf("control: status %s, want IN FLIGHT", p.Status)
		}
	}
	for name, l := range map[string]*ledgerBuilder{
		"prerequisite FALSE": decision(dispose(d, "rejected")),
		"decision OPEN":      decision(),
		"open hold":          func() *ledgerBuilder { l := blockedOverReadyLedger(t); l.add(t, hold); return l }(),
	} {
		t.Run(name, func(t *testing.T) {
			l.add(t, start)
			_, err := Replay(l.bundles())
			wantFault(t, err, CodeInvalidTransition)
		})
	}
}
