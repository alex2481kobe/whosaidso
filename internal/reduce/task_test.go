package reduce

import (
	"testing"

	"whosaidso/internal/model"
)

func projectTask(t *testing.T, l *ledgerBuilder, id model.ID) TaskProjection {
	t.Helper()
	s := mustReplay(t, l.bundles())
	p, ok := s.Task(Ident{Project: testProject, ID: id})
	if !ok {
		t.Fatalf("task %s is not projected", id)
	}
	return p
}

func reasonKinds(p TaskProjection) []string {
	out := []string{}
	for _, r := range p.Reasons {
		out = append(out, r.Kind)
	}
	return out
}

func hasReason(p TaskProjection, kind string) bool {
	for _, r := range p.Reasons {
		if r.Kind == kind {
			return true
		}
	}
	return false
}

// ---- the four statuses ----------------------------------------------------

func TestTheFourStatuses(t *testing.T) {
	cases := []struct {
		name   string
		want   TaskStatus
		build  func(t *testing.T) *ledgerBuilder
		target model.ID
	}{
		{"created and unobstructed", StatusReady, func(t *testing.T) *ledgerBuilder {
			l := newLedger()
			l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
			return l
		}, newID("TSKA")},

		{"held open", StatusBlocked, func(t *testing.T) *ledgerBuilder {
			l := newLedger()
			l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
			l.add(t, &model.BlockerHold{
				Task: ref(newID("TSKA"), 1), BlockerID: newID("HDA1"),
				Reason: model.BlockerPrerequisite, Actor: model.Actor{ID: "owner"},
				Criterion: "the owner rules on the shell material",
			})
			return l
		}, newID("TSKA")},

		{"an attempt is open", StatusInFlight, goodLedger, newID("TSKA")},

		{"closed with witnesses", StatusClosed, closedTaskLedger, newID("TSKA")},

		{"closed cancelled needs no witness", StatusClosed, func(t *testing.T) *ledgerBuilder {
			l := newLedger()
			l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
			l.add(t, &model.TaskClose{
				Task: ref(newID("TSKA"), 1), Outcome: model.ClosureCancelled,
				Authority:             closeAuthority(),
				AcceptanceWitnessRefs: []model.AcceptanceWitness{},
				DeliveryWitnessRefs:   []model.ArtifactRef{},
			})
			return l
		}, newID("TSKA")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := projectTask(t, tc.build(t), tc.target)
			if p.Status != tc.want {
				t.Fatalf("status = %q, want %q (reasons %v)", p.Status, tc.want, reasonKinds(p))
			}
		})
	}
}

// TestClosedIsNeverReadAlone proves the outcome is carried out of the fold.
// Cancelled and withdrawn are CLOSED too, so any consumer branching on the
// status alone would treat an abandoned task as finished work.
func TestClosedIsNeverReadAlone(t *testing.T) {
	for _, outcome := range []model.ClosureOutcome{model.ClosureCancelled, model.ClosureWithdrawn, model.ClosureWaived} {
		l := newLedger()
		l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
		l.add(t, &model.TaskClose{
			Task: ref(newID("TSKA"), 1), Outcome: outcome,
			Authority:             closeAuthority(),
			AcceptanceWitnessRefs: []model.AcceptanceWitness{},
			DeliveryWitnessRefs:   []model.ArtifactRef{},
		})
		p := projectTask(t, l, newID("TSKA"))
		if p.Status != StatusClosed {
			t.Fatalf("%s: status = %q", outcome, p.Status)
		}
		if p.Outcome != outcome {
			t.Fatalf("%s: outcome = %q", outcome, p.Outcome)
		}
	}
}

// ---- BLOCKED wins over READY ----------------------------------------------

// blockedOverReadyLedger has every prerequisite TRUE. The only thing standing
// between it and READY is what the caller adds after it.
func blockedOverReadyLedger(t *testing.T) *ledgerBuilder {
	t.Helper()
	l := closedTaskLedger(t) // TSKA closes successfully, so the dependency is TRUE
	l.add(t, &model.TaskCreate{
		Provenance: provenance("lane-b"), ID: newID("TSKB"),
		Spec: taskSpec(
			withIntent("dispatchable except for what is owed"),
			withPrerequisite("task-success", ref(newID("TSKA"), 1), "forbid", nil),
		),
	})
	return l
}

