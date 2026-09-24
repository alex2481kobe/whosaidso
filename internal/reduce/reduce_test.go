package reduce

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"whosaidso/internal/model"
)

// ---- fixture construction -------------------------------------------------
//
// Fixtures are built through model.EncodeEvent, which is the same strict
// boundary untrusted bytes cross. A fixture that the wire layer would refuse is
// not a fixture, it is a test passing on input the system can never see.

const testProject = model.ProjectID("datum/reduce-fixtures")

var baseTime = time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)

// newID pads a readable tag into a ULID body. It panics rather than returning
// an error because a malformed fixture id is a broken test, not a test case.
// Crockford has no I, L, O or U, so tags avoid those letters.
func newID(tag string) model.ID {
	s := strings.ToUpper(tag)
	if len(s) > 26 {
		panic("id tag too long: " + tag)
	}
	out := model.ID(strings.Repeat("0", 26-len(s)) + s)
	if !model.ValidID(out) {
		panic("id tag is not ULID material: " + tag)
	}
	return out
}

func newDigest(s string) model.Digest { return model.HashBytes([]byte(s)) }

func blobRef(name string) model.ArtifactRef {
	return model.ArtifactRef{
		Kind: "content",
		Content: &model.ContentPin{
			SHA256:    newDigest(name),
			Length:    uint64(len(name)),
			MediaType: "text/plain",
			Locators:  []model.Locator{{Path: ".whosaidso/artifacts/" + name}},
		},
		Selector: model.Selector{Kind: "whole"},
	}
}

func testScope() model.Scope {
	return model.Scope{
		SourcePaths: []string{"internal/reduce/reduce.go"},
		ContextRefs: []model.RecordRef{},
		AppliesWhen: "while the reducer fixtures run",
		Limitations: "fixture data, not a measurement",
	}
}

// closeAuthority is the authority a pre-R15.1 closure cited; it stays optional.
func closeAuthority() *model.Authority {
	a := rulingAuthority("owner")
	return &a
}

func rulingAuthority(actor string) model.Authority {
	return model.Authority{
		Actor:     model.Actor{ID: actor},
		SourceRef: blobRef("owner-ruling"),
		Selector:  model.Selector{Kind: "json-pointer", Pointer: "/rulings/0"},
		Scope:     testScope(),
	}
}

func provenance(actor string) model.Provenance {
	return model.Provenance{
		Author:     model.Actor{ID: actor},
		SourceRefs: []model.ArtifactRef{blobRef("intake-" + actor)},
	}
}

type taskOpt func(*model.TaskSpec)

func withPrerequisite(kind string, target model.RecordRef, policy string, auth *model.Authority) taskOpt {
	return func(s *model.TaskSpec) {
		s.Prerequisites = append(s.Prerequisites, model.Prerequisite{
			Kind:         kind,
			Target:       target,
			WaiverPolicy: policy,
			Authority:    auth,
		})
	}
}

func withCriteria(cs ...model.AcceptanceCriterion) taskOpt {
	return func(s *model.TaskSpec) { s.AcceptanceCriteria = cs }
}

func withNextActor(a model.Actor) taskOpt {
	return func(s *model.TaskSpec) { s.NextActor = a }
}

func withIntent(text string) taskOpt {
	return func(s *model.TaskSpec) { s.Intent = text }
}

func taskSpec(opts ...taskOpt) model.TaskSpec {
	s := model.TaskSpec{
		Intent:   "fold task events into the four TASK statuses",
		Subject:  "internal/reduce",
		Scope:    testScope(),
		NonGoals: []string{"no filesystem access inside replay"},
		AcceptanceCriteria: []model.AcceptanceCriterion{
			{ID: newID("ACCA"), Revision: 1, Criterion: "all four statuses are fixture proven"},
		},
		ContextRefs:    []model.RecordRef{},
		ConstraintRefs: []model.RecordRef{},
		Prerequisites:  []model.Prerequisite{},
		NextActor:      model.Actor{ID: "coordinator"},
	}
	for _, o := range opts {
		o(&s)
	}
	return s
}

func claimSpec() model.ClaimSpec {
	return model.ClaimSpec{
		Assertion:    "replaying the same log twice gives identical state",
		Falsifier:    "two replays disagree on any task status",
		Scope:        testScope(),
		ExternalRefs: []model.ExternalReference{},
	}
}

func decisionSpec() model.DecisionSpec {
	return model.DecisionSpec{
		Question:     "does BLOCKED win over READY",
		Options:      []string{"yes", "no"},
		WaitingActor: model.Actor{ID: "owner"},
		Scope:        testScope(),
	}
}

func ref(id model.ID, rev model.Revision) model.RecordRef {
	return model.RecordRef{Project: testProject, RecordID: id, Revision: rev}
}

// ledgerBuilder chains sequence and predecessor the way the store does, so a
// fixture cannot accidentally test the reducer against an impossible ledger.
type ledgerBuilder struct {
	project model.ProjectID
	seq     uint64
	prev    model.ID
	out     []model.Bundle
	// bare skips attributeFixture, for fixtures that test missing attribution.
	bare bool
	// holders are the attempt actors seen so far, so a receipt's default
	// packet author is its attempt's holder.
	holders map[model.ID]model.Actor
}

