package acceptance_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
)

const reduceProject = model.ProjectID("example/reduce")

func reduceID(n int) model.ID {
	return model.ID(fmt.Sprintf("00000000000000000000%06d", n))
}

func reduceRef(n int, rev model.Revision) model.RecordRef {
	return model.RecordRef{Project: reduceProject, RecordID: reduceID(n), Revision: rev}
}

func reduceIdent(n int) reduce.Ident {
	return reduce.Ident{Project: reduceProject, ID: reduceID(n)}
}

func reduceArtifact(name string) model.ArtifactRef {
	return model.ArtifactRef{
		Kind: "content", Selector: model.Selector{Kind: "whole"},
		Content: &model.ContentPin{
			SHA256: model.HashBytes([]byte(name)), Length: uint64(len(name)),
			MediaType: "text/plain", Locators: []model.Locator{{Path: ".whosaidso/artifacts/" + name}},
		},
	}
}

func reduceScope() model.Scope {
	return model.Scope{
		SourcePaths: []string{"internal/reduce/task.go"}, ContextRefs: []model.RecordRef{},
		AppliesWhen: "the independent reducer fixture runs", Limitations: "no artifact availability claim",
	}
}

func reduceProvenance() model.Provenance {
	return model.Provenance{SourceRefs: []model.ArtifactRef{reduceArtifact("request")}}
}

func reduceSpec(criterionRevision model.Revision) model.TaskSpec {
	return model.TaskSpec{
		Intent: "preserve the admitted task obligation", Subject: "the reducer fixture", Scope: reduceScope(),
		NonGoals: []string{"resolve artifact bytes"},
		AcceptanceCriteria: []model.AcceptanceCriterion{{
			ID: reduceID(90), Revision: criterionRevision, Criterion: "the required task revision has an applicable witness",
		}},
		ContextRefs: []model.RecordRef{}, ConstraintRefs: []model.RecordRef{}, Prerequisites: []model.Prerequisite{},
		NextActor: model.Actor{ID: "acceptance-owner"},
	}
}

func reduceCreate(n int, spec model.TaskSpec) *model.TaskCreate {
	return &model.TaskCreate{ID: reduceID(n), Provenance: reduceProvenance(), Spec: spec}
}

func reduceClose(n int, revision, criterionRevision model.Revision, outcome model.ClosureOutcome) *model.TaskClose {
	return &model.TaskClose{
		Task: reduceRef(n, revision), Outcome: outcome,
		Authority: &model.Authority{ // A closure's authority is optional, so a pointer.
			Actor: model.Actor{ID: "owner"}, SourceRef: reduceArtifact("ruling"),
			Selector: model.Selector{Kind: "json-pointer", Pointer: "/ruling"}, Scope: reduceScope(),
		},
		AcceptanceWitnessRefs: []model.AcceptanceWitness{{
			CriterionID: reduceID(90), CriterionRevision: criterionRevision, WitnessRef: reduceArtifact("acceptance"),
		}},
		DeliveryWitnessRefs: []model.ArtifactRef{reduceArtifact("delivery")},
	}
}

func reduceConsumer(requiredRevision model.Revision) *model.TaskCreate {
	spec := reduceSpec(1)
	spec.Prerequisites = []model.Prerequisite{{Kind: "task-success", Target: reduceRef(1, requiredRevision), WaiverPolicy: "forbid"}}
	return reduceCreate(2, spec)
}

func reduceBundle(t *testing.T, previous model.Bundle, events ...model.TypedEvent) model.Bundle {
	t.Helper()
	sequence := previous.Sequence + 1
	b := model.Bundle{
		Version: model.WireVersion, Project: reduceProject, Sequence: sequence,
		CommandID: reduceID(1000 + int(sequence)), Predecessor: previous.CommandID,
		RequestDigest: model.HashBytes([]byte(fmt.Sprintf("request-%d", sequence))),
		Admitter:      model.Actor{ID: "coordinator"}, RecordedAt: time.Date(2026, 9, 22, 12, int(sequence), 0, 0, time.UTC),
		Packets: []model.PacketRef{}, Events: []model.Event{},
	}
	for _, payload := range events {
		raw, err := model.EncodeEvent(payload)
		if err != nil {
			t.Fatalf("fixture %s must pass the strict event codec: %v", payload.EventType(), err)
		}
		b.Events = append(b.Events, raw)
	}
	return b
}

