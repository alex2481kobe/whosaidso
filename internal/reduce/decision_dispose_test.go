package reduce

// Replay-time refusal of a decision disposition with no named authority lives
// here. Gate admission of decision.dispose (quote, authority, packet author) is
// tested in internal/write; decision projections live in proof_test.go.

import (
	"testing"

	"datum/internal/model"
)

func TestR10DispositionNeedsANamedAuthorityOnReplay(t *testing.T) {
	l := proofLedger(t, true)
	d := ref(newID("DCSA"), 1)
	l.add(t, &model.DecisionOpen{ID: d.RecordID, Provenance: provenance("author"), Spec: decisionSpec()})
	before := mustReplay(t, l.out)
	unnamed := &model.DecisionDispose{Decision: d, Disposition: "approved", Quote: "approved", Scope: testScope(), Authority: rulingAuthority("owner")}
	unnamed.Authority.Actor = model.Actor{UnknownReason: "nobody recorded who ruled"}
	_, err := Apply(before, l.add(t, unnamed))
	if f := wantFault(t, err, CodeInvalidTransition); f.Path != "authority" {
		t.Fatalf("unnamed authority refused at %s, want authority", f.Path)
	}
}