func newLedger() *ledgerBuilder { return &ledgerBuilder{project: testProject} }

func (l *ledgerBuilder) add(t *testing.T, events ...model.TypedEvent) model.Bundle {
	t.Helper()
	l.seq++
	packet := model.PacketRef{CommandID: newID(fmt.Sprintf("PKT%d", l.seq)), Digest: newDigest(fmt.Sprintf("packet-%d", l.seq))}
	if l.holders == nil {
		l.holders = map[model.ID]model.Actor{}
	}
	for _, e := range events {
		switch e := e.(type) {
		case *model.TaskStart:
			l.holders[e.AttemptID] = e.Actor
		case *model.TaskTakeover:
			l.holders[e.AttemptID] = e.Actor
		}
	}
	if !l.bare {
		events = attributeFixtureWith(l.seq, events, l.holders)
	}
	raw := make([]model.Event, 0, len(events))
	for _, e := range events {
		enc, err := model.EncodeEvent(e)
		if err != nil {
			t.Fatalf("fixture event %s does not encode: %v", e.EventType(), err)
		}
		raw = append(raw, enc)
	}
	b := model.Bundle{
		Version:       model.WireVersion,
		Project:       l.project,
		Sequence:      l.seq,
		CommandID:     newID(fmt.Sprintf("CMD%d", l.seq)),
		Predecessor:   l.prev,
		RequestDigest: newDigest(fmt.Sprintf("request-%d", l.seq)),
		Admitter:      model.Actor{ID: "coordinator"},
		RecordedAt:    baseTime.Add(time.Duration(l.seq) * time.Minute),
		Packets:       []model.PacketRef{packet},
		Events:        raw,
	}
	l.prev = b.CommandID
	l.out = append(l.out, b)
	return b
}

func (l *ledgerBuilder) bundles() []model.Bundle { return l.out }

// attributeFixture gives a bundle that carries a start, criterion fix,
// proof, takeover or receipt and no review the attribution admission writes:
// an accepted review with one packet per event, authored by the actor that
// event names (the criterion author, the proof judgment, the takeover actor,
// the receipt's attempt holder, otherwise "coordinator"), each captured a
// minute after the latest start. Tests of the attribution rules themselves
// build their own reviews and so bypass this default.
func attributeFixture(seq uint64, events []model.TypedEvent) []model.TypedEvent {
	return attributeFixtureWith(seq, events, nil)
}

func attributeFixtureWith(seq uint64, events []model.TypedEvent, holders map[model.ID]model.Actor) []model.TypedEvent {
	var latest time.Time
	needed := false
	for _, e := range events {
		switch e := e.(type) {
		case *model.ReviewAdmit:
			return events
		case *model.InvocationStart:
			needed = true
			if e.Envelope.StartedAt.After(latest) {
				latest = e.Envelope.StartedAt
			}
		case *model.CriterionFix, *model.ProofAdmit, *model.TaskTakeover, *model.AttemptTerminal:
			needed = true
		}
	}
	if !needed {
		return events
	}
	review := &model.ReviewAdmit{Outcome: "accepted", Actor: model.Actor{ID: "coordinator"}, Reason: "fixture admission",
		Authors: map[model.ID]model.Actor{}, CapturedAt: map[model.ID]model.Availability[time.Time]{}}
	for i, e := range events {
		packet := newID(fmt.Sprintf("PKT%dE%d", seq, i))
		author := model.Actor{ID: "coordinator"}
		switch e := e.(type) {
		case *model.CriterionFix:
			author = e.Author
		case *model.ProofAdmit:
			author = e.Judgment.Actor
		case *model.TaskTakeover:
			author = e.Actor
		case *model.AttemptTerminal:
			if holder, ok := holders[e.AttemptID]; ok {
				author = holder
			}
		}
		review.Packets = append(review.Packets, model.PacketRef{CommandID: packet, Digest: newDigest(string(packet))})
		review.EventPackets = append(review.EventPackets, packet)
		review.Authors[packet] = author
		review.CapturedAt[packet] = knownAt(latest.Add(time.Minute))
	}
	return append(append([]model.TypedEvent{}, events...), review)
}

// knownAt is a recorded capture time.
func knownAt(at time.Time) model.Availability[time.Time] {
	return model.Availability[time.Time]{State: model.Known, Value: &at}
}

// ---- assertions -----------------------------------------------------------

func wantFault(t *testing.T, err error, code string) *model.Fault {
	t.Helper()
	if err == nil {
		t.Fatalf("expected fault %q, got no error", code)
	}
	var f *model.Fault
	if !errors.As(err, &f) {
		t.Fatalf("expected *model.Fault %q, got %T: %v", code, err, err)
	}
	if f.Code != code {
		t.Fatalf("expected fault %q, got %q: %v", code, f.Code, err)
	}
	return f
}

