package query

// Routing tests for INSTRUMENTS, STATE, NOW and TODO over a fixture ledger.
// Run, closure, continue, provider and real-ledger tests have their own files.

import (
	"strings"
	"testing"

	"datum/internal/model"
	"datum/internal/reduce"
)

func TestInstrumentsShowEveryFieldAndUnknownValidationFirst(t *testing.T) {
	p := testProject(t)
	presetWorld(t, p)
	appendEvents(t, p, 106, &model.TrustWithdraw{Instrument: testRef(10, 1), Scope: testScope(), RevalidationCondition: "rerun the known-answer suite"})
	a := presetAnswer(t, p, Request{Command: "instruments"})
	assertHonestRendering(t, a)
	views := *a.Preset.Instruments
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
	for _, note := range a.Preset.Attention {
		kinds[note.Kind] = note.Ref
	}
	if kinds["instrument-validation-unknown"] != testRef(11, 1) || kinds["instrument-trust-not-active"] != testRef(10, 1) || len(a.Preset.Attention) != 2 {
		t.Fatalf("UNKNOWN validation and withdrawn trust must each be raised under attention, got %+v", a.Preset.Attention)
	}
}

func TestClaimsNeverReadAsEstablishedShortOfProof(t *testing.T) {
	p := testProject(t)
	presetWorld(t, p)
	for _, command := range []string{"state", "context"} {
		a := presetAnswer(t, p, Request{Command: command})
		assertHonestRendering(t, a)
		claims := *a.Preset.Claims
		if len(claims) != 3 {
			t.Fatalf("%s must list every claim at every status, got %d", command, len(claims))
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
		if unmeasured.Missing[0] != "local observation" || len(unmeasured.Observations) != 0 {
			t.Fatalf("an UNMEASURED claim must name the missing local observation, got %+v", unmeasured.Missing)
		}
		if measured.Status != reduce.StatusMeasured || len(measured.Observations) != 1 || measured.Observations[0].Outcome.(model.ProcessOutcome).ExitCode == nil {
			t.Fatalf("a failed completed run is still an observation of a MEASURED claim, got %+v", measured)
		}
		if proven.Status != reduce.StatusProven || len(proven.Missing) != 0 || len(proven.Proofs) != 1 || strings.Contains(proven.Standing, "not established") ||
			!strings.Contains(proven.Standing, "revision 1") {
			t.Fatalf("PROVEN must name its exact revision and keep current support separate, got %+v", proven)
		}
		decided := *a.Preset.Decisions
		if len(decided) != 1 || decided[0].Status != reduce.StatusDecided || len(decided[0].Rulings) != 1 ||
			decided[0].Rulings[0].Disposition.Quote != "yes, BLOCKED wins" || decided[0].Authorizes != "" {
			t.Fatalf("%s must show only DECIDED decisions with their quoted ruling, got %+v", command, decided)
		}
	}
}

func TestNowShowsInFlightWorkOpenDecisionsAndItsRuns(t *testing.T) {
	p := testProject(t)
	presetWorld(t, p)
	a := presetAnswer(t, p, Request{Command: "now"})
	assertHonestRendering(t, a)
	flying, open := *a.Preset.InFlight, *a.Preset.Decisions
	if len(flying) != 1 || flying[0].Task.Status != reduce.StatusInFlight || flying[0].Task.AttemptHolders[0].Actor != (model.Actor{ID: "worker"}) {
		t.Fatalf("NOW must show the IN FLIGHT task and its admitted holder, got %+v", flying)
	}
	if len(open) != 1 || open[0].Ref != testRef(30, 1) || open[0].Authorizes == "" || open[0].WaitingActor != (model.Actor{ID: "owner"}) {
		t.Fatalf("NOW must show the OPEN decision, its waiting actor, and that it authorises nothing, got %+v", open)
	}
	if len(*a.Preset.Runs) != 3 {
		t.Fatalf("NOW must show every run of in-flight work, got %d", len(*a.Preset.Runs))
	}
	state := presetAnswer(t, p, Request{Command: "state"})
	if len(*state.Preset.Closed) != 0 || len(*state.Preset.Runs) != 3 {
		t.Fatalf("STATE must list closed tasks and every run, got %+v", state.Preset)
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
		a := presetAnswer(t, p, Request{Command: "todo", Limit: limit})
		assertHonestRendering(t, a)
		blocked, awaiting, ready := *a.Preset.Blocked, *a.Preset.AwaitingAcceptance, *a.Preset.Ready
		if len(blocked) != 1 || blocked[0].Fact.Key.ID != testID(2) || len(blocked[0].Task.Blockers) != 1 {
			t.Fatalf("limit %d hid the blocked task or its typed blocker: %+v", limit, blocked)
		}
		if len(awaiting) != 1 || awaiting[0].Fact.Key.ID != testID(5) || len(*a.Preset.Decisions) != 1 {
			t.Fatalf("limit %d hid the awaiting-acceptance queue or the open decision", limit)
		}
		want := 3
		if limit > 0 && limit < 3 {
			want = limit
		}
		if len(ready) != want || a.Preset.Limit.Offered != 3 || a.Preset.Limit.Omitted != 3-want {
			t.Fatalf("limit %d must cut only READY work and say how much, got %d kept, report %+v", limit, len(ready), a.Preset.Limit)
		}
	}
}

func TestPresetAbsentRecordIsWatermarkedUnknownAndBadRequestsFail(t *testing.T) {
	p := testProject(t)
	presetWorld(t, p)
	for _, command := range []string{"context", "continue"} {
		a, err := Read(p, Request{Command: command, ID: testID(999)})
		if err != nil || a.Result != "UNKNOWN" || a.Reason == "" || a.Watermark.Sequence != 6 {
			t.Fatalf("%s of an absent record must be a watermarked UNKNOWN success, got %+v, %v", command, a, err)
		}
	}
	for _, bad := range []Request{{Command: "continue"}, {Command: "instruments", Limit: 1}, {Command: "todo", Limit: -1},
		{Command: "now", ID: testID(1)}, {Command: "continue", ID: testID(20)}, {Command: "state", Observed: &Observation{}}} {
		if _, err := Read(p, bad); err == nil {
			t.Fatalf("request %+v must be refused", bad)
		}
	}
}
