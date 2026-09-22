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
	events, env, proof := outsideProofFixture()
	// A non-hour offset avoids Go's shared fixed-zone cache. Never mutate UTC,
	// Local, or a cached whole-hour location as part of this regression fixture.
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
	want := before.Invocations()[0].Start.StartedAt.Format(time.RFC3339Nano)
	returned := after.Invocations()[0]
	loc := returned.Start.StartedAt.Location()
	if loc == time.UTC || loc == time.Local {
		t.Fatal("fixture must use a private decoded fixed-offset location")
	}
	saved := *loc
	defer func() { *loc = saved }()
	*loc = *time.FixedZone("caller-replacement", 38*60)
	for name, s := range map[string]reduce.Snapshot{"earlier": before, "later": after} {
		got := s.Invocations()[0].Start.StartedAt.Format(time.RFC3339Nano)
		if got != want {
			t.Errorf("the %s snapshot's admitted timestamp changed from %s to %s after assigning through a returned Time.Location(). The copier skips time.Time's private location pointer, but its exported Location method makes that shared object mutable; no event authorized this change", name, want, got)
		}
	}
}