// TestMustProduceBlocked is the must-fail fixture. Every prerequisite is TRUE
// and reconciliation is owed, so both predicate 3 and predicate 4 hold. The
// order decides, and dispatching this task is the failure the order prevents.
// If this test ever reports READY it has caught exactly the bug it exists for.
func TestMustProduceBlocked(t *testing.T) {
	// Control: without anything owed, the identical prerequisites reach READY.
	control := projectTask(t, blockedOverReadyLedger(t), newID("TSKB"))
	if control.Status != StatusReady {
		t.Fatalf("control: status = %q, want READY (reasons %v)", control.Status, reasonKinds(control))
	}
	if len(control.Prerequisites) != 1 || control.Prerequisites[0].Truth != TruthTrue {
		t.Fatalf("control: prerequisite is %+v, want TRUE", control.Prerequisites)
	}

	l := blockedOverReadyLedger(t)
	l.add(t, &model.TaskStart{Task: ref(newID("TSKB"), 1), Actor: model.Actor{ID: "lane-b"}, AttemptID: newID("ATTB")})
	l.add(t, &model.AttemptTerminal{
		Task: ref(newID("TSKB"), 1), AttemptID: newID("ATTB"),
		Outcome:            model.AttemptRunnerDied,
		Reason:             "the observer died with the runner, so the terminal state is unknown",
		NextAction:         "reconcile the observed outcome against the ledger",
		DeliveryRefs:       []model.ArtifactRef{},
		ReconciliationOwed: true,
	})

	p := projectTask(t, l, newID("TSKB"))
	if p.Status != StatusBlocked {
		t.Fatalf("status = %q, want BLOCKED: every prerequisite is TRUE but reconciliation is owed", p.Status)
	}
	if !hasReason(p, ReasonReconciliation) {
		t.Fatalf("BLOCKED carries no reconciliation reason: %v", reasonKinds(p))
	}
	// The prerequisites really are all TRUE, so the status came from the
	// ordering rather than from a prerequisite quietly failing.
	for _, r := range p.Prerequisites {
		if !r.Satisfied() {
			t.Fatalf("prerequisite %d is %s, which would make this test prove nothing", r.Index, r.Truth)
		}
	}
	if len(p.WaitingActors) == 0 {
		t.Fatal("BLOCKED names no waiting actor")
	}
}

// TestReconciliationIsDischargedByAnAdmittedClear proves the reconciliation
// rule is a real gate and not a permanent block.
func TestReconciliationIsDischargedByAnAdmittedClear(t *testing.T) {
	l := blockedOverReadyLedger(t)
	l.add(t, &model.TaskStart{Task: ref(newID("TSKB"), 1), Actor: model.Actor{ID: "lane-b"}, AttemptID: newID("ATTB")})
	l.add(t, &model.AttemptTerminal{
		Task: ref(newID("TSKB"), 1), AttemptID: newID("ATTB"),
		Outcome: model.AttemptRunnerDied, Reason: "the observer died", NextAction: "reconcile",
		DeliveryRefs: []model.ArtifactRef{}, ReconciliationOwed: true,
	})
	if p := projectTask(t, l, newID("TSKB")); p.Status != StatusBlocked {
		t.Fatalf("control: status = %q, want BLOCKED", p.Status)
	}

	l.add(t, &model.BlockerHold{
		Task: ref(newID("TSKB"), 1), BlockerID: newID("HDB1"),
		Reason: model.BlockerReconciliation, Actor: model.Actor{ID: "coordinator"},
		Criterion: "the observed process outcome is established",
	})
	l.add(t, &model.BlockerClear{
		Task: ref(newID("TSKB"), 1), BlockerID: newID("HDB1"),
		HoldRef:          model.BlockerRef{Task: ref(newID("TSKB"), 1), BlockerID: newID("HDB1")},
		ResolvingWitness: blobRef("reconciliation-evidence"),
	})

	p := projectTask(t, l, newID("TSKB"))
	if p.Status != StatusReady {
		t.Fatalf("status = %q, want READY after reconciliation cleared (reasons %v)", p.Status, reasonKinds(p))
	}
}