// reducePacket is one intake packet as the store captures it: an author
// and the capture instant intake stamps.
func reducePacket(t *testing.T, n int, author string, captured time.Time, events ...model.TypedEvent) model.Packet {
	t.Helper()
	p := model.Packet{Version: model.WireVersion, Project: reduceProject, CommandID: reduceID(n),
		RequestDigest: model.HashBytes([]byte(fmt.Sprintf("packet-%d", n))), Author: model.Actor{ID: author},
		CapturedAt: captured.UTC(), Events: []model.Event{}}
	for _, payload := range events {
		raw, err := model.EncodeEvent(payload)
		if err != nil {
			t.Fatalf("fixture %s must pass the strict event codec: %v", payload.EventType(), err)
		}
		p.Events = append(p.Events, raw)
	}
	return p
}

// reduceReview returns the packets' events followed by the accepted
// review.admit write.Admit records for them: packet authors, capture stamps
// and the packet of every event, so replay can check attribution and
// chronology from the ledger alone.
func reduceReview(t *testing.T, admitter model.Actor, refs []model.PacketRef, packets []model.Packet) []model.Event {
	t.Helper()
	events := []model.Event{}
	authors := map[model.ID]model.Actor{}
	// A capture time is an Availability, known here.
	captured := map[model.ID]model.Availability[time.Time]{}
	eventPackets := []model.ID{}
	for _, p := range packets {
		events = append(events, p.Events...)
		for range p.Events {
			eventPackets = append(eventPackets, p.CommandID)
		}
		at := p.CapturedAt.UTC()
		authors[p.CommandID], captured[p.CommandID] = p.Author, model.Availability[time.Time]{State: model.Known, Value: &at}
	}
	review, err := model.EncodeEvent(&model.ReviewAdmit{Packets: refs, Outcome: "accepted", Actor: admitter,
		Reason: "the fixture packets were independently checked", Authors: authors, CapturedAt: captured, EventPackets: eventPackets})
	if err != nil {
		t.Fatalf("fixture review.admit must pass the strict event codec: %v", err)
	}
	return append(events, review)
}

// reduceAdmitted is reduceBundle in the shape admission publishes:
// the bundle binds its packets by digest and ends in their accepted review.
// Every packet is captured before the bundle is recorded.
func reduceAdmitted(t *testing.T, previous model.Bundle, packets ...model.Packet) model.Bundle {
	t.Helper()
	b := reduceBundle(t, previous)
	for _, p := range packets {
		if !p.CapturedAt.Before(b.RecordedAt) {
			t.Fatalf("fixture packet %s captured at %s cannot be admitted by a bundle recorded at %s", p.CommandID, p.CapturedAt, b.RecordedAt)
		}
		data, err := model.Encode(p)
		if err != nil {
			t.Fatalf("fixture packet must encode: %v", err)
		}
		b.Packets = append(b.Packets, model.PacketRef{CommandID: p.CommandID, Digest: model.HashBytes(data)})
	}
	b.Events = reduceReview(t, b.Admitter, b.Packets, packets)
	return b
}

func reduceReplay(t *testing.T, bundles ...model.Bundle) reduce.Snapshot {
	t.Helper()
	s, err := reduce.Replay(bundles)
	if err != nil {
		t.Fatalf("valid control ledger must replay: %v", err)
	}
	return s
}

func reduceTask(t *testing.T, s reduce.Snapshot, n int) reduce.TaskProjection {
	t.Helper()
	p, ok := s.Task(reduceIdent(n))
	if !ok {
		t.Fatalf("admitted task %d disappeared", n)
	}
	return p
}

func TestReducerValidWitnessedSuccessSatisfiesItsConsumerButEveryNonSuccessClosureBlocks(t *testing.T) {
	for _, outcome := range []model.ClosureOutcome{model.ClosureSuccess, model.ClosureCancelled, model.ClosureWithdrawn, model.ClosureWaived} {
		t.Run(string(outcome), func(t *testing.T) {
			b := reduceBundle(t, model.Bundle{}, reduceCreate(1, reduceSpec(1)), reduceClose(1, 1, 1, outcome), reduceConsumer(1))
			s := reduceReplay(t, b)
			producer, consumer := reduceTask(t, s, 1), reduceTask(t, s, 2)
			if producer.Status != reduce.StatusClosed || producer.Outcome != outcome {
				t.Fatalf("control producer status %s outcome %s, want CLOSED %s", producer.Status, producer.Outcome, outcome)
			}
			want := reduce.StatusBlocked
			if outcome == model.ClosureSuccess {
				want = reduce.StatusReady
			}
			if consumer.Status != want {
				t.Fatalf("consumer of CLOSED %s became %s, want %s", outcome, consumer.Status, want)
			}
		})
	}
}

