package query

// What the removed INSTRUMENTS, STATE, NOW and TODO reads guaranteed, asserted
// in the views that absorbed them (R19): show --kind instrument, show --kind
// claim and decision, and todo. Run, closure, continue and real-ledger tests
// have their own files.

import (
	"strings"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
)

func TestInstrumentsShowEveryFieldAndUnknownValidationFirst(t *testing.T) {
	p := testProject(t)
	presetWorld(t, p)
	appendEvents(t, p, 106, &model.TrustWithdraw{Instrument: testRef(10, 1), Scope: testScope(), RevalidationCondition: "rerun the known-answer suite"})
	a := view_(t, p, ViewRequest{View: "show", Kind: "instrument"}).(*ShowAnswer)
	assertViewHonest(t, a)
	views := []InstrumentView{}
	for _, d := range a.Records {
		views = append(views, *d.Instrument)
	}
	if len(views) != 2 || views[0].Ref != testRef(10, 1) || views[1].Ref != testRef(11, 1) {
		t.Fatalf("expected both current instruments in identity order, got %+v", views)
	}
	good, bad := views[0], views[1]
	if good.Validation.State != "KNOWN" || good.Validation.Version != "1" || good.Validation.Ref == nil {
		t.Fatalf("recorded validation must read KNOWN with its ref and version, got %+v", good.Validation)
	}
	if bad.Validation.State != "UNKNOWN" || bad.Validation.Reason != "never checked against a case with a known answer" || bad.Validation.Ref != nil {
		t.Fatalf("unrecorded validation must read UNKNOWN with its own reason, got %+v", bad.Validation)
	}
	if bad.BlindTo != "generated code" || bad.NotAnswered == "" || bad.QuestionAnswered == "" || len(bad.DangerousDefaults) != 1 ||
		len(bad.ConfigSurface) != 1 || bad.ValidRange == "" || bad.ImplementationRef.Kind == "" || bad.Author != (model.Actor{ID: "lane-a"}) {
		t.Fatalf("every declared instrument field must be shown, got %+v", bad)
	}
	if good.Trust != reduce.TruthFalse || len(good.Withdrawals) != 1 || bad.Trust != reduce.TruthUnknown {
		t.Fatalf("withdrawn trust must read FALSE and unvalidated trust UNKNOWN, got %s/%s", good.Trust, bad.Trust)
	}
	kinds := map[string]model.RecordRef{}
	for _, note := range a.Attention {
		kinds[note.Kind] = note.Ref
	}
	if kinds["instrument-validation-unknown"] != testRef(11, 1) || kinds["instrument-trust-not-active"] != testRef(10, 1) || len(a.Attention) != 2 {
		t.Fatalf("UNKNOWN validation and withdrawn trust must each be raised under attention, got %+v", a.Attention)
	}
}

func TestClaimsNeverReadAsEstablishedShortOfProof(t *testing.T) {
	p := testProject(t)
	presetWorld(t, p)
	{
		a := view_(t, p, ViewRequest{View: "show", Kind: "claim"}).(*ShowAnswer)
		assertViewHonest(t, a)
		claims, observed := []ClaimView{}, [][]model.ID{}
		for _, d := range a.Records {
			claims, observed = append(claims, d.Claim.ClaimView), append(observed, d.Claim.Observations)
		}
		runs := map[model.ID]RunDetail{}
		for _, r := range *a.Runs {
			runs[r.Invocation] = r
		}
		if len(claims) != 3 {
			t.Fatalf("show --kind claim must list every claim at every status, got %d", len(claims))
		}
		unmeasured, measured, proven := claims[0], claims[1], claims[2]
		if unmeasured.Status != reduce.StatusUnmeasured || len(unmeasured.ExternalRefs) != 1 || unmeasured.ExternalRefs[0].Tag != "VERIFIED" {
			t.Fatalf("a VERIFIED external tag must not lift a claim from UNMEASURED, got %+v", unmeasured)
		}
		for _, c := range []ClaimView{unmeasured, measured} {
			if c.CurrentSupport == reduce.TruthTrue || len(c.Missing) == 0 || c.Missing[len(c.Missing)-1] != "admitted proof" ||
				!strings.Contains(c.Standing, "not established") {
				t.Fatalf("%s claim must read as not established and name what is missing, got %+v", c.Status, c)
			}
		}
		if unmeasured.Missing[0] != "local observation" || len(observed[0]) != 0 {
			t.Fatalf("an UNMEASURED claim must name the missing local observation, got %+v", unmeasured.Missing)
		}
		if measured.Status != reduce.StatusMeasured || len(observed[1]) != 1 || runs[observed[1][0]].Outcome.(model.ProcessOutcome).ExitCode == nil {
			t.Fatalf("a failed completed run is still an observation of a MEASURED claim, got %+v", measured)
		}
		if proven.Status != reduce.StatusProven || len(proven.Missing) != 0 || len(proven.Proofs) != 1 || strings.Contains(proven.Standing, "not established") ||
			!strings.Contains(proven.Standing, "revision 1") {
			t.Fatalf("PROVEN must name its exact revision and keep current support separate, got %+v", proven)
		}
		decided := []DecisionView{}
		for _, d := range view_(t, p, ViewRequest{View: "show", Kind: "decision"}).(*ShowAnswer).Records {
			if d.Decision.Status == reduce.StatusDecided {
				decided = append(decided, *d.Decision)
			}
		}
		if len(decided) != 1 || len(decided[0].Rulings) != 1 ||
			decided[0].Rulings[0].Disposition.Quote != "yes, BLOCKED wins" || decided[0].Authorizes != "" {
			t.Fatalf("show --kind decision must show the DECIDED decision with its quoted ruling, got %+v", decided)
		}
	}
}