func wantConflict(t *testing.T, err error) *Conflict {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a revision conflict, got no error")
	}
	var c *Conflict
	if !errors.As(err, &c) {
		t.Fatalf("expected *Conflict, got %T: %v", err, err)
	}
	return c
}

func mustReplay(t *testing.T, bundles []model.Bundle) Snapshot {
	t.Helper()
	s, err := Replay(bundles)
	if err != nil {
		t.Fatalf("good control failed to replay: %v", err)
	}
	return s
}

// ---- the good control -----------------------------------------------------

// goodLedger is the control every refusal test opens with. A reducer that
// rejects every log passes every negative case while being entirely broken, so
// no refusal below is asserted without first proving this one is accepted.
func goodLedger(t *testing.T) *ledgerBuilder {
	t.Helper()
	l := newLedger()
	l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
	l.add(t, &model.TaskStart{Task: ref(newID("TSKA"), 1), Actor: model.Actor{ID: "lane-a"}, AttemptID: newID("ATTA")})
	return l
}

func TestGoodControlReplays(t *testing.T) {
	l := goodLedger(t)
	s := mustReplay(t, l.bundles())

	if s.Project() != testProject {
		t.Fatalf("project = %q, want %q", s.Project(), testProject)
	}
	w := s.Watermark()
	if w.Sequence != 2 || w.Bundles != 2 || w.Events != 2 {
		t.Fatalf("watermark = %+v", w)
	}
	if w.CommandID != newID("CMD2") {
		t.Fatalf("watermark command = %s", w.CommandID)
	}
	p, ok := s.Task(Ident{Project: testProject, ID: newID("TSKA")})
	if !ok {
		t.Fatal("the created task is not projected")
	}
	if p.Status != StatusInFlight {
		t.Fatalf("status = %q, want IN FLIGHT", p.Status)
	}
	if len(s.Deferred()) != 0 {
		t.Fatalf("a task-only ledger deferred %d events", len(s.Deferred()))
	}
}

// ---- ledger integrity -----------------------------------------------------

func TestReplayRefusesBrokenLedgers(t *testing.T) {
	// Control first: the unmodified ledger replays.
	mustReplay(t, goodLedger(t).bundles())

	cases := []struct {
		name string
		code string
		bend func(bs []model.Bundle) []model.Bundle
	}{
		{"sequence gap", CodeLedgerDiscontinuity, func(bs []model.Bundle) []model.Bundle {
			bs[1].Sequence = 3
			return bs
		}},
		{"repeated sequence", CodeLedgerDiscontinuity, func(bs []model.Bundle) []model.Bundle {
			bs[1].Sequence = 1
			return bs
		}},
		{"bundles out of order", CodeLedgerDiscontinuity, func(bs []model.Bundle) []model.Bundle {
			return []model.Bundle{bs[1], bs[0]}
		}},
		{"wrong predecessor", CodeLedgerDiscontinuity, func(bs []model.Bundle) []model.Bundle {
			bs[1].Predecessor = newID("CMD9")
			return bs
		}},
		{"genesis with predecessor", CodeLedgerDiscontinuity, func(bs []model.Bundle) []model.Bundle {
			bs[0].Predecessor = newID("CMD9")
			return bs
		}},
		{"repeated transaction id", CodeDuplicateCommand, func(bs []model.Bundle) []model.Bundle {
			bs[1].CommandID = bs[0].CommandID
			bs[1].Predecessor = bs[0].CommandID
			return bs
		}},
		{"foreign project", CodeProjectMismatch, func(bs []model.Bundle) []model.Bundle {
			bs[1].Project = "datum/somewhere-else"
			return bs
		}},
		{"unknown wire version", CodeUnknownVersion, func(bs []model.Bundle) []model.Bundle {
			bs[1].Version = model.WireVersion + 1
			return bs
		}},
		{"empty bundle", CodeInvalidField, func(bs []model.Bundle) []model.Bundle {
			bs[1].Events = nil
			return bs
		}},
		{"admitter with neither branch", CodeInvalidField, func(bs []model.Bundle) []model.Bundle {
			bs[1].Admitter = model.Actor{}
			return bs
		}},
		{"admitter with both branches", CodeInvalidField, func(bs []model.Bundle) []model.Bundle {
			bs[1].Admitter = model.Actor{ID: "coordinator", UnknownReason: "also unknown"}
			return bs
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bs := tc.bend(goodLedger(t).bundles())
			_, err := Replay(bs)
			wantFault(t, err, tc.code)
		})
	}
}

func TestReplayPublishesNoPartialState(t *testing.T) {
	l := goodLedger(t)
	bs := l.bundles()
	mustReplay(t, bs) // control

	bs[1].Sequence = 7
	s, err := Replay(bs)
	if err == nil {
		t.Fatal("expected the broken ledger to be refused")
	}
	if s.Watermark().Sequence != 0 || len(s.Records()) != 0 || len(s.Tasks()) != 0 {
		t.Fatalf("a refused replay published state: %+v", s.Watermark())
	}
}

