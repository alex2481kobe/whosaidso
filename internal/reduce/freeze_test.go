package reduce

// Tests for criterion freezing on every invocation.start (freeze.go), each
// through both Replay and Apply: criterion bundle recorded_at < started_at <=
// captured_at, with the criterion in a prior bundle and the capture time read
// from the review that attributes the start. None of these runs is ever
// offered as proof.

import (
	"testing"
	"time"

	"datum/internal/model"
)

// freezeLedger fixes the criterion in its own bundle and returns the time that
// bundle was recorded.
func freezeLedger(t *testing.T) (*ledgerBuilder, model.RecordRef, time.Time) {
	t.Helper()
	l := goodLedger(t)
	claim := ref(newID("CMA1"), 1)
	fixed := l.add(t, &model.InstrumentDeclare{ID: newID("HNSS"), Provenance: provenance("lane-a"), Spec: proofInstrument()},
		&model.ClaimAssert{ID: claim.RecordID, Provenance: provenance("lane-a"), Spec: claimSpec()}, fixProofCriterion(claim))
	return l, claim, fixed.RecordedAt
}

// startReview is admission's review of one packet carrying n events, authored
// by the criterion's author: it attributes them and records captured when
// non-nil, else records the capture time as unknown.
func startReview(n int, captured *time.Time) *model.ReviewAdmit {
	packet := newID("PKTF")
	review := &model.ReviewAdmit{Packets: []model.PacketRef{{CommandID: packet, Digest: newDigest("freeze")}}, Outcome: "accepted",
		Actor: model.Actor{ID: "coordinator"}, Reason: "freeze fixture", Authors: map[model.ID]model.Actor{packet: {ID: "lane-a"}}, EventPackets: []model.ID{}}
	for i := 0; i < n; i++ {
		review.EventPackets = append(review.EventPackets, packet)
	}
	review.CapturedAt = map[model.ID]model.Availability[time.Time]{packet: {State: model.Unknown, Reason: "the fixture recorded no capture time"}}
	if captured != nil {
		review.CapturedAt[packet] = knownAt(captured.UTC())
	}
	return review
}

func TestStartFrozenBetweenCriterionAndCapture(t *testing.T) {
	for _, tc := range []struct {
		name     string
		start    func(fixed time.Time) time.Time // started_at from the criterion bundle's time
		captured func(start time.Time) time.Time
		code     string
	}{
		// Controls: a start strictly after the criterion's bundle, captured
		// later or at the same instant, admits.
		{"captured later", func(f time.Time) time.Time { return f.Add(time.Hour) }, func(s time.Time) time.Time { return s.Add(time.Minute) }, ""},
		{"captured at the start instant", func(f time.Time) time.Time { return f.Add(time.Hour) }, func(s time.Time) time.Time { return s }, ""},
		{"start after capture", func(f time.Time) time.Time { return f.Add(time.Hour) }, func(s time.Time) time.Time { return s.Add(-time.Nanosecond) }, CodeStartAfterCapture},
		{"start at the criterion's bundle time", func(f time.Time) time.Time { return f }, func(s time.Time) time.Time { return s.Add(time.Minute) }, CodeCriterionNotFrozen},
		{"start before the criterion's bundle", func(f time.Time) time.Time { return f.Add(-time.Second) }, func(s time.Time) time.Time { return s.Add(time.Minute) }, CodeCriterionNotFrozen},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, claim, fixed := freezeLedger(t)
			env := proofEnvelope(claim, newID("RNA"))
			env.StartedAt = tc.start(fixed)
			captured := tc.captured(env.StartedAt)
			l.add(t, &model.InvocationStart{Envelope: env}, startReview(1, &captured))
			wantBoth(t, l, tc.code)
		})
	}
}

// A start that names no criterion is still bounded by its capture.
func TestStartWithoutCriterionIsStillBoundedByCapture(t *testing.T) {
	for _, late := range []bool{false, true} {
		l, claim, _ := freezeLedger(t)
		env := proofEnvelope(claim, newID("RNA"))
		env.CriterionRef = proofUnknown[model.CriterionRef]()
		captured := env.StartedAt
		code := ""
		if late {
			env.StartedAt = captured.Add(time.Second)
			code = CodeStartAfterCapture
		}
		l.add(t, &model.InvocationStart{Envelope: env}, startReview(1, &captured))
		wantBoth(t, l, code)
	}
}

// A capture time that is unrecorded (no review) or recorded unknown never
// becomes a default timestamp: not the bundle's recorded_at, not the start's
// own claim.
func TestStartWithoutCaptureAttributionIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name   string
		review *model.ReviewAdmit
	}{
		{"no review", nil},
		{"attributed, capture time recorded unknown", startReview(1, nil)},
	} {
		for _, withCriterion := range []bool{true, false} {
			l, claim, _ := freezeLedger(t)
			env := proofEnvelope(claim, newID("RNA"))
			if !withCriterion {
				env.CriterionRef = proofUnknown[model.CriterionRef]()
			}
			events := []model.TypedEvent{&model.InvocationStart{Envelope: env}}
			if tc.review != nil {
				events = append(events, tc.review)
			}
			l.bare = true
			l.add(t, events...)
			t.Run(tc.name, func(t *testing.T) { wantBoth(t, l, CodeStartUncaptured) })
		}
	}
}

// A criterion fixed in the start's own bundle is not frozen, even when that
// bundle's recorded_at is before the start, and even when it is admission's
// synthetic validation time.
func TestCriterionInTheSameBundleIsNotFrozen(t *testing.T) {
	for _, synthetic := range []bool{false, true} {
		l := goodLedger(t)
		claim := ref(newID("CMA1"), 1)
		l.add(t, &model.InstrumentDeclare{ID: newID("HNSS"), Provenance: provenance("lane-a"), Spec: proofInstrument()},
			&model.ClaimAssert{ID: claim.RecordID, Provenance: provenance("lane-a"), Spec: claimSpec()})
		env := proofEnvelope(claim, newID("RNA"))
		captured := env.StartedAt.Add(time.Minute)
		b := l.add(t, fixProofCriterion(claim), &model.InvocationStart{Envelope: env}, startReview(2, &captured))
		if !b.RecordedAt.Before(env.StartedAt) {
			t.Fatal("fixture: the bundle must be recorded before the start, so only the prior-bundle rule refuses it")
		}
		if synthetic {
			l.out[len(l.out)-1].RecordedAt = time.Unix(0, 0).UTC()
		}
		wantBoth(t, l, CodeCriterionNotFrozen)
	}
}
