package acceptance_test

import (
	"reflect"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/reduce"
)

func TestCopySnapshotNestedMapsAndPointersRemainDetached(t *testing.T) {
	before, first := outsideProofControl(t)
	after, err := reduce.Apply(before, laneEReduceBundle(t, first, laneEReduceCreate(2, laneEReduceSpec(1))))
	if err != nil {
		t.Fatalf("control independent snapshot fork must apply: %v", err)
	}
	want := before.Invocations()[0]
	if !reflect.DeepEqual(want, after.Invocations()[0]) {
		t.Fatal("control fork changed an unrelated invocation before any caller mutation")
	}
	got := after.Invocations()[0]
	got.Start.ConfigRequested["sample_count"] = laneEEvidenceNumber("999")
	*(*got.Seal.ConfigEffective.Value)["sample_count"].Value.Number = "888"
	(*got.Seal.OutputRefs.Value)[0].Content.Locators[0].Path = "caller-only"
	*got.Seal.Outcome.Value.ExitCode = 99
	for name, s := range map[string]reduce.Snapshot{"earlier": before, "later": after} {
		if !reflect.DeepEqual(want, s.Invocations()[0]) {
			t.Errorf("mutating nested map values, scalar pointers or artifact locator slices returned by the later snapshot changed the %s snapshot without an admitted event", name)
		}
	}
}

func TestCopySnapshotTimeLocationCannotRewriteAdmittedTimestamp(t *testing.T) {
	// SPEC CHANGE under ruling R8.4, not a test bent to fit code. This fixture
	// assumed a private non-UTC location survived admission, so assigning
	// through a returned Time.Location() rewrote the admitted timestamp in both
	// snapshots. R8.4 makes that premise false by design: the wire carries only
	// UTC, EncodeEvent writes the instant as UTC, and decoding refuses any other
	// offset. The test now asserts the invariant that closes the hole instead.
	events, env, proof := outsideProofFixture()
	// A non-hour offset avoids Go's shared fixed-zone cache.
	env.StartedAt = time.Date(2026, 9, 22, 12, 0, 0, 0, time.FixedZone("fixture", 37*60))
	first := laneEReduceBundle(t, model.Bundle{}, append(events, &model.InvocationStart{Envelope: env}, outsideProofSeal(env), proof)...)
	before := laneEReduceReplay(t, first)
	p, ok := before.ClaimAt(proof.Claim)
	if !ok || p.Status != reduce.StatusProven {
		t.Fatalf("control non-hour timestamp must establish PROVEN before testing isolation: %+v", p)
	}
	after, err := reduce.Apply(before, laneEReduceBundle(t, first, laneEReduceCreate(2, laneEReduceSpec(1))))
	if err != nil {
		t.Fatalf("control independent fork must apply: %v", err)
	}
	for name, s := range map[string]reduce.Snapshot{"earlier": before, "later": after} {
		got := s.Invocations()[0].Start.StartedAt
		if !got.Equal(env.StartedAt) || got.Location() != time.UTC {
			t.Errorf("the %s snapshot's admitted timestamp is %s (location %q). A non-UTC timestamp admitted through the real path must come back as the same instant in UTC, so no private location is ever shared between snapshots", name, got.Format(time.RFC3339Nano), got.Location())
		}
	}
}