// TestAnOpenHoldOfAnyReasonBlocks walks all four typed reasons, each with its
// cleared control, so no reason is silently ignored.
func TestAnOpenHoldOfAnyReasonBlocks(t *testing.T) {
	reasons := []model.BlockerReason{
		model.BlockerPrerequisite,
		model.BlockerAwaitingAcceptance,
		model.BlockerResume,
		model.BlockerReconciliation,
	}
	for _, reason := range reasons {
		t.Run(string(reason), func(t *testing.T) {
			l := newLedger()
			l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
			if p := projectTask(t, l, newID("TSKA")); p.Status != StatusReady {
				t.Fatalf("control: status = %q, want READY", p.Status)
			}
			l.add(t, &model.BlockerHold{
				Task: ref(newID("TSKA"), 1), BlockerID: newID("HDA1"),
				Reason: reason, Actor: model.Actor{ID: "owner"},
				Criterion: "the named condition holds",
			})
			p := projectTask(t, l, newID("TSKA"))
			if p.Status != StatusBlocked {
				t.Fatalf("status = %q, want BLOCKED", p.Status)
			}
			if !hasReason(p, string(reason)) {
				t.Fatalf("reasons %v do not carry %q", reasonKinds(p), reason)
			}
			if len(p.WaitingActors) != 1 || p.WaitingActors[0].ID != "owner" {
				t.Fatalf("waiting actors = %+v", p.WaitingActors)
			}

			l.add(t, &model.BlockerClear{
				Task: ref(newID("TSKA"), 1), BlockerID: newID("HDA1"),
				HoldRef:          model.BlockerRef{Task: ref(newID("TSKA"), 1), BlockerID: newID("HDA1")},
				ResolvingWitness: blobRef("witness"),
			})
			if p := projectTask(t, l, newID("TSKA")); p.Status != StatusReady {
				t.Fatalf("after clearing, status = %q, want READY (reasons %v)", p.Status, reasonKinds(p))
			}
		})
	}
}

// ---- attempts -------------------------------------------------------------

func TestMultipleAttemptsAreAllRetained(t *testing.T) {
	l := newLedger()
	l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
	l.add(t, &model.TaskStart{Task: ref(newID("TSKA"), 1), Actor: model.Actor{ID: "lane-a"}, AttemptID: newID("ATTA")})
	l.add(t, &model.AttemptTerminal{
		Task: ref(newID("TSKA"), 1), AttemptID: newID("ATTA"),
		Outcome: model.AttemptHarnessBroken, Reason: "the fixture harness does not build",
		NextAction: "repair the harness and retry", DeliveryRefs: []model.ArtifactRef{},
	})
	// A second start is legitimate once the first attempt has its receipt.
	l.add(t, &model.TaskStart{Task: ref(newID("TSKA"), 1), Actor: model.Actor{ID: "lane-a"}, AttemptID: newID("ATTB")})

	p := projectTask(t, l, newID("TSKA"))
	if p.Status != StatusInFlight {
		t.Fatalf("status = %q, want IN FLIGHT", p.Status)
	}
	if len(p.Attempts) != 2 {
		t.Fatalf("attempts = %d, want both retained", len(p.Attempts))
	}
	if p.Attempts[0].Terminal == nil || p.Attempts[0].Terminal.Outcome != model.AttemptHarnessBroken {
		t.Fatal("the first attempt lost its honest non-success receipt")
	}
	if !p.Attempts[1].Live() {
		t.Fatal("the second attempt is not live")
	}
	if p.Attempts[0].Key.Attempt != newID("ATTA") || p.Attempts[1].Key.Attempt != newID("ATTB") {
		t.Fatal("attempts are not in ledger order")
	}
}

// TestTakeoverDoesNotEraseTheOldAttempt is the invariant takeover exists for.
// The prior attempt keeps no terminal receipt, so the task stays IN FLIGHT and
// the abandoned attempt remains visible instead of being tidied away.
func TestTakeoverDoesNotEraseTheOldAttempt(t *testing.T) {
	l := goodLedger(t) // TSKA with live attempt ATTA held by lane-a
	l.add(t, &model.TaskTakeover{
		Task: ref(newID("TSKA"), 1), Actor: model.Actor{ID: "lane-b"},
		AttemptID: newID("ATTB"), PriorAttemptID: newID("ATTA"),
		StoppedConfirmationRef: blobRef("lane-a-stopped"),
	})

	p := projectTask(t, l, newID("TSKA"))
	if p.Status != StatusInFlight {
		t.Fatalf("status = %q, want IN FLIGHT", p.Status)
	}
	if len(p.Attempts) != 2 {
		t.Fatalf("attempts = %d, want the prior attempt retained", len(p.Attempts))
	}
	prior := p.Attempts[0]
	if prior.Key.Attempt != newID("ATTA") || prior.Terminal != nil {
		t.Fatal("takeover wrote a terminal receipt on the prior holder's behalf")
	}
	if len(p.LiveAttempts) != 2 {
		t.Fatalf("live attempts = %d, want both the abandoned and the new one visible", len(p.LiveAttempts))
	}
	taken := p.Attempts[1]
	if !taken.Takeover || taken.PriorAttempt != newID("ATTA") || taken.Actor.ID != "lane-b" {
		t.Fatalf("takeover attempt = %+v", taken)
	}
}