func TestApplyDoesNotTouchItsInput(t *testing.T) {
	l := goodLedger(t)
	before := mustReplay(t, l.bundles())

	next := l.add(t, &model.AttemptTerminal{
		Task:         ref(newID("TSKA"), 1),
		AttemptID:    newID("ATTA"),
		Outcome:      model.AttemptStopped,
		Reason:       "the lane ran out of budget",
		NextAction:   "resume with a fresh attempt",
		DeliveryRefs: []model.ArtifactRef{},
	})
	after, err := Apply(before, next)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	beforeTask, _ := before.Task(Ident{Project: testProject, ID: newID("TSKA")})
	if beforeTask.Status != StatusInFlight {
		t.Fatalf("Apply mutated its input snapshot: status is now %q", beforeTask.Status)
	}
	if before.Watermark().Sequence != 2 {
		t.Fatalf("Apply moved the input watermark to %d", before.Watermark().Sequence)
	}
	afterTask, _ := after.Task(Ident{Project: testProject, ID: newID("TSKA")})
	if afterTask.Status != StatusReady {
		t.Fatalf("after a stopped attempt the task is %q, want READY", afterTask.Status)
	}
}

func TestFailedApplyReturnsNoState(t *testing.T) {
	l := goodLedger(t)
	s := mustReplay(t, l.bundles())

	bad := l.add(t, &model.TaskStart{
		Task:      ref(newID("TSKA"), 1),
		Actor:     model.Actor{ID: "lane-b"},
		AttemptID: newID("ATTB"),
	})
	out, err := Apply(s, bad)
	wantFault(t, err, CodeInvalidTransition)
	if out.Watermark().Sequence != 0 || len(out.Tasks()) != 0 {
		t.Fatal("a refused Apply published partial state")
	}
	if s.Watermark().Sequence != 2 {
		t.Fatal("a refused Apply disturbed the input snapshot")
	}
}

// ---- expected revisions ---------------------------------------------------

func TestStaleAmendmentIsATypedConflict(t *testing.T) {
	l := newLedger()
	l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
	// Control: the first amendment at the current revision is accepted.
	l.add(t, &model.TaskAmend{
		Provenance:  provenance("lane-a"),
		Target:      ref(newID("TSKA"), 1),
		Replacement: taskSpec(withIntent("first amendment wins")),
	})
	s := mustReplay(t, l.bundles())
	if rev, _ := s.CurrentRevision(Ident{Project: testProject, ID: newID("TSKA")}); rev != 2 {
		t.Fatalf("control amendment did not land: revision %d", rev)
	}

	// The second amendment expects the same revision and must lose.
	stale := l.add(t, &model.TaskAmend{
		Provenance:  provenance("lane-b"),
		Target:      ref(newID("TSKA"), 1),
		Replacement: taskSpec(withIntent("second amendment loses")),
	})
	_, err := Apply(s, stale)
	c := wantConflict(t, err)
	if c.Sequence != 3 || c.EventIndex != 0 {
		t.Fatalf("conflict does not locate the event: %+v", c)
	}
	if c.Expected != 1 || c.Actual != 2 {
		t.Fatalf("conflict revisions = expected %d actual %d", c.Expected, c.Actual)
	}
	if c.Target.RecordID != newID("TSKA") {
		t.Fatalf("conflict target = %s", c.Target.RecordID)
	}
}

// TestLedgerSequenceDecidesWhichAmendmentLoses swaps the two conflicting
// amendments. The loser changes with ledger order and with nothing else: no
// directory enumeration, no map iteration and no wall clock is consulted.
func TestLedgerSequenceDecidesWhichAmendmentLoses(t *testing.T) {
	build := func(first, second string) error {
		l := newLedger()
		l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
		l.add(t, &model.TaskAmend{
			Provenance:  provenance("lane-a"),
			Target:      ref(newID("TSKA"), 1),
			Replacement: taskSpec(withIntent(first)),
		})
		l.add(t, &model.TaskAmend{
			Provenance:  provenance("lane-b"),
			Target:      ref(newID("TSKA"), 1),
			Replacement: taskSpec(withIntent(second)),
		})
		_, err := Replay(l.bundles())
		return err
	}

	a := wantConflict(t, build("lane a first", "lane b second"))
	b := wantConflict(t, build("lane b first", "lane a second"))
	if a.Sequence != 3 || b.Sequence != 3 {
		t.Fatalf("the rejection is not at the later bundle: %d and %d", a.Sequence, b.Sequence)
	}
	// Both orderings produce a defined rejection at the same position. What
	// differs is which content survives, and that is decided by sequence alone.
	s, err := Replay([]model.Bundle{})
	if err != nil || s.Watermark().Sequence != 0 {
		t.Fatalf("empty replay: %v %+v", err, s.Watermark())
	}
}