func reduceRevisedDependency(t *testing.T, witnessRevision, requiredRevision model.Revision) reduce.Snapshot {
	t.Helper()
	b := reduceBundle(t, model.Bundle{},
		reduceCreate(1, reduceSpec(1)),
		&model.TaskAmend{Provenance: reduceProvenance(), Target: reduceRef(1, 1), Replacement: reduceSpec(2)},
		reduceClose(1, 2, witnessRevision, model.ClosureSuccess), reduceConsumer(requiredRevision))
	return reduceReplay(t, b)
}

func TestReducerDependencyRequiresTheProducerToBeClosedAsWellAsAHistoricalWitness(t *testing.T) {
	control := reduceRevisedDependency(t, 2, 2)
	if got := reduceTask(t, control, 2).Status; got != reduce.StatusReady {
		t.Fatalf("control with current witnessed closure became %s, want READY", got)
	}

	s := reduceRevisedDependency(t, 1, 1)
	producer := reduceTask(t, s, 1)
	if producer.Status != reduce.StatusBlocked {
		t.Fatalf("control stale closure must leave its producer BLOCKED, got %s", producer.Status)
	}
	consumer := reduceTask(t, s, 2)
	if consumer.Status != reduce.StatusBlocked || consumer.Prerequisites[0].Truth != reduce.TruthFalse {
		t.Fatalf("producer revision 2 is BLOCKED on its stale witness, but consumer requiring revision 1 became %s with truth %s. A historical witness cannot replace the CLOSED conjunct", consumer.Status, consumer.Prerequisites[0].Truth)
	}
}

func TestReducerClosedProducerCannotSatisfyADifferentRequiredCriterionRevision(t *testing.T) {
	control := reduceRevisedDependency(t, 2, 2)
	if got := reduceTask(t, control, 2).Status; got != reduce.StatusReady {
		t.Fatalf("control applicable witness became %s", got)
	}
	s := reduceRevisedDependency(t, 2, 1)
	if got := reduceTask(t, s, 1).Status; got != reduce.StatusClosed {
		t.Fatalf("control producer must be CLOSED, got %s", got)
	}
	if got := reduceTask(t, s, 2).Status; got != reduce.StatusBlocked {
		t.Fatalf("a witness only for criterion revision 2 satisfied a consumer requiring revision 1, got %s", got)
	}
}

func TestReducerReturnedClosureCannotRewriteAnEarlierApplySnapshot(t *testing.T) {
	first := reduceBundle(t, model.Bundle{}, reduceCreate(1, reduceSpec(1)), reduceClose(1, 1, 1, model.ClosureSuccess))
	before := reduceReplay(t, first)
	next := reduceBundle(t, first, reduceCreate(2, reduceSpec(1)))
	after, err := reduce.Apply(before, next)
	if err != nil {
		t.Fatalf("control independent Apply must succeed: %v", err)
	}
	if reduceTask(t, before, 1).Status != reduce.StatusClosed || reduceTask(t, after, 1).Status != reduce.StatusClosed {
		t.Fatal("control both snapshots must initially read CLOSED")
	}
	closure, ok := after.Closure(reduceIdent(1))
	if !ok {
		t.Fatal("control admitted closure disappeared")
	}
	closure.AcceptanceWitnesses[0].CriterionRevision = 99
	if got := reduceTask(t, before, 1).Status; got != reduce.StatusClosed {
		t.Fatalf("mutating a closure returned from the later Apply snapshot changed the earlier snapshot from CLOSED to %s without any admitted event", got)
	}
}