// TestASecondStartOverALiveAttemptIsRefused keeps the takeover path the only
// way a second writer enters a task, which is what preserves the record that
// the first one was displaced.
func TestASecondStartOverALiveAttemptIsRefused(t *testing.T) {
	control := goodLedger(t)
	control.add(t, &model.AttemptTerminal{
		Task: ref(newID("TSKA"), 1), AttemptID: newID("ATTA"),
		Outcome: model.AttemptStopped, Reason: "stopped cleanly", NextAction: "restart",
		DeliveryRefs: []model.ArtifactRef{},
	})
	control.add(t, &model.TaskStart{Task: ref(newID("TSKA"), 1), Actor: model.Actor{ID: "lane-b"}, AttemptID: newID("ATTB")})
	mustReplay(t, control.bundles())

	l := goodLedger(t)
	l.add(t, &model.TaskStart{Task: ref(newID("TSKA"), 1), Actor: model.Actor{ID: "lane-b"}, AttemptID: newID("ATTB")})
	_, err := Replay(l.bundles())
	wantFault(t, err, CodeInvalidTransition)
}

// ---- terminal without closure ---------------------------------------------

// TestTerminalSuccessWithoutClosureIsAwaitingAcceptance holds the line that
// success closes the ATTEMPT, not the obligation. A task that reads READY or
// CLOSED here is a task nobody ever accepts.
func TestTerminalSuccessWithoutClosureIsAwaitingAcceptance(t *testing.T) {
	l := newLedger()
	l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
	l.add(t, &model.TaskStart{Task: ref(newID("TSKA"), 1), Actor: model.Actor{ID: "lane-a"}, AttemptID: newID("ATTA")})
	l.add(t, &model.AttemptTerminal{
		Task: ref(newID("TSKA"), 1), AttemptID: newID("ATTA"),
		Outcome: model.AttemptSuccess, Reason: "the unit is built and checked",
		NextAction: "coordinator accepts", DeliveryRefs: []model.ArtifactRef{blobRef("delivery")},
	})

	p := projectTask(t, l, newID("TSKA"))
	if p.Status != StatusBlocked {
		t.Fatalf("status = %q, want BLOCKED awaiting acceptance", p.Status)
	}
	if !hasReason(p, ReasonAwaitingAcceptance) {
		t.Fatalf("reasons = %v, want an awaiting-acceptance queue entry", reasonKinds(p))
	}

	// The control: the authorised closure with applicable witnesses closes it.
	l.add(t, closeSuccess(newID("TSKA"), 1))
	closed := projectTask(t, l, newID("TSKA"))
	if closed.Status != StatusClosed || closed.Outcome != model.ClosureSuccess {
		t.Fatalf("after closure: status = %q outcome = %q", closed.Status, closed.Outcome)
	}
}

// TestHonestNonSuccessStopsTheAttemptWithoutOwingAcceptance walks the outcomes
// that are not success. None of them owes acceptance, and none of them closes
// the task either.
func TestHonestNonSuccessStopsTheAttemptWithoutOwingAcceptance(t *testing.T) {
	outcomes := []model.AttemptOutcome{
		model.AttemptStopped,
		model.AttemptRefused,
		model.AttemptNoReading,
		model.AttemptMeasurementImpossible,
		model.AttemptHarnessBroken,
	}
	for _, outcome := range outcomes {
		t.Run(string(outcome), func(t *testing.T) {
			l := newLedger()
			l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
			l.add(t, &model.TaskStart{Task: ref(newID("TSKA"), 1), Actor: model.Actor{ID: "lane-a"}, AttemptID: newID("ATTA")})
			l.add(t, &model.AttemptTerminal{
				Task: ref(newID("TSKA"), 1), AttemptID: newID("ATTA"),
				Outcome: outcome, Reason: "an honest stop with no measurement invented",
				NextAction: "reassign", DeliveryRefs: []model.ArtifactRef{},
			})
			p := projectTask(t, l, newID("TSKA"))
			if p.Status != StatusReady {
				t.Fatalf("status = %q, want READY (reasons %v)", p.Status, reasonKinds(p))
			}
		})
	}
}