func TestStaleTaskRevisionOnAttemptEvents(t *testing.T) {
	l := newLedger()
	l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
	// Control: starting at the current revision works.
	l.add(t, &model.TaskStart{Task: ref(newID("TSKA"), 1), Actor: model.Actor{ID: "lane-a"}, AttemptID: newID("ATTA")})
	s := mustReplay(t, l.bundles())

	amend := l.add(t, &model.TaskAmend{
		Provenance:  provenance("coordinator"),
		Target:      ref(newID("TSKA"), 1),
		Replacement: taskSpec(withIntent("the contract moved under the lane")),
	})
	s, err := Apply(s, amend)
	if err != nil {
		t.Fatalf("control amendment: %v", err)
	}

	stale := l.add(t, &model.AttemptTerminal{
		Task:         ref(newID("TSKA"), 1),
		AttemptID:    newID("ATTA"),
		Outcome:      model.AttemptSuccess,
		Reason:       "handed back against a superseded contract",
		NextAction:   "none",
		DeliveryRefs: []model.ArtifactRef{blobRef("delivery")},
	})
	c := wantConflict(t, mustErr(Apply(s, stale)))
	if c.Expected != 1 || c.Actual != 2 {
		t.Fatalf("conflict = %+v", c)
	}
}

func mustErr(_ Snapshot, err error) error { return err }

func TestAttemptIdentityCollisionIsAProjectWideFault(t *testing.T) {
	for _, takeover := range []bool{false, true} {
		for _, terminal := range []bool{false, true} {
			t.Run(fmt.Sprintf("takeover=%t/terminal=%t", takeover, terminal), func(t *testing.T) {
				l := goodLedger(t)
				l.add(t, &model.TaskCreate{Provenance: provenance("lane-b"), ID: newID("TSKB"), Spec: taskSpec()})
				if terminal {
					l.add(t, &model.AttemptTerminal{
						Task: ref(newID("TSKA"), 1), AttemptID: newID("ATTA"),
						Outcome: model.AttemptStopped, Reason: "stopped", NextAction: "retry",
						DeliveryRefs: []model.ArtifactRef{},
					})
				}
				if takeover {
					l.add(t, &model.TaskStart{Task: ref(newID("TSKB"), 1), Actor: model.Actor{ID: "lane-b"}, AttemptID: newID("ATTB")})
				}
				before := mustReplay(t, l.bundles())
				unchanged := mustReplay(t, l.bundles())
				event := func(id model.ID) model.TypedEvent {
					if takeover {
						return &model.TaskTakeover{
							Task: ref(newID("TSKB"), 1), Actor: model.Actor{ID: "lane-c"},
							AttemptID: id, PriorAttemptID: newID("ATTB"), StoppedConfirmationRef: blobRef("stopped"),
						}
					}
					return &model.TaskStart{Task: ref(newID("TSKB"), 1), Actor: model.Actor{ID: "lane-c"}, AttemptID: id}
				}
				controlLedger := *l
				control, err := Apply(before, controlLedger.add(t, event(newID("ATTC"))))
				if err != nil {
					t.Fatalf("distinct attempt identity refused: %v", err)
				}
				if got := control.inner().attemptOwner[Ident{Project: testProject, ID: newID("ATTC")}]; got.Task != newID("TSKB") {
					t.Fatalf("distinct attempt owner = %+v", got)
				}
				// An earlier valid event must also be rolled back on collision.
				bundle := l.add(t,
					&model.TaskCreate{Provenance: provenance("lane-c"), ID: newID("TSKC"), Spec: taskSpec()},
					event(newID("ATTA")),
				)
				after, err := Apply(before, bundle)
				f := wantFault(t, err, CodeDuplicateRecord)
				if f.Path != "attempt_id" || f.Sequence != bundle.Sequence || f.EventIndex != 1 {
					t.Fatalf("collision fault location = %+v", f)
				}
				if !reflect.DeepEqual(after, Snapshot{}) || !reflect.DeepEqual(before, unchanged) {
					t.Fatal("refused collision published partial state or changed its input")
				}
			})
		}
	}
}

// ---- references -----------------------------------------------------------