func TestReducerReturnedTaskSpecCannotRewriteAnEarlierApplySnapshot(t *testing.T) {
	first := reduceBundle(t, model.Bundle{}, reduceCreate(1, reduceSpec(1)), reduceClose(1, 1, 1, model.ClosureSuccess))
	before := reduceReplay(t, first)
	after, err := reduce.Apply(before, reduceBundle(t, first, reduceCreate(2, reduceSpec(1))))
	if err != nil {
		t.Fatalf("control independent Apply must succeed: %v", err)
	}
	if got := reduceTask(t, before, 1).Status; got != reduce.StatusClosed {
		t.Fatalf("control task must be CLOSED, got %s", got)
	}
	reduceTask(t, after, 1).Spec.AcceptanceCriteria[0].Revision = 99
	if got := reduceTask(t, before, 1).Status; got != reduce.StatusClosed {
		t.Fatalf("mutating the later snapshot's returned task spec changed the earlier snapshot from CLOSED to %s. The caller read-only convention does not isolate snapshots", got)
	}
}

func TestReducerReturnedTerminalCannotAddReconciliationDebtToAnotherSnapshot(t *testing.T) {
	// The reducer requires a terminal receipt in its holder's packet, so the
	// receipt travels in worker's reviewed packet, not an unattributed bundle.
	captured := recWhen.Add(time.Hour)
	first := reduceAdmitted(t, model.Bundle{},
		reducePacket(t, 2101, "agent-e", captured, reduceCreate(1, reduceSpec(1))),
		reducePacket(t, 2102, "worker", captured,
			&model.TaskStart{Task: reduceRef(1, 1), Actor: model.Actor{ID: "worker"}, AttemptID: reduceID(70)},
			&model.AttemptTerminal{Task: reduceRef(1, 1), AttemptID: reduceID(70), Outcome: model.AttemptNoReading,
				Reason: "no measurement was produced", NextAction: "run the repaired instrument", DeliveryRefs: []model.ArtifactRef{}}),
	)
	before := reduceReplay(t, first)
	after, err := reduce.Apply(before, reduceBundle(t, first, reduceCreate(2, reduceSpec(1))))
	if err != nil {
		t.Fatalf("control independent Apply must succeed: %v", err)
	}
	if got := reduceTask(t, before, 1).Status; got != reduce.StatusReady {
		t.Fatalf("control honest no-reading receipt must leave READY, got %s", got)
	}
	after.Attempts(reduceIdent(1))[0].Terminal.ReconciliationOwed = true
	if got := reduceTask(t, before, 1).Status; got != reduce.StatusReady {
		t.Fatalf("mutating a terminal returned by a later Apply snapshot added reconciliation debt to the earlier snapshot, changing READY to %s", got)
	}
}

func TestReducerApplyFailureAfterAnEarlierEventPublishesNoStateAndDoesNotAmendItsInput(t *testing.T) {
	first := reduceBundle(t, model.Bundle{}, reduceCreate(1, reduceSpec(1)))
	before := reduceReplay(t, first)
	amend := &model.TaskAmend{Provenance: reduceProvenance(), Target: reduceRef(1, 1), Replacement: reduceSpec(2)}
	control, err := reduce.Apply(before, reduceBundle(t, first, amend))
	if err != nil || reduceTask(t, control, 1).Task.Revision != 2 {
		t.Fatalf("control single amendment must produce revision 2: %v", err)
	}
	failed, err := reduce.Apply(before, reduceBundle(t, first, amend, amend))
	var conflict *reduce.Conflict
	if !errors.As(err, &conflict) || conflict.EventIndex != 1 || conflict.Sequence != 2 {
		t.Fatalf("second amendment must lose at sequence 2 event 1, got %v", err)
	}
	if !reflect.DeepEqual(failed, reduce.Snapshot{}) {
		t.Fatalf("failed Apply returned partial state: watermark %+v", failed.Watermark())
	}
	if got := reduceTask(t, before, 1).Task.Revision; got != 1 {
		t.Fatalf("failed Apply mutated input revision to %d", got)
	}
}