// TestCommitsDeniedIsCarriedNotHidden keeps the uncommitted delivery condition
// visible. It is a reported condition on the attempt rather than an obligation
// on the task, because a commit is not the task's to make.
func TestCommitsDeniedIsCarriedNotHidden(t *testing.T) {
	l := newLedger()
	l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
	l.add(t, &model.TaskStart{Task: ref(newID("TSKA"), 1), Actor: model.Actor{ID: "lane-a"}, AttemptID: newID("ATTA")})
	l.add(t, &model.AttemptTerminal{
		Task: ref(newID("TSKA"), 1), AttemptID: newID("ATTA"),
		Outcome: model.AttemptStopped, Reason: "the durable ledger is written, the commit is not",
		NextAction: "coordinator commits in the next window", DeliveryRefs: []model.ArtifactRef{blobRef("delivery")},
		CommitsDenied: true,
	})
	p := projectTask(t, l, newID("TSKA"))
	if !p.CommitsDenied {
		t.Fatal("the commits-denied facet did not reach the projection")
	}
}

// ---- closure witnesses ----------------------------------------------------

// TestStaleAcceptanceWitnessDoesNotClose is the stale-witness fixture. The
// closure cites criterion revision 1 while the task now carries revision 2, so
// an old sign-off would be certifying a contract that has since changed.
func TestStaleAcceptanceWitnessDoesNotClose(t *testing.T) {
	build := func(witnessRevision model.Revision) *ledgerBuilder {
		l := newLedger()
		l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
		l.add(t, &model.TaskAmend{
			Provenance: provenance("coordinator"), Target: ref(newID("TSKA"), 1), ExpectedRevision: 1,
			Replacement: taskSpec(withCriteria(model.AcceptanceCriterion{
				ID: newID("ACCA"), Revision: 2, Criterion: "the acceptance bar was raised",
			})),
		})
		l.add(t, &model.TaskStart{Task: ref(newID("TSKA"), 2), Actor: model.Actor{ID: "lane-a"}, AttemptID: newID("ATTA")})
		l.add(t, &model.AttemptTerminal{
			Task: ref(newID("TSKA"), 2), AttemptID: newID("ATTA"),
			Outcome: model.AttemptSuccess, Reason: "done", NextAction: "accept",
			DeliveryRefs: []model.ArtifactRef{blobRef("delivery")},
		})
		l.add(t, &model.TaskClose{
			Task: ref(newID("TSKA"), 2), Outcome: model.ClosureSuccess,
			Authority: closeAuthority(),
			AcceptanceWitnessRefs: []model.AcceptanceWitness{
				{CriterionID: newID("ACCA"), CriterionRevision: witnessRevision, WitnessRef: blobRef("acceptance")},
			},
			DeliveryWitnessRefs: []model.ArtifactRef{blobRef("delivery")},
		})
		return l
	}

	// Control: a witness at the current criterion revision closes the task.
	if p := projectTask(t, build(2), newID("TSKA")); p.Status != StatusClosed || p.Outcome != model.ClosureSuccess {
		t.Fatalf("control: status = %q outcome = %q (reasons %v)", p.Status, p.Outcome, reasonKinds(p))
	}

	p := projectTask(t, build(1), newID("TSKA"))
	if p.Status != StatusBlocked {
		t.Fatalf("status = %q, want BLOCKED on the stale witness", p.Status)
	}
	if !hasReason(p, ReasonAwaitingAcceptance) {
		t.Fatalf("reasons = %v", reasonKinds(p))
	}
	if p.Closure == nil {
		t.Fatal("the ineffective closure was hidden instead of carried")
	}
	if p.Closure.Outcome != model.ClosureSuccess {
		t.Fatalf("carried closure = %+v", p.Closure)
	}
}

func TestSuccessClosureNeedsADeliveryWitness(t *testing.T) {
	// Control first.
	if p := projectTask(t, closedTaskLedger(t), newID("TSKA")); p.Status != StatusClosed {
		t.Fatalf("control: status = %q", p.Status)
	}

	l := newLedger()
	l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
	l.add(t, &model.TaskClose{
		Task: ref(newID("TSKA"), 1), Outcome: model.ClosureSuccess,
		Authority: closeAuthority(),
		AcceptanceWitnessRefs: []model.AcceptanceWitness{
			{CriterionID: newID("ACCA"), CriterionRevision: 1, WitnessRef: blobRef("acceptance")},
		},
		DeliveryWitnessRefs: []model.ArtifactRef{},
	})
	p := projectTask(t, l, newID("TSKA"))
	if p.Status != StatusBlocked {
		t.Fatalf("status = %q, want BLOCKED with no delivery witness", p.Status)
	}
}