func TestUnknownReferencesAreRefused(t *testing.T) {
	// Control: the same events against an admitted task are accepted.
	mustReplay(t, goodLedger(t).bundles())

	cases := []struct {
		name  string
		code  string
		build func(t *testing.T) []model.Bundle
	}{
		{"start on an unadmitted task", CodeUnknownReference, func(t *testing.T) []model.Bundle {
			l := newLedger()
			l.add(t, &model.TaskStart{Task: ref(newID("TSKA"), 1), Actor: model.Actor{ID: "lane-a"}, AttemptID: newID("ATTA")})
			return l.bundles()
		}},
		{"terminal for an unadmitted attempt", CodeUnknownReference, func(t *testing.T) []model.Bundle {
			l := goodLedger(t)
			l.add(t, &model.AttemptTerminal{
				Task: ref(newID("TSKA"), 1), AttemptID: newID("ATTZ"),
				Outcome: model.AttemptStopped, Reason: "no such attempt", NextAction: "none",
				DeliveryRefs: []model.ArtifactRef{},
			})
			return l.bundles()
		}},
		{"takeover of an unadmitted attempt", CodeUnknownReference, func(t *testing.T) []model.Bundle {
			l := goodLedger(t)
			l.add(t, &model.TaskTakeover{
				Task: ref(newID("TSKA"), 1), Actor: model.Actor{ID: "lane-b"},
				AttemptID: newID("ATTB"), PriorAttemptID: newID("ATTZ"),
				StoppedConfirmationRef: blobRef("stopped"),
			})
			return l.bundles()
		}},
		{"clear of an unadmitted hold", CodeUnknownReference, func(t *testing.T) []model.Bundle {
			l := goodLedger(t)
			l.add(t, &model.BlockerClear{
				Task: ref(newID("TSKA"), 1), BlockerID: newID("HDZ1"),
				HoldRef:          model.BlockerRef{Task: ref(newID("TSKA"), 1), BlockerID: newID("HDZ1")},
				ResolvingWitness: blobRef("witness"),
			})
			return l.bundles()
		}},
		{"context reference to a revision that does not exist", CodeUnknownReference, func(t *testing.T) []model.Bundle {
			l := goodLedger(t)
			spec := taskSpec()
			spec.ContextRefs = []model.RecordRef{ref(newID("TSKA"), 9)}
			l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKB"), Spec: spec})
			return l.bundles()
		}},
		{"second record under one id", CodeDuplicateRecord, func(t *testing.T) []model.Bundle {
			l := goodLedger(t)
			l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
			return l.bundles()
		}},
		{"second receipt on one attempt", CodeInvalidTransition, func(t *testing.T) []model.Bundle {
			l := goodLedger(t)
			term := &model.AttemptTerminal{
				Task: ref(newID("TSKA"), 1), AttemptID: newID("ATTA"),
				Outcome: model.AttemptStopped, Reason: "first receipt", NextAction: "none",
				DeliveryRefs: []model.ArtifactRef{},
			}
			l.add(t, term)
			l.add(t, &model.AttemptTerminal{
				Task: ref(newID("TSKA"), 1), AttemptID: newID("ATTA"),
				Outcome: model.AttemptSuccess, Reason: "second receipt", NextAction: "none",
				DeliveryRefs: []model.ArtifactRef{},
			})
			return l.bundles()
		}},
		{"amendment of a closed task", CodeInvalidTransition, func(t *testing.T) []model.Bundle {
			l := closedTaskLedger(t)
			l.add(t, &model.TaskAmend{
				Provenance: provenance("coordinator"), Target: ref(newID("TSKA"), 1), Replacement: taskSpec(withIntent("rewriting the closed contract")),
			})
			return l.bundles()
		}},
		{"second closure", CodeInvalidTransition, func(t *testing.T) []model.Bundle {
			l := closedTaskLedger(t)
			l.add(t, closeSuccess(newID("TSKA"), 1))
			return l.bundles()
		}},
		{"blocker id reused", CodeDuplicateRecord, func(t *testing.T) []model.Bundle {
			l := goodLedger(t)
			hold := &model.BlockerHold{
				Task: ref(newID("TSKA"), 1), BlockerID: newID("HDA1"),
				Reason: model.BlockerResume, Actor: model.Actor{ID: "lane-a"},
				Criterion: "the fixture harness is restored",
			}
			l.add(t, hold)
			l.add(t, &model.BlockerClear{
				Task: ref(newID("TSKA"), 1), BlockerID: newID("HDA1"),
				HoldRef:          model.BlockerRef{Task: ref(newID("TSKA"), 1), BlockerID: newID("HDA1")},
				ResolvingWitness: blobRef("witness"),
			})
			l.add(t, hold)
			return l.bundles()
		}},
		{"authoring into another project", CodeInvalidField, func(t *testing.T) []model.Bundle {
			l := goodLedger(t)
			l.add(t, &model.TaskStart{
				Task:      model.RecordRef{Project: "datum/elsewhere", RecordID: newID("TSKA"), Revision: 1},
				Actor:     model.Actor{ID: "lane-a"},
				AttemptID: newID("ATTB"),
			})
			return l.bundles()
		}},
		{"prerequisite pointed at the wrong kind", CodeInvalidField, func(t *testing.T) []model.Bundle {
			l := newLedger()
			l.add(t, &model.ClaimAssert{Provenance: provenance("lane-a"), ID: newID("CMA1"), Spec: claimSpec()})
			l.add(t, &model.TaskCreate{
				Provenance: provenance("lane-a"), ID: newID("TSKA"),
				Spec: taskSpec(withPrerequisite("task-success", ref(newID("CMA1"), 1), "forbid", nil)),
			})
			return l.bundles()
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Replay(tc.build(t))
			wantFault(t, err, tc.code)
		})
	}
}

// ---- determinism and order ------------------------------------------------

func TestReplayIsDeterministic(t *testing.T) {
	bundles := goldenLedger(t).bundles()
	first, err := Replay(bundles)
	if err != nil {
		t.Fatalf("first replay: %v", err)
	}
	second, err := Replay(bundles)
	if err != nil {
		t.Fatalf("second replay: %v", err)
	}
	if a, b := render(first), render(second); a != b {
		t.Fatalf("two replays of one log disagree:\n%s\n---\n%s", a, b)
	}
	if !reflect.DeepEqual(first.Records(), second.Records()) {
		t.Fatal("two replays produced different record projections")
	}
	// Repeat the projection itself: sorting must not depend on map iteration,
	// which Go randomizes per range.
	for i := 0; i < 20; i++ {
		if render(first) != render(second) {
			t.Fatalf("projection is unstable on iteration %d", i)
		}
	}
}

