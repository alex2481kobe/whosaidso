package acceptance_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/reduce"
)

func TestCopySnapshotNestedMapsAndPointersRemainDetached(t *testing.T) {
	before, prefix := outsideProofControl(t)
	first := prefix[len(prefix)-1]
	after, err := reduce.Apply(before, reduceBundle(t, first, reduceCreate(2, reduceSpec(1))))
	if err != nil {
		t.Fatalf("control independent snapshot fork must apply: %v", err)
	}
	want := before.Invocations()[0]
	if !reflect.DeepEqual(want, after.Invocations()[0]) {
		t.Fatal("control fork changed an unrelated invocation before any caller mutation")
	}
	got := after.Invocations()[0]
	got.Start.ConfigRequested["sample_count"] = evidenceNumber("999")
	*(*got.Seal.ConfigEffective.Value)["sample_count"].Value.Number = "888"
	(*got.Seal.Outputs.Value)[0].Name = "caller-only"
	*got.Seal.Outcome.Value.ExitCode = 99
	for name, s := range map[string]reduce.Snapshot{"earlier": before, "later": after} {
		if !reflect.DeepEqual(want, s.Invocations()[0]) {
			t.Errorf("mutating nested map values, scalar pointers or output slices returned by the later snapshot changed the %s snapshot without an admitted event", name)
		}
	}
}

func TestCopySnapshotTimeLocationCannotRewriteAdmittedTimestamp(t *testing.T) {
	// SPEC CHANGE, not a test bent to fit code. This fixture
	// assumed a private non-UTC location survived admission, so assigning
	// through a returned Time.Location() rewrote the admitted timestamp in both
	// snapshots. That premise is false by design: the wire carries only
	// UTC, EncodeEvent writes the instant as UTC, and decoding refuses any other
	// offset. The test now asserts the invariant that closes the hole instead.
	events, env, proof := outsideProofFixture()
	// A non-hour offset avoids Go's shared fixed-zone cache. The instant is
	// the fixture's 12:01:10 UTC, after the criterion's bundle.
	env.StartedAt = time.Date(2026, 9, 22, 12, 38, 10, 0, time.FixedZone("fixture", 37*60))
	setup := outsideProofSetup(t, events, true)
	first := outsideProofRun(t, setup, env, outsideProofEncode(t, proof))
	before := reduceReplay(t, setup, first)
	p, ok := before.ClaimAt(proof.Claim)
	if !ok || p.Status != reduce.StatusProven {
		t.Fatalf("control non-hour timestamp must establish PROVEN before testing isolation: %+v", p)
	}
	after, err := reduce.Apply(before, reduceBundle(t, first, reduceCreate(2, reduceSpec(1))))
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