// TestAClosureWithALiveAttemptDoesNotClose evaluates predicate 1's second
// conjunct. The closure is recorded and visible, and the task still reads
// IN FLIGHT because a writer is still in it.
func TestAClosureWithALiveAttemptDoesNotClose(t *testing.T) {
	l := goodLedger(t) // live attempt ATTA
	l.add(t, closeSuccess(newID("TSKA"), 1))

	p := projectTask(t, l, newID("TSKA"))
	if p.Status != StatusInFlight {
		t.Fatalf("status = %q, want IN FLIGHT while an attempt has no receipt", p.Status)
	}
	if p.Closure == nil {
		t.Fatal("the closure was dropped rather than carried")
	}
}

// ---- dependencies ---------------------------------------------------------

func TestDependencyRequiresCurrentAndRequiredRevisionWitnesses(t *testing.T) {
	cases := []struct {
		name      string
		witnesses []model.Revision
		required  model.Revision
		producer  TaskStatus
		consumer  TaskStatus
		truth     Truth
	}{
		{"current", []model.Revision{2}, 2, StatusClosed, StatusReady, TruthTrue},
		{"historical only", []model.Revision{1}, 1, StatusBlocked, StatusBlocked, TruthFalse},
		{"required missing", []model.Revision{2}, 1, StatusClosed, StatusBlocked, TruthFalse},
		{"both revisions", []model.Revision{1, 2}, 1, StatusClosed, StatusReady, TruthTrue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := newLedger()
			l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
			l.add(t, &model.TaskAmend{
				Provenance: provenance("coordinator"), Target: ref(newID("TSKA"), 1), ExpectedRevision: 1,
				Replacement: taskSpec(withCriteria(model.AcceptanceCriterion{
					ID: newID("ACCA"), Revision: 2, Criterion: "the acceptance bar was raised",
				})),
			})
			closure := closeSuccess(newID("TSKA"), 2)
			closure.AcceptanceWitnessRefs = nil
			for _, rev := range tc.witnesses {
				closure.AcceptanceWitnessRefs = append(closure.AcceptanceWitnessRefs, model.AcceptanceWitness{
					CriterionID: newID("ACCA"), CriterionRevision: rev, WitnessRef: blobRef("acceptance"),
				})
			}
			l.add(t, closure, &model.TaskCreate{
				Provenance: provenance("lane-b"), ID: newID("TSKB"),
				Spec: taskSpec(withPrerequisite("task-success", ref(newID("TSKA"), tc.required), "forbid", nil)),
			})
			s := mustReplay(t, l.bundles())
			producer, _ := s.Task(Ident{Project: testProject, ID: newID("TSKA")})
			consumer, _ := s.Task(Ident{Project: testProject, ID: newID("TSKB")})
			if producer.Status != tc.producer || consumer.Status != tc.consumer || consumer.Prerequisites[0].Truth != tc.truth {
				t.Fatalf("producer %s, consumer %s (%s); want %s, %s (%s)",
					producer.Status, consumer.Status, consumer.Prerequisites[0].Truth, tc.producer, tc.consumer, tc.truth)
			}
		})
	}
}

// TestDependencyRuleRejectsEveryNonSuccessClosure is the closed-is-not-success
// fixture. Cancelled, withdrawn and waived are all CLOSED, and none of them may
// satisfy an ordinary success dependency.
func TestDependencyRuleRejectsEveryNonSuccessClosure(t *testing.T) {
	build := func(outcome model.ClosureOutcome, witnesses []model.AcceptanceWitness, delivery []model.ArtifactRef) *ledgerBuilder {
		l := newLedger()
		l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
		l.add(t, &model.TaskClose{
			Task: ref(newID("TSKA"), 1), Outcome: outcome,
			Authority: closeAuthority(), AcceptanceWitnessRefs: witnesses, DeliveryWitnessRefs: delivery,
		})
		l.add(t, &model.TaskCreate{
			Provenance: provenance("lane-b"), ID: newID("TSKB"),
			Spec: taskSpec(withPrerequisite("task-success", ref(newID("TSKA"), 1), "forbid", nil)),
		})
		return l
	}
	witnesses := []model.AcceptanceWitness{
		{CriterionID: newID("ACCA"), CriterionRevision: 1, WitnessRef: blobRef("acceptance")},
	}
	delivery := []model.ArtifactRef{blobRef("delivery")}

	// Control: success with applicable witnesses does satisfy the dependency.
	if p := projectTask(t, build(model.ClosureSuccess, witnesses, delivery), newID("TSKB")); p.Status != StatusReady {
		t.Fatalf("control: status = %q (reasons %v)", p.Status, reasonKinds(p))
	}

	for _, outcome := range []model.ClosureOutcome{model.ClosureCancelled, model.ClosureWithdrawn, model.ClosureWaived} {
		t.Run(string(outcome), func(t *testing.T) {
			p := projectTask(t, build(outcome, witnesses, delivery), newID("TSKB"))
			if p.Status != StatusBlocked {
				t.Fatalf("a %s dependency satisfied a success prerequisite: status = %q", outcome, p.Status)
			}
			if p.Prerequisites[0].Truth != TruthFalse {
				t.Fatalf("prerequisite truth = %q, want FALSE", p.Prerequisites[0].Truth)
			}
		})
	}
}

