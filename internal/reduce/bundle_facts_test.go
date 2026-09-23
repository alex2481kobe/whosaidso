package reduce

// Tests for the candidate bundle's inventory: it is read once, it retains the
// criterion's bundle recording time, and indexing the whole bundle never lets
// an event name something a later event in the same bundle establishes.
// Proof family rules that read the inventory are tested in proof_family_test.go.

import (
	"testing"

	"datum/internal/model"
)

func TestCriterionRetainsItsBundleRecordingTime(t *testing.T) {
	l := proofLedger(t, false)
	claim := ref(newID("CMA1"), 1)
	s := mustReplay(t, l.out)
	c, ok := s.Criterion(proofCriterion(claim))
	if !ok {
		t.Fatal("criterion missing")
	}
	want := l.out[c.Origin.Sequence-1].RecordedAt
	if !c.RecordedAt.Equal(want) || c.RecordedAt.IsZero() {
		t.Fatalf("criterion recorded at %v, its bundle at %v", c.RecordedAt, want)
	}
	// The same through Apply onto the prefix before the fixing bundle.
	prefix := mustReplay(t, l.out[:c.Origin.Sequence-1])
	applied, err := Apply(prefix, l.out[c.Origin.Sequence-1])
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := applied.Criterion(proofCriterion(claim)); !got.RecordedAt.Equal(want) {
		t.Fatalf("Apply recorded %v, want %v", got.RecordedAt, want)
	}
}

// sameBundleLedger puts the run, and optionally its proof, in one bundle in the
// given order.
func sameBundleLedger(t *testing.T, order string) (*ledgerBuilder, Snapshot, model.RecordRef) {
	t.Helper()
	l := goodLedger(t)
	claim := ref(newID("CMA1"), 1)
	l.add(t, &model.InstrumentDeclare{ID: newID("HNSS"), Provenance: provenance("lane-a"), Spec: proofInstrument()},
		&model.ClaimAssert{ID: claim.RecordID, Provenance: provenance("lane-a"), Spec: claimSpec()}, fixProofCriterion(claim))
	before := mustReplay(t, l.out)
	env := proofEnvelope(claim, newID("RNA"))
	events := map[byte]model.TypedEvent{'s': &model.InvocationStart{Envelope: env}, 'e': sealProof(env, 0), 'p': admitProof(claim, env.InvocationID)}
	var typed []model.TypedEvent
	for i := range order {
		typed = append(typed, events[order[i]])
	}
	l.add(t, typed...)
	return l, before, claim
}

func TestBundleInventoryAuthorizesNoForwardReference(t *testing.T) {
	// Controls: in bundle order, a run and its proof admit together.
	for _, order := range []string{"se", "sep"} {
		l, before, _ := sameBundleLedger(t, order)
		mustReplay(t, l.out)
		if _, err := Apply(before, l.out[len(l.out)-1]); err != nil {
			t.Fatalf("%s: control refused through Apply: %v", order, err)
		}
	}
	l, _, claim := sameBundleLedger(t, "sep")
	wantClaim(t, mustReplay(t, l.out), claim, StatusProven)
	// A seal before its start, or a proof before its member, names a later
	// event of the same bundle: still an unknown reference.
	for _, order := range []string{"es", "pse"} {
		l, before, _ := sameBundleLedger(t, order)
		_, err := Replay(l.out)
		wantFault(t, err, CodeUnknownReference)
		_, err = Apply(before, l.out[len(l.out)-1])
		wantFault(t, err, CodeUnknownReference)
	}
}
