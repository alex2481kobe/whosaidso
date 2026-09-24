package query

// The four views' own rules: the brief agrees with the JSON, every answer
// carries its watermark, todo lists each task once with in-flight work first,
// continue carries each record and run once for any kind, limits never cut
// mandatory closure, blocked work or required attention, and show --kind
// instrument keeps the validation and trust attention.

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
)

// viewsWorld is richWorld plus the task shapes todo must file: task 5 awaiting
// acceptance by its named accepter; task 6 awaiting acceptance and held; plan
// task 8 whose items are tasks 5 and 14; task 14 READY; task 15 IN FLIGHT
// with one sealed run outside its scope and one unsealed run; task 9 closed; decision 33 whose
// context names instruments 11 and 10 and a record in another project. Task 1 is
// amended to name claims 21 and 22 as context: their runs are its own runs.
func viewsWorld(t *testing.T, p store.Project) {
	t.Helper()
	richWorld(t, p)
	five, plan, nine := testTask(5), testTask(8), testTask(9)
	five.Spec.Accepter = &model.Actor{ID: "owner"}
	plan.Spec.Intent = "Plan: ship tasks 5 and 14"
	plan.Spec.Prerequisites = []model.Prerequisite{{Kind: "task-success", Target: testRef(5, 1), WaiverPolicy: "forbid"},
		{Kind: "task-success", Target: testRef(14, 1), WaiverPolicy: "forbid"}}
	decision := decisionSpec()
	decision.Scope.ContextRefs = []model.RecordRef{testRef(11, 1), testRef(10, 1), {Project: "datum/elsewhere", RecordID: testID(777), Revision: 1}}
	amended := testTask(1).Spec
	amended.ContextRefs = []model.RecordRef{testRef(21, 1), testRef(22, 1)}
	appendEvents(t, p, 119, admitted(119, &model.TaskAmend{Target: testRef(1, 1), ExpectedRevision: 1, Replacement: amended,
		Provenance: testTask(1).Provenance})...)
	appendEvents(t, p, 120, admitted(120, five, testTask(6), testTask(14), testTask(15), plan, nine,
		&model.DecisionOpen{ID: testID(33), Provenance: prov("lane-c"), Spec: decision})...)
	appendEvents(t, p, 121, admitted(121,
		&model.TaskStart{Task: testRef(5, 1), AttemptID: testID(75), Actor: model.Actor{ID: "worker"}},
		&model.TaskStart{Task: testRef(6, 1), AttemptID: testID(76), Actor: model.Actor{ID: "worker"}},
		&model.TaskStart{Task: testRef(9, 1), AttemptID: testID(79), Actor: model.Actor{ID: "lane-a"}},
		&model.TaskStart{Task: testRef(15, 1), AttemptID: testID(78), Actor: model.Actor{ID: "worker"}})...)
	outside, unsealed := envelope(53, 78, 10, 0, gitInput("cmd/whosaidso/main.go")), envelope(54, 78, 10, 0, testArtifact())
	appendEvents(t, p, 124, admitted(124, &model.InvocationStart{Envelope: outside}, &model.InvocationStart{Envelope: unsealed})...)
	appendEvents(t, p, 125, seal(outside, 0, time.Second))
	success := func(task, attempt int) model.TypedEvent {
		return &model.AttemptTerminal{Task: testRef(task, 1), AttemptID: testID(attempt), Outcome: model.AttemptSuccess, Reason: "done",
			NextAction: "accept it", DeliveryRefs: []model.ArtifactRef{testArtifact()}}
	}
	appendEvents(t, p, 122, admittedAs(122, map[int]string{0: "worker", 1: "worker"}, success(5, 75), success(6, 76), success(9, 79),
		&model.BlockerHold{Task: testRef(6, 1), BlockerID: testID(86), Reason: model.BlockerResume, Actor: model.Actor{ID: "owner"},
			Criterion: "the owner re-reads the delivery"})...)
	appendEvents(t, p, 123, admitted(123, &model.TaskClose{Task: testRef(9, 1), Outcome: model.ClosureSuccess,
		AcceptanceWitnessRefs: []model.AcceptanceWitness{{CriterionID: testID(90), CriterionRevision: 1, WitnessRef: testArtifact()}},
		DeliveryWitnessRefs:   []model.ArtifactRef{testArtifact()}})...)
}