// TestIndependentEventsReorderFreely asserts only what is true: events that do
// not touch each other may be admitted in either order with the same answer.
// Invariance across ALL interleavings is not claimed, because a stale
// amendment genuinely conflicts and must produce a defined rejection instead.
func TestIndependentEventsReorderFreely(t *testing.T) {
	forward := newLedger()
	forward.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
	forward.add(t, &model.TaskCreate{Provenance: provenance("lane-b"), ID: newID("TSKB"), Spec: taskSpec()})
	forward.add(t, &model.TaskStart{Task: ref(newID("TSKA"), 1), Actor: model.Actor{ID: "lane-a"}, AttemptID: newID("ATTA")})
	forward.add(t, &model.BlockerHold{
		Task: ref(newID("TSKB"), 1), BlockerID: newID("HDB1"),
		Reason: model.BlockerResume, Actor: model.Actor{ID: "lane-b"},
		Criterion: "the upstream harness is repaired",
	})

	swapped := newLedger()
	swapped.add(t, &model.TaskCreate{Provenance: provenance("lane-b"), ID: newID("TSKB"), Spec: taskSpec()})
	swapped.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
	swapped.add(t, &model.BlockerHold{
		Task: ref(newID("TSKB"), 1), BlockerID: newID("HDB1"),
		Reason: model.BlockerResume, Actor: model.Actor{ID: "lane-b"},
		Criterion: "the upstream harness is repaired",
	})
	swapped.add(t, &model.TaskStart{Task: ref(newID("TSKA"), 1), Actor: model.Actor{ID: "lane-a"}, AttemptID: newID("ATTA")})

	a := statuses(mustReplay(t, forward.bundles()))
	b := statuses(mustReplay(t, swapped.bundles()))
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("independent reordering changed the answer:\n%v\n%v", a, b)
	}
	if a[string(newID("TSKA"))] != string(StatusInFlight) || a[string(newID("TSKB"))] != string(StatusBlocked) {
		t.Fatalf("control expectation wrong: %v", a)
	}
}

func statuses(s Snapshot) map[string]string {
	out := map[string]string{}
	for _, p := range s.Tasks() {
		out[string(p.Task.ID)] = string(p.Status)
	}
	return out
}

// ---- the golden log -------------------------------------------------------

func closeSuccess(task model.ID, rev model.Revision) *model.TaskClose {
	return &model.TaskClose{
		Task:      ref(task, rev),
		Outcome:   model.ClosureSuccess,
		Authority: closeAuthority(),
		AcceptanceWitnessRefs: []model.AcceptanceWitness{
			{CriterionID: newID("ACCA"), CriterionRevision: 1, WitnessRef: blobRef("acceptance")},
		},
		DeliveryWitnessRefs: []model.ArtifactRef{blobRef("delivery")},
	}
}

// closedTaskLedger is the shared CLOSED-with-success fixture: create, start,
// succeed, then close with witnesses that apply to the closed revision.
func closedTaskLedger(t *testing.T) *ledgerBuilder {
	t.Helper()
	l := newLedger()
	l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
	l.add(t, &model.TaskStart{Task: ref(newID("TSKA"), 1), Actor: model.Actor{ID: "lane-a"}, AttemptID: newID("ATTA")})
	l.add(t, &model.AttemptTerminal{
		Task: ref(newID("TSKA"), 1), AttemptID: newID("ATTA"),
		Outcome: model.AttemptSuccess, Reason: "the reducer folds the four statuses",
		NextAction: "coordinator accepts", DeliveryRefs: []model.ArtifactRef{blobRef("delivery")},
	})
	l.add(t, closeSuccess(newID("TSKA"), 1))
	return l
}

