package reduce

// Copy costs of detaching a timestamp's location live here. UTC through
// durable ledger publication is time_publication_test.go's.

import (
	"testing"
	"time"
)

// A test-only candidate measures the cost of a detached Location struct. Its
// private tables remain shared, but ordinary Go cannot overwrite those tables.
// This deliberately does not change production copy semantics.
func detachedTime(stamp time.Time) time.Time {
	location := *stamp.Location()
	return stamp.In(&location)
}

func detachInvocationTimes(inv Invocation) Invocation {
	inv.Start.StartedAt = detachedTime(inv.Start.StartedAt)
	if inv.Start.ObservedAt.Value != nil {
		*inv.Start.ObservedAt.Value = detachedTime(*inv.Start.ObservedAt.Value)
	}
	if inv.Seal != nil {
		inv.Seal.StartedAt = detachedTime(inv.Seal.StartedAt)
		if inv.Seal.ObservedAt.Value != nil {
			*inv.Seal.ObservedAt.Value = detachedTime(*inv.Seal.ObservedAt.Value)
		}
	}
	return inv
}

var timeCopySink Invocation

func timeCopySnapshot() Snapshot {
	env := proofEnvelope(ref(newID("CMA1"), 1), newID("RNA"))
	env.StartedAt = env.StartedAt.In(time.FixedZone("cost-fixture", 37*60))
	inv := Invocation{Start: env, Seal: &sealProof(env, 0).Envelope}
	st := newState()
	st.invocations[InvocationKey{Project: testProject, InvocationID: env.InvocationID}] = inv
	return Snapshot{st: st}
}

func TestDetachedLocationCandidateCost(t *testing.T) {
	utc := baseTime.UTC()
	copied := detachedTime(utc)
	if copied.Location() == time.UTC || copied == utc || !copied.Equal(utc) {
		t.Fatal("detached UTC must lose pointer/struct equality but retain instant equality")
	}
	snapshot := timeCopySnapshot()
	current := testing.AllocsPerRun(100, func() { timeCopySink = snapshot.Invocations()[0] })
	candidate := testing.AllocsPerRun(100, func() {
		timeCopySink = detachInvocationTimes(snapshot.Invocations()[0])
	})
	t.Logf("one sealed invocation, three timestamps: current=%.0f candidate=%.0f delta=%.0f allocations/read",
		current, candidate, candidate-current)
	if candidate-current != 3 {
		t.Fatalf("remeasure candidate cost: expected one extra allocation per timestamp, got %.0f", candidate-current)
	}
	original := snapshot.Invocations()[0].Start.StartedAt.Format(time.RFC3339Nano)
	*timeCopySink.Start.StartedAt.Location() = *time.FixedZone("candidate-mutation", 38*60)
	if snapshot.Invocations()[0].Start.StartedAt.Format(time.RFC3339Nano) != original {
		t.Fatal("candidate failed to detach the returned location")
	}
}

func BenchmarkInvocationLocationCopy(b *testing.B) {
	snapshot := timeCopySnapshot()
	b.Run("current", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			timeCopySink = snapshot.Invocations()[0]
		}
	})
	b.Run("detached-location-candidate", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			timeCopySink = detachInvocationTimes(snapshot.Invocations()[0])
		}
	})
}