// TestUnresolvedDependenciesAreUnknownNeverTrue covers every way a dependency
// can fail to resolve. UNKNOWN blocks, which is the whole point: a dependency
// nobody established is not a dependency that was met.
func TestUnresolvedDependenciesAreUnknownNeverTrue(t *testing.T) {
	cases := []struct {
		name  string
		build func(t *testing.T) *ledgerBuilder
	}{
		{"cross-project target", func(t *testing.T) *ledgerBuilder {
			l := newLedger()
			l.add(t, &model.TaskCreate{
				Provenance: provenance("lane-b"), ID: newID("TSKB"),
				Spec: taskSpec(withPrerequisite("task-success",
					model.RecordRef{Project: "datum/other", RecordID: newID("TSKZ"), Revision: 1}, "forbid", nil)),
			})
			return l
		}},
		{"cross-project claim proof", func(t *testing.T) *ledgerBuilder {
			l := newLedger()
			l.add(t, &model.TaskCreate{
				Provenance: provenance("lane-b"), ID: newID("TSKB"),
				Spec: taskSpec(withPrerequisite("claim-proof",
					model.RecordRef{Project: "datum/other", RecordID: newID("CMA1"), Revision: 1}, "forbid", nil)),
			})
			return l
		}},
		{"cross-project decision approval", func(t *testing.T) *ledgerBuilder {
			l := newLedger()
			l.add(t, &model.TaskCreate{
				Provenance: provenance("lane-b"), ID: newID("TSKB"),
				Spec: taskSpec(withPrerequisite("decision-approved",
					model.RecordRef{Project: "datum/other", RecordID: newID("DCS1"), Revision: 1}, "forbid", nil)),
			})
			return l
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := projectTask(t, tc.build(t), newID("TSKB"))
			if p.Prerequisites[0].Truth != TruthUnknown {
				t.Fatalf("truth = %q, want UNKNOWN", p.Prerequisites[0].Truth)
			}
			if p.Status != StatusBlocked {
				t.Fatalf("status = %q, want BLOCKED on an unresolved dependency", p.Status)
			}
			if p.Prerequisites[0].Detail == "" {
				t.Fatal("UNKNOWN carries no stated reason")
			}
		})
	}
}

func TestAnEmptyPrerequisiteSetIsVacuouslyTrue(t *testing.T) {
	l := newLedger()
	l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
	p := projectTask(t, l, newID("TSKA"))
	if len(p.Prerequisites) != 0 {
		t.Fatalf("fixture has %d prerequisites", len(p.Prerequisites))
	}
	if p.Status != StatusReady {
		t.Fatalf("status = %q, want READY", p.Status)
	}
}

// ---- waivers --------------------------------------------------------------

