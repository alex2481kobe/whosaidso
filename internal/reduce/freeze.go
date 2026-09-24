package reduce

// Criterion freezing for every invocation.start lives here: the criterion a
// run names was fixed in a prior bundle recorded before the run started, and
// the run did not start after intake captured it, so
// criterion bundle recorded_at < started_at <= captured_at. Every start is
// held to it, not only starts a proof later lists. The capture time is read
// from the review that attributes the start to its packet; an unattributed or
// uncaptured start is refused, never given a default time. Proof family rules
// do not live here.

import (
	"fmt"
	"time"

	"whosaidso/internal/model"
)

// Freezing refusal codes. Admission reports these same codes because it
// reaches them through Apply.
const (
	// CodeStartUncaptured is a start the bundle does not attribute to a
	// packet with a recorded capture time.
	CodeStartUncaptured = "start-uncaptured"
	// CodeStartAfterCapture is a start dated after intake captured it.
	CodeStartAfterCapture = "start-after-capture"
	// CodeCriterionNotFrozen is a start naming a criterion that was not fixed
	// in a prior bundle recorded before the run started.
	CodeCriterionNotFrozen = "criterion-not-frozen"
)

// checkStartFrozen: started_at is the author's claim, so it is bounded above
// by the capture time intake stamped and review.admit recorded, and below by
// the criterion's bundle time. Only prior bundles' recorded_at are
// observations: the candidate bundle's own time is synthetic during
// admission's validation replay, so a criterion fixed in this bundle is never
// frozen, whatever time this bundle carries.
func (s *state) checkStartFrozen(b model.Bundle, idx int, env model.InvocationEnvelope) error {
	captured, ok := s.bundle.capturedAt(idx)
	if !ok {
		return faultAt(CodeStartUncaptured, b.Sequence, idx, "envelope.started_at",
			"no review in this bundle attributes the start to a packet with a recorded capture time")
	}
	if env.StartedAt.After(captured) {
		return faultAt(CodeStartAfterCapture, b.Sequence, idx, "envelope.started_at",
			fmt.Sprintf("the run claims to start at %s, after it was captured at %s", stamp(env.StartedAt), stamp(captured)))
	}
	if env.CriterionRef.State != model.Known || env.CriterionRef.Value == nil {
		return nil
	}
	criterion, ok := s.criteria[criterionKey(*env.CriterionRef.Value)]
	if !ok || env.CriterionRef.Value.Claim.Project != b.Project {
		return faultAt(CodeCriterionNotFrozen, b.Sequence, idx, "envelope.criterion_ref", "the criterion is not fixed in this ledger")
	}
	if criterion.Origin.Sequence >= b.Sequence {
		return faultAt(CodeCriterionNotFrozen, b.Sequence, idx, "envelope.criterion_ref", "the criterion is fixed in this same bundle, not a prior one")
	}
	if !criterion.RecordedAt.Before(env.StartedAt) {
		return faultAt(CodeCriterionNotFrozen, b.Sequence, idx, "envelope.criterion_ref",
			fmt.Sprintf("criterion recorded at %s, not before the run started at %s", stamp(criterion.RecordedAt), stamp(env.StartedAt)))
	}
	return nil
}

// capturedAt is the capture time of the packet that carried event idx, when
// this bundle's accepted review attributes it and records that time.
func (f *bundleFacts) capturedAt(idx int) (time.Time, bool) {
	if f == nil {
		return time.Time{}, false
	}
	packet, ok := f.packetOf[idx]
	if !ok {
		return time.Time{}, false
	}
	at, ok := f.captured[packet]
	return at, ok
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.999999999Z07:00") }