var viewsNonTasks = []model.ID{testID(2), testID(5), testID(6), testID(8), testID(9), testID(14), testID(15), testID(10), testID(11), testID(20),
	testID(21), testID(22), testID(30), testID(31), testID(32), testID(33)}

func view_(t *testing.T, p store.Project, r ViewRequest) ViewAnswer {
	t.Helper()
	a, err := ReadView(p, r)
	if err != nil {
		t.Fatalf("%s view must succeed: %v", r.View, err)
	}
	return a
}

// assertViewHonest checks the rendering rules every view answer keeps: the
// brief shows only JSON leaves at their paths (brief_test.go's rule), it
// renders deterministically, and the watermark heads the JSON and the brief.
func assertViewHonest(t *testing.T, a ViewAnswer) string {
	t.Helper()
	var exported, again bytes.Buffer
	if err := RenderViewJSON(&exported, a); err != nil {
		t.Fatal(err)
	}
	name := a.Header().View
	leaves := jsonLeaves(t, exported.Bytes())
	for _, key := range []string{"sequence", "bundles", "events", "head"} {
		found := false
		for path := range leaves {
			found = found || strings.HasPrefix(path, "answer\n\"watermark\"\n\""+key+"\"")
		}
		if !found {
			t.Fatalf("%s answer carries no watermark %s", name, key)
		}
	}
	text, facts, err := ViewBriefOf(exported.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if err := RenderViewBrief(&again, a); err != nil || again.String() != text {
		t.Fatalf("%s brief must render deterministically", name)
	}
	checkBriefFacts(t, name, exported.Bytes(), text, facts, a.Header().Watermark.Sequence)
	return text
}

func allViewRequests() []ViewRequest {
	out := []ViewRequest{{View: "todo"}, {View: "todo", Limit: 1}, {View: "show"}, {View: "show", Stale: richStale()},
		{View: "show", ID: testID(999)}, {View: "continue", ID: testID(999)}, {View: "history"}, {View: "history", ID: testID(1)},
		{View: "history", SelfAdmitted: model.SelfAdmissionTrue}}
	for kind := range viewKinds {
		out = append(out, ViewRequest{View: "show", Kind: kind})
	}
	for _, id := range append([]model.ID{testID(1)}, viewsNonTasks...) {
		out = append(out, ViewRequest{View: "show", ID: id}, ViewRequest{View: "continue", ID: id, Observed: richObservation()},
			ViewRequest{View: "continue", ID: id, Limit: 1})
	}
	return out
}

func TestEveryViewRendersHonestlyWithItsWatermark(t *testing.T) {
	p := testProject(t)
	viewsWorld(t, p)
	for _, r := range allViewRequests() {
		a := view_(t, p, r)
		text := assertViewHonest(t, a)
		if a.Header().Result == "UNKNOWN" && !strings.Contains(text, "\nreason: no admitted record "+string(testID(999))) {
			t.Fatalf("an UNKNOWN view must say why, got\n%s", text)
		}
	}
	// The cached path and a provided snapshot answer identically.
	source, err := store.Replayed(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range allViewRequests() {
		viaRead, viaSource := view_(t, p, r), must(ReadViewFrom(p, r, source))
		if renderedView(t, viaRead) != renderedView(t, viaSource) {
			t.Fatalf("%s %s answers differently from a provided snapshot", r.View, r.ID)
		}
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func renderedView(t *testing.T, a ViewAnswer) string {
	t.Helper()
	var j bytes.Buffer
	if err := RenderViewJSON(&j, a); err != nil {
		t.Fatal(err)
	}
	return j.String()
}

func TestTodoFilesEachOwedTaskOnceInFlightFirst(t *testing.T) {
	p := testProject(t)
	viewsWorld(t, p)
	a := view_(t, p, ViewRequest{View: "todo"}).(*TodoAnswer)
	ids := func(ts []TodoTask) []model.ID {
		out := []model.ID{}
		for _, t := range ts {
			out = append(out, t.Ref.RecordID)
		}
		return out
	}
	want := map[string][]model.ID{"in_flight": {testID(15)}, "awaiting": {testID(5), testID(6)}, "blocked": {testID(2), testID(8)},
		"ready": {testID(1), testID(14)}}
	got := map[string][]model.ID{"in_flight": ids(a.InFlight), "awaiting": ids(a.AwaitingAcceptance), "blocked": ids(a.Blocked), "ready": ids(a.Ready)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("todo sections = %v, want %v", got, want)
	}
	if a.Totals.Tasks != 7 || a.Totals.OpenDecisions != 3 || a.Totals.IntakeUnreviewed != 1 || a.Totals.IntakeRejected != 1 || a.Totals.IntakeCorrectionRequested != 0 || len(a.OpenDecisions) != 3 {
		t.Fatalf("totals must count each task identity once: %+v", a.Totals)
	}
	if a.AwaitingAcceptance[0].Accepter != (model.Actor{ID: "owner"}) || a.AwaitingAcceptance[1].Accepter != "anyone" {
		t.Fatalf("awaiting acceptance must name the accepter or anyone: %v, %v", a.AwaitingAcceptance[0].Accepter, a.AwaitingAcceptance[1].Accepter)
	}
	if !hasReasonKind(a.AwaitingAcceptance[1].Task.Reasons, reduce.ReasonResume) {
		t.Fatal("a task awaiting acceptance and held lists both reasons in its one entry")
	}
	if runs := a.InFlight[0].Runs; runs == nil || len(*runs) != 2 || !(*runs)[0].Sealed || (*runs)[1].Sealed || !hasAttention(a.Attention, "run-outside-task-scope") {
		t.Fatalf("an in-flight task carries every run of its, unsealed included: %+v", runs)
	}
	text := assertViewHonest(t, a)
	if !strings.HasPrefix(strings.SplitN(text, "\n", 3)[2], "in flight: 1\n") {
		t.Fatalf("in-flight work comes first:\n%s", text)
	}
}

func TestLimitsNeverCutBlockedWorkClosureOrAttention(t *testing.T) {
	p := testProject(t)
	viewsWorld(t, p)
	full := view_(t, p, ViewRequest{View: "todo"}).(*TodoAnswer)
	cut := view_(t, p, ViewRequest{View: "todo", Limit: 1}).(*TodoAnswer)
	if len(cut.Ready) != 1 || cut.Omitted["ready"].Omitted != 1 || cut.Totals != full.Totals {
		t.Fatalf("control: the limit cuts READY and reports it, totals unchanged: %+v %+v", cut.Omitted, cut.Totals)
	}
	if !reflect.DeepEqual(cut.InFlight, full.InFlight) || !reflect.DeepEqual(cut.Blocked, full.Blocked) ||
		!reflect.DeepEqual(cut.AwaitingAcceptance, full.AwaitingAcceptance) || !reflect.DeepEqual(cut.OpenDecisions, full.OpenDecisions) ||
		!reflect.DeepEqual(cut.Attention, full.Attention) || !reflect.DeepEqual(cut.IntakePending, full.IntakePending) {
		t.Fatal("the todo limit must never cut in-flight, blocked or awaiting work, open decisions, intake or attention")
	}
	if len(full.Attention) == 0 {
		t.Fatal("control: the fixture must raise attention")
	}
	// Decision 33 offers two resolved optional refs and one unresolved one.
	whole := view_(t, p, ViewRequest{View: "continue", ID: testID(33)}).(*ContinueAnswer)
	one := view_(t, p, ViewRequest{View: "continue", ID: testID(33), Limit: 1}).(*ContinueAnswer)
	if len(whole.Context.Refs) != 3 || whole.Context.Limit.Offered != 3 || whole.Context.Limit.Omitted != 0 {
		t.Fatalf("control: decision 33 offers three optional refs, got %+v", whole.Context)
	}
	if len(one.Context.Refs) != 2 || one.Context.Limit.Omitted != 1 || one.Context.Refs[1].Unresolved == nil {
		t.Fatalf("limit 1 keeps one resolved ref and every unresolved one, and counts the cut: %+v", one.Context)
	}
	if !reflect.DeepEqual(one.Attention, whole.Attention) || !hasAttention(one.Attention, "unresolved-link") {
		t.Fatalf("the limit must never cut attention:\n%+v\n%+v", one.Attention, whole.Attention)
	}
	plan := view_(t, p, ViewRequest{View: "continue", ID: testID(8), Limit: 1}).(*ContinueAnswer)
	planWhole := view_(t, p, ViewRequest{View: "continue", ID: testID(8)}).(*ContinueAnswer)
	if len(plan.Closure.Mandatory) != 2 || !reflect.DeepEqual(plan.Closure, planWhole.Closure) || !reflect.DeepEqual(plan.Owed, planWhole.Owed) {
		t.Fatalf("the limit must never cut mandatory closure: %+v", plan.Closure)
	}
}