func TestTodoShowsInFlightWorkOpenDecisionsAndItsRuns(t *testing.T) {
	p := testProject(t)
	presetWorld(t, p)
	a := todoOf(t, p)
	assertViewHonest(t, a)
	flying, open := a.InFlight, a.OpenDecisions
	if len(flying) != 1 || flying[0].Task.Status != reduce.StatusInFlight || flying[0].Task.AttemptHolders[0].Actor != (model.Actor{ID: "worker"}) {
		t.Fatalf("todo must show the IN FLIGHT task and its admitted holder, got %+v", flying)
	}
	if len(open) != 1 || open[0].Ref != testRef(30, 1) || open[0].Decision.Authorizes == "" || open[0].Decision.WaitingActor != (model.Actor{ID: "owner"}) {
		t.Fatalf("todo must show the OPEN decision, its waiting actor, and that it authorises nothing, got %+v", open)
	}
	if len(*flying[0].Runs) != 3 {
		t.Fatalf("todo must show every run of in-flight work, got %d", len(*flying[0].Runs))
	}
	show := view_(t, p, ViewRequest{View: "show"}).(*ShowAnswer)
	if show.Summary.Tasks == nil || (*show.Summary.Tasks)["CLOSED"] != 0 || len(*show.Runs) != 3 {
		t.Fatalf("bare show must count closed tasks and list every run, got %+v", show.Summary)
	}
}

func TestTodoLimitNeverHidesABlocker(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	appendEvents(t, p, 101, testTask(2), testTask(3), testTask(4), testTask(5),
		&model.BlockerHold{Task: testRef(2, 1), BlockerID: testID(80), Reason: model.BlockerResume,
			Actor: model.Actor{ID: "owner"}, Criterion: "resume is authorized"},
		&model.TaskStart{Task: testRef(5, 1), AttemptID: testID(71), Actor: model.Actor{ID: "worker"}},
		&model.DecisionOpen{ID: testID(30), Provenance: prov("lane-c"), Spec: decisionSpec()})
	appendEvents(t, p, 102, &model.AttemptTerminal{Task: testRef(5, 1), AttemptID: testID(71), Outcome: model.AttemptSuccess,
		Reason: "done", NextAction: "owner accepts or rejects", DeliveryRefs: []model.ArtifactRef{testArtifact()}})
	for _, limit := range []int{0, 1, 2} {
		a := view_(t, p, ViewRequest{View: "todo", Limit: limit}).(*TodoAnswer)
		assertViewHonest(t, a)
		blocked, awaiting, ready := a.Blocked, a.AwaitingAcceptance, a.Ready
		if len(blocked) != 1 || blocked[0].Fact.Key.ID != testID(2) || len(blocked[0].Task.Blockers) != 1 {
			t.Fatalf("limit %d hid the blocked task or its typed blocker: %+v", limit, blocked)
		}
		if len(awaiting) != 1 || awaiting[0].Fact.Key.ID != testID(5) || len(a.OpenDecisions) != 1 {
			t.Fatalf("limit %d hid the awaiting-acceptance queue or the open decision", limit)
		}
		want := 3
		if limit > 0 && limit < 3 {
			want = limit
		}
		if report := a.Omitted["ready"]; len(ready) != want || report.Offered != 3 || report.Omitted != 3-want {
			t.Fatalf("limit %d must cut only READY work and say how much, got %d kept, report %+v", limit, len(ready), report)
		}
	}
}

