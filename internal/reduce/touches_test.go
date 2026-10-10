package reduce

// Tests for Touches (about.go): which events a record's history lists from
// a set-aside bundle, where no snapshot index can say.

import (
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestTouchesEveryWayAnEventIsAboutAnID(t *testing.T) {
	claim := ref(newID("CMA1"), 1)
	cases := []struct {
		name  string
		event model.TypedEvent
		id    string
	}{
		{"creates a task", createTask("TSKX"), "TSKX"},
		{"creates a claim", &model.ClaimAssert{ID: newID("CMX1"), Provenance: provenance("agent-a"), Spec: claimSpec()}, "CMX1"},
		{"opens a decision", &model.DecisionOpen{ID: newID("DCX1"), Provenance: provenance("agent-a"), Spec: decisionSpec()}, "DCX1"},
		{"declares an instrument", &model.InstrumentDeclare{ID: newID("HNSX"), Provenance: provenance("agent-a"), Spec: proofInstrument()}, "HNSX"},
		{"takes in a source", &model.SourceIntake{SourceID: newID("SRCX"), OriginalDigest: newDigest("said"), Length: 4,
			Speaker: model.Actor{ID: "owner"}, SourceRef: blobRef("said"), Referents: []model.RecordRef{}}, "SRCX"},
		{"revises it", amendTask("TSKA", 1, "changed"), "TSKA"},
		{"names it", &model.TaskStart{Task: ref(newID("TSKA"), 1), Actor: model.Actor{ID: "agent-a"}, AttemptID: newID("ATTX")}, "TSKA"},
		{"fixes a criterion of it", fixProofCriterion(claim), "CMA1"},
		{"starts a run under a criterion of it", &model.InvocationStart{Envelope: proofEnvelope(claim, newID("RNX"))}, "CMA1"},
		{"clears a hold on it", clearAt(newID("TSKA"), 1, newID("HDX1")), "TSKA"},
	}
	s := mustReplay(t, goodLedger(t).out)
	about := func(id string) Ident { return Ident{Project: testProject, ID: newID(id)} }
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !s.Touches(tc.event, about(tc.id)) {
				t.Fatalf("an event that %s must touch %s", tc.name, tc.id)
			}
			if s.Touches(tc.event, about("ZZZZ")) {
				t.Fatalf("an event that %s touches nothing else", tc.name)
			}
		})
	}
}