func TestReducerLedgerSequenceAlwaysDecidesWhichSameRevisionAmendmentLoses(t *testing.T) {
	for _, order := range [][2]string{{"alpha", "beta"}, {"beta", "alpha"}} {
		t.Run(order[0]+"_before_"+order[1], func(t *testing.T) {
			first := reduceBundle(t, model.Bundle{}, reduceCreate(1, reduceSpec(1)))
			amend := func(intent string) *model.TaskAmend {
				spec := reduceSpec(1)
				spec.Intent = intent
				return &model.TaskAmend{Provenance: reduceProvenance(), Target: reduceRef(1, 1), Replacement: spec}
			}
			winner := reduceBundle(t, first, amend(order[0]))
			control := reduceReplay(t, first, winner)
			if got := reduceTask(t, control, 1).Spec.Intent; got != order[0] {
				t.Fatalf("control earlier amendment intent = %q", got)
			}
			loser := reduceBundle(t, winner, amend(order[1]))
			for repetition := 0; repetition < 5; repetition++ {
				failed, err := reduce.Replay([]model.Bundle{first, winner, loser})
				var conflict *reduce.Conflict
				if !errors.As(err, &conflict) || conflict.Sequence != 3 || conflict.EventIndex != 0 || conflict.Expected != 1 || conflict.Actual != 2 {
					t.Fatalf("later %s amendment must consistently lose against %s at sequence 3: %v", order[1], order[0], err)
				}
				if !reflect.DeepEqual(failed, reduce.Snapshot{}) {
					t.Fatal("conflicting replay returned partial state")
				}
			}
		})
	}
}

func TestReducerBlockedWinsWhenAllPrerequisitesAreTrueAndAHoldRemainsOpen(t *testing.T) {
	first := reduceBundle(t, model.Bundle{}, reduceCreate(1, reduceSpec(1)), reduceClose(1, 1, 1, model.ClosureSuccess), reduceConsumer(1))
	before := reduceReplay(t, first)
	if got := reduceTask(t, before, 2).Status; got != reduce.StatusReady {
		t.Fatalf("control consumer with true prerequisite must be READY, got %s", got)
	}
	hold := &model.BlockerHold{Task: reduceRef(2, 1), BlockerID: reduceID(80), Reason: model.BlockerReconciliation,
		Actor: model.Actor{ID: "owner"}, Criterion: "reconcile the two independent observations"}
	after, err := reduce.Apply(before, reduceBundle(t, first, hold))
	if err != nil {
		t.Fatalf("valid reconciliation hold must apply: %v", err)
	}
	p := reduceTask(t, after, 2)
	if p.Prerequisites[0].Truth != reduce.TruthTrue || p.Status != reduce.StatusBlocked {
		t.Fatalf("true prerequisite with open reconciliation hold became %s with truth %s, want BLOCKED and TRUE", p.Status, p.Prerequisites[0].Truth)
	}
}

func TestReducerIndependentEventOrderDoesNotMoveTaskAnswers(t *testing.T) {
	build := func(reverse bool) reduce.Snapshot {
		create := []model.TypedEvent{reduceCreate(1, reduceSpec(1)), reduceCreate(2, reduceSpec(1))}
		start := []model.TypedEvent{
			&model.TaskStart{Task: reduceRef(1, 1), Actor: model.Actor{ID: "worker-a"}, AttemptID: reduceID(70)},
			&model.BlockerHold{Task: reduceRef(2, 1), BlockerID: reduceID(80), Reason: model.BlockerResume, Actor: model.Actor{ID: "worker-b"}, Criterion: "a resume ruling exists"},
		}
		finish := []model.TypedEvent{
			&model.AttemptTerminal{Task: reduceRef(1, 1), AttemptID: reduceID(70), Outcome: model.AttemptNoReading, Reason: "the instrument emitted no reading", NextAction: "retry the instrument", DeliveryRefs: []model.ArtifactRef{}},
			&model.BlockerClear{Task: reduceRef(2, 1), BlockerID: reduceID(80), HoldRef: model.BlockerRef{Task: reduceRef(2, 1), BlockerID: reduceID(80)}, ResolvingWitness: reduceArtifact("resume")},
		}
		if reverse {
			create[0], create[1] = create[1], create[0]
			start[0], start[1] = start[1], start[0]
			finish[0], finish[1] = finish[1], finish[0]
		}
		first := reduceBundle(t, model.Bundle{}, create...)
		second := reduceBundle(t, first, start...)
		// The terminal receipt travels in its holder worker-a's reviewed packet.
		finished := reduceAdmitted(t, second, reducePacket(t, 2201, "worker-a", recWhen.Add(time.Hour), finish...))
		return reduceReplay(t, first, second, finished)
	}
	left, right := build(false), build(true)
	for _, n := range []int{1, 2} {
		a, b := reduceTask(t, left, n), reduceTask(t, right, n)
		if a.Status != reduce.StatusReady || b.Status != reduce.StatusReady || !reflect.DeepEqual(a.Spec, b.Spec) || a.Outcome != b.Outcome || a.CommitsDenied != b.CommitsDenied {
			t.Fatalf("independent event reordering changed task %d: left %s right %s", n, a.Status, b.Status)
		}
	}
	for repetition := 0; repetition < 5; repetition++ {
		if !reflect.DeepEqual(left.Tasks(), left.Tasks()) {
			t.Fatal("repeated projection of one immutable prefix produced different answers")
		}
	}
}