// goldenLedger is a fixed log whose projection is asserted byte for byte below.
// Its whole job is to make any later change that moves an answer visible in the
// same commit that moves it, instead of weeks later in a real query.
func goldenLedger(t *testing.T) *ledgerBuilder {
	t.Helper()
	l := closedTaskLedger(t) // TSKA: CLOSED success

	// TSKB depends on TSKA at the revision that was closed and witnessed.
	l.add(t, &model.TaskCreate{
		Provenance: provenance("lane-b"), ID: newID("TSKB"),
		Spec: taskSpec(
			withIntent("consume the closed dependency"),
			withNextActor(model.Actor{ID: "lane-b"}),
			withPrerequisite("task-success", ref(newID("TSKA"), 1), "forbid", nil),
		),
	})

	// TSKC is live, TSKD waits on it.
	l.add(t, &model.TaskCreate{Provenance: provenance("lane-c"), ID: newID("TSKC"), Spec: taskSpec(withIntent("run the harness"))})
	l.add(t, &model.TaskStart{Task: ref(newID("TSKC"), 1), Actor: model.Actor{ID: "lane-c"}, AttemptID: newID("ATTC")})
	l.add(t, &model.TaskCreate{
		Provenance: provenance("lane-d"), ID: newID("TSKD"),
		Spec: taskSpec(
			withIntent("wait on the live task"),
			withPrerequisite("task-success", ref(newID("TSKC"), 1), "forbid", nil),
		),
	})

	// TSKE succeeded but nobody accepted it.
	l.add(t, &model.TaskCreate{Provenance: provenance("lane-e"), ID: newID("TSKE"), Spec: taskSpec(withIntent("hand back without acceptance"))})
	l.add(t, &model.TaskStart{Task: ref(newID("TSKE"), 1), Actor: model.Actor{ID: "lane-e"}, AttemptID: newID("ATTE")})
	l.add(t, &model.AttemptTerminal{
		Task: ref(newID("TSKE"), 1), AttemptID: newID("ATTE"),
		Outcome: model.AttemptSuccess, Reason: "the work is done",
		NextAction: "coordinator accepts", DeliveryRefs: []model.ArtifactRef{blobRef("delivery-e")},
	})

	// A captured source, so the golden log is not tasks alone.
	l.add(t, &model.SourceIntake{
		SourceID:       newID("SRC1"),
		OriginalDigest: newDigest("owner transcript"),
		Length:         uint64(len("owner transcript")),
		SourceRef: model.ArtifactRef{
			Kind: "content",
			Content: &model.ContentPin{
				SHA256:    newDigest("owner transcript"),
				Length:    uint64(len("owner transcript")),
				MediaType: "text/plain",
				Locators:  []model.Locator{{Path: ".whosaidso/artifacts/transcript"}},
			},
			Selector: model.Selector{Kind: "whole"},
		},
		Speaker:   model.Actor{ID: "owner"},
		Order:     0,
		Referents: []model.RecordRef{ref(newID("TSKA"), 1)},
	})
	return l
}

// render is the golden projection: every task, its status, its outcome, its
// reason kinds and its prerequisite truths. Anything that moves an answer moves
// this string.
func render(s Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "watermark seq=%d bundles=%d events=%d\n",
		s.Watermark().Sequence, s.Watermark().Bundles, s.Watermark().Events)
	for _, p := range s.Tasks() {
		fmt.Fprintf(&b, "%s rev=%d %s", p.Task.ID, p.Task.Revision, p.Status)
		if p.Outcome != "" {
			fmt.Fprintf(&b, " outcome=%s", p.Outcome)
		}
		fmt.Fprintf(&b, " live=%d attempts=%d", len(p.LiveAttempts), len(p.Attempts))
		for _, r := range p.Prerequisites {
			fmt.Fprintf(&b, " [prereq %d %s=%s waived=%t]", r.Index, r.Kind, r.Truth, r.Waived)
		}
		for _, r := range p.Reasons {
			fmt.Fprintf(&b, " [blocked %s]", r.Kind)
		}
		for _, a := range p.WaitingActors {
			if a.ID != "" {
				fmt.Fprintf(&b, " [waiting %s]", a.ID)
			} else {
				fmt.Fprintf(&b, " [waiting unknown]")
			}
		}
		b.WriteString("\n")
	}
	for _, src := range s.Sources() {
		fmt.Fprintf(&b, "source %s speaker=%s referents=%d\n", src.Key.Source, src.Intake.Speaker.ID, len(src.Intake.Referents))
	}
	return b.String()
}

const goldenProjection = `watermark seq=12 bundles=12 events=14
0000000000000000000000TSKA rev=1 CLOSED outcome=success live=0 attempts=1
0000000000000000000000TSKB rev=1 READY live=0 attempts=0 [prereq 0 task-success=TRUE waived=false]
0000000000000000000000TSKC rev=1 IN FLIGHT live=1 attempts=1
0000000000000000000000TSKD rev=1 BLOCKED live=0 attempts=0 [prereq 0 task-success=FALSE waived=false] [blocked prerequisite] [waiting coordinator]
0000000000000000000000TSKE rev=1 BLOCKED live=0 attempts=1 [blocked awaiting-acceptance] [waiting coordinator]
source 0000000000000000000000SRC1 speaker=owner referents=1
`

func TestGoldenLog(t *testing.T) {
	s := mustReplay(t, goldenLedger(t).bundles())
	got := render(s)
	if got != goldenProjection {
		t.Fatalf("the golden projection moved.\ngot:\n%s\nwant:\n%s", got, goldenProjection)
	}
}

func TestGoldenLogReplaysBundleByBundle(t *testing.T) {
	bundles := goldenLedger(t).bundles()
	var s Snapshot
	var err error
	for i, b := range bundles {
		s, err = Apply(s, b)
		if err != nil {
			t.Fatalf("apply bundle %d: %v", i+1, err)
		}
	}
	if got := render(s); got != goldenProjection {
		t.Fatalf("incremental Apply disagrees with Replay:\n%s", got)
	}
}