// TestWaiverCountsOnlyWhereTheConsumerPermitsIt is the waiver-exclusion
// fixture. The same unmet dependency blocks under "forbid" and is carried under
// "allow-with-authority", and the waiver stays visible either way rather than
// being folded into a TRUE nobody can audit.
func TestWaiverCountsOnlyWhereTheConsumerPermitsIt(t *testing.T) {
	build := func(policy string, auth *model.Authority) *ledgerBuilder {
		l := newLedger()
		// TSKA is closed CANCELLED, so the dependency is definitely FALSE.
		l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
		l.add(t, &model.TaskClose{
			Task: ref(newID("TSKA"), 1), Outcome: model.ClosureCancelled,
			Authority:             closeAuthority(),
			AcceptanceWitnessRefs: []model.AcceptanceWitness{},
			DeliveryWitnessRefs:   []model.ArtifactRef{},
		})
		l.add(t, &model.TaskCreate{
			Provenance: provenance("lane-b"), ID: newID("TSKB"),
			Spec: taskSpec(withPrerequisite("task-success", ref(newID("TSKA"), 1), policy, auth)),
		})
		return l
	}

	forbidden := projectTask(t, build("forbid", nil), newID("TSKB"))
	if forbidden.Status != StatusBlocked {
		t.Fatalf("forbid: status = %q, want BLOCKED", forbidden.Status)
	}
	if forbidden.Prerequisites[0].Waived {
		t.Fatal("a forbid policy produced a waiver")
	}

	auth := rulingAuthority("owner")
	waived := projectTask(t, build("allow-with-authority", &auth), newID("TSKB"))
	if waived.Status != StatusReady {
		t.Fatalf("allow-with-authority: status = %q, want READY (reasons %v)", waived.Status, reasonKinds(waived))
	}
	r := waived.Prerequisites[0]
	if !r.Waived || r.Waiver == nil || r.Waiver.Actor.ID != "owner" {
		t.Fatalf("waiver is not carried with its authority: %+v", r)
	}
	// The underlying truth is still FALSE. A waiver excuses a requirement. It
	// does not manufacture the fact the requirement asked for.
	if r.Truth != TruthFalse {
		t.Fatalf("waived prerequisite reports truth %q, want FALSE", r.Truth)
	}
}

// TestAWaiverDoesNotWaiveAnyoneElsesDependency confirms the waiver is scoped to
// the consumer that declared it.
func TestAWaiverDoesNotWaiveAnyoneElsesDependency(t *testing.T) {
	auth := rulingAuthority("owner")
	l := newLedger()
	l.add(t, &model.TaskCreate{Provenance: provenance("lane-a"), ID: newID("TSKA"), Spec: taskSpec()})
	l.add(t, &model.TaskClose{
		Task: ref(newID("TSKA"), 1), Outcome: model.ClosureCancelled,
		Authority:             closeAuthority(),
		AcceptanceWitnessRefs: []model.AcceptanceWitness{},
		DeliveryWitnessRefs:   []model.ArtifactRef{},
	})
	l.add(t, &model.TaskCreate{
		Provenance: provenance("lane-b"), ID: newID("TSKB"),
		Spec: taskSpec(withPrerequisite("task-success", ref(newID("TSKA"), 1), "allow-with-authority", &auth)),
	})
	l.add(t, &model.TaskCreate{
		Provenance: provenance("lane-c"), ID: newID("TSKC"),
		Spec: taskSpec(withPrerequisite("task-success", ref(newID("TSKA"), 1), "forbid", nil)),
	})

	s := mustReplay(t, l.bundles())
	b, _ := s.Task(Ident{Project: testProject, ID: newID("TSKB")})
	c, _ := s.Task(Ident{Project: testProject, ID: newID("TSKC")})
	if b.Status != StatusReady {
		t.Fatalf("the waiving consumer is %q, want READY", b.Status)
	}
	if c.Status != StatusBlocked {
		t.Fatalf("the neighbouring consumer inherited a waiver: status = %q", c.Status)
	}
}

// ---- reverse references ---------------------------------------------------

func TestReverseReferencesAreRecorded(t *testing.T) {
	l := goodLedger(t)
	s := mustReplay(t, l.bundles())
	refs := s.RecordReferrers(ref(newID("TSKA"), 1))
	if len(refs) != 1 || refs[0].Type != "task.start" || refs[0].Origin.Sequence != 2 {
		t.Fatalf("reverse edges = %+v", refs)
	}
}

// TestSnapshotsDoNotShareMutableTails guards the clone. Two forks of one
// snapshot must not see each other's reverse edges, because one fork silently
// altering another is the failure mode a shared backing array produces.
func TestSnapshotsDoNotShareMutableTails(t *testing.T) {
	l := goodLedger(t)
	base := mustReplay(t, l.bundles())

	fork := l.add(t, &model.BlockerHold{
		Task: ref(newID("TSKA"), 1), BlockerID: newID("HDA1"),
		Reason: model.BlockerResume, Actor: model.Actor{ID: "owner"},
		Criterion: "the harness is repaired",
	})
	forked, err := Apply(base, fork)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := len(base.RecordReferrers(ref(newID("TSKA"), 1))); got != 1 {
		t.Fatalf("the base snapshot gained %d reverse edges from a fork", got-1)
	}
	if got := len(forked.RecordReferrers(ref(newID("TSKA"), 1))); got != 2 {
		t.Fatalf("the fork has %d reverse edges, want 2", got)
	}
	if len(base.Blockers(Ident{Project: testProject, ID: newID("TSKA")})) != 0 {
		t.Fatal("the base snapshot gained the fork's blocker")
	}
}