func TestReducerInputEventBytesCanChangeAfterApplyWithoutChangingTheSnapshot(t *testing.T) {
	first := reduceBundle(t, model.Bundle{}, reduceCreate(1, reduceSpec(1)))
	s, err := reduce.Apply(reduce.Snapshot{}, first)
	if err != nil {
		t.Fatalf("control genesis Apply must succeed: %v", err)
	}
	before := reduceTask(t, s, 1)
	for i := range first.Events[0].Data {
		first.Events[0].Data[i] = ' '
	}
	after := reduceTask(t, s, 1)
	if !reflect.DeepEqual(before, after) || after.Status != reduce.StatusReady {
		t.Fatal("mutating caller-owned event bytes changed an already returned snapshot")
	}
}

func reduceInvocation(attempt, invocation int) *model.InvocationStart {
	envelope := recEnvelope()
	envelope.InvocationID = reduceID(invocation)
	envelope.AttemptID = reduceID(attempt)
	envelope.ExecutionSourceIdentity.Project = reduceProject
	return &model.InvocationStart{Envelope: envelope}
}

func TestReducerAttemptIDReuseCannotSilentlyReassignInvocationOwnership(t *testing.T) {
	// Each start travels in its worker's packet, captured after it started, so
	// the invocations carry the capture stamps replay freezes them against.
	captured := recWhen.Add(time.Hour)
	first := reduceAdmitted(t, model.Bundle{},
		reducePacket(t, 2001, "agent-e", captured, reduceCreate(1, reduceSpec(1)), reduceCreate(2, reduceSpec(1))),
		reducePacket(t, 2002, "worker-a", captured,
			&model.TaskStart{Task: reduceRef(1, 1), Actor: model.Actor{ID: "worker-a"}, AttemptID: reduceID(70)}, reduceInvocation(70, 81)))
	before := reduceReplay(t, first)
	original, ok := before.Invocation(reduce.InvocationKey{Project: reduceProject, InvocationID: reduceID(81)})
	if !ok || original.Attempt.Task != reduceID(1) {
		t.Fatalf("control invocation must belong to task 1, got %+v", original.Attempt)
	}
	unique := reduceAdmitted(t, first, reducePacket(t, 2003, "worker-b", captured,
		&model.TaskStart{Task: reduceRef(2, 1), Actor: model.Actor{ID: "worker-b"}, AttemptID: reduceID(71)}, reduceInvocation(71, 82)))
	control, err := reduce.Apply(before, unique)
	if err != nil {
		t.Fatalf("control distinct attempt identity must be accepted: %v", err)
	}
	other, ok := control.Invocation(reduce.InvocationKey{Project: reduceProject, InvocationID: reduceID(82)})
	if !ok || other.Attempt.Task != reduceID(2) {
		t.Fatalf("control unique second attempt must resolve to task 2, got %+v", other.Attempt)
	}
	reused := reduceAdmitted(t, first, reducePacket(t, 2004, "worker-b", captured,
		&model.TaskStart{Task: reduceRef(2, 1), Actor: model.Actor{ID: "worker-b"}, AttemptID: reduceID(70)}, reduceInvocation(70, 82)))
	after, err := reduce.Apply(before, reused)
	if err != nil {
		if !reflect.DeepEqual(after, reduce.Snapshot{}) {
			t.Fatal("refused attempt identity collision returned partial state")
		}
		return
	}
	rebound, ok := after.Invocation(reduce.InvocationKey{Project: reduceProject, InvocationID: reduceID(82)})
	if !ok {
		t.Fatal("admitted second invocation disappeared")
	}
	t.Fatalf("one project and attempt ID was admitted under two tasks. Invocation 81 resolves it to task %s and invocation 82 resolves the identical identity to task %s. The later start silently replaced the ownership index", original.Attempt.Task, rebound.Attempt.Task)
}