func TestAbsentRecordIsAWatermarkedUnknown(t *testing.T) {
	p := testProject(t)
	presetWorld(t, p)
	for _, view := range []string{"show", "continue", "history"} {
		a, err := ReadView(p, ViewRequest{View: view, ID: testID(999)})
		if err != nil || a.Header().Result != "UNKNOWN" || a.Header().Reason == "" || a.Header().Watermark.Sequence != 6 {
			t.Fatalf("%s of an absent record must be a watermarked UNKNOWN success, got %+v, %v", view, a, err)
		}
	}
}

// todo raises every reason owed on a BLOCKED task itself, with its waiting
// actor, and leaves computed prerequisites (owed on another record) to the
// blocked section.
func TestTodoRaisesBlockedWorkOwedByAnActor(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	waiting := testTask(6)
	waiting.Spec.Prerequisites = []model.Prerequisite{{Kind: "task-success", Target: testRef(3, 1), WaiverPolicy: "forbid"}}
	appendEvents(t, p, 101, testTask(2), testTask(3), testTask(5), waiting,
		&model.BlockerHold{Task: testRef(2, 1), BlockerID: testID(80), Reason: model.BlockerResume,
			Actor: model.Actor{ID: "owner"}, Criterion: "resume is authorized"},
		&model.BlockerHold{Task: testRef(6, 1), BlockerID: testID(81), Reason: model.BlockerPrerequisite,
			Actor: model.Actor{ID: "infra"}, Criterion: "the build machine is back"},
		&model.TaskStart{Task: testRef(5, 1), AttemptID: testID(71), Actor: model.Actor{ID: "worker"}})
	appendEvents(t, p, 102, &model.AttemptTerminal{Task: testRef(5, 1), AttemptID: testID(71), Outcome: model.AttemptSuccess,
		Reason: "done", NextAction: "owner accepts or rejects", DeliveryRefs: []model.ArtifactRef{testArtifact()}})
	a := todoOf(t, p)
	if len(a.Blocked) != 2 || len(a.AwaitingAcceptance) != 1 {
		t.Fatalf("control: todo must hold the held and the prerequisite-blocked task, and one awaiting acceptance, got %+v", a)
	}
	assertViewHonest(t, a)
	owed := map[model.ID]Attention{}
	for _, note := range a.Attention {
		if note.Kind == "task-blocked-owed" {
			owed[note.Ref.RecordID] = note
		}
	}
	if len(owed) != 3 || len(a.Attention) != 3 {
		t.Fatalf("todo must raise exactly the two holds and the task awaiting acceptance, got %+v", a.Attention)
	}
	held, accept := owed[testID(2)], owed[testID(5)]
	if held.WaitingActor != (model.Actor{ID: "owner"}) || !strings.Contains(held.Reason, "resume hold "+string(testID(80))) || held.Ref != testRef(2, 1) {
		t.Fatalf("the hold must name its kind, id and waiting actor, got %+v", held)
	}
	if accept.WaitingActor != (model.Actor{ID: "acceptance-owner"}) || !strings.HasPrefix(accept.Reason, reduce.ReasonAwaitingAcceptance+": ") {
		t.Fatalf("awaiting acceptance must name the task's next actor, got %+v", accept)
	}
	// Task 6 has an explicit prerequisite-kind hold and a computed unmet
	// prerequisite; only the hold is owed on the task itself.
	if gated := owed[testID(6)]; gated.WaitingActor != (model.Actor{ID: "infra"}) || !strings.Contains(gated.Reason, "hold "+string(testID(81))) {
		t.Fatalf("a hold of kind prerequisite is still a hold owed by its actor, and the computed prerequisite stays in TODO; got %+v", gated)
	}
}
