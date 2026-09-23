package query

// continue and show as the views define them: continue carries each record
// and run once for any kind, show summarises before it lists and keeps the
// instrument attention, requests outside their view are refused, and the
// history view is the history read.

import (
	"reflect"
	"strings"
	"testing"

	"datum/internal/model"
	"datum/internal/reduce"
)

// eachObject visits every JSON object in the export.
func eachObject(v any, visit func(map[string]any)) {
	switch node := v.(type) {
	case map[string]any:
		visit(node)
		for _, child := range node {
			eachObject(child, visit)
		}
	case []any:
		for _, child := range node {
			eachObject(child, visit)
		}
	}
}

// assertEachBodyOnce: every record body (an object with a fact) and every run
// body (an object with an invocation and its start) appears exactly once.
func assertEachBodyOnce(t *testing.T, a ViewAnswer) (records, runs int) {
	t.Helper()
	seen := map[string]int{}
	eachObject(exported(t, a), func(o map[string]any) {
		if _, ok := o["fact"]; ok {
			seen["record "+factKey(o, "fact")]++
			records++
		}
		if _, ok := o["start"]; ok {
			if id, ok := o["invocation"].(string); ok {
				seen["run "+id]++
				runs++
			}
		}
	})
	for body, n := range seen {
		if n != 1 {
			t.Fatalf("%s %s: %s appears %d times; each body appears once", a.Header().View, a.Header().Result, body, n)
		}
	}
	return records, runs
}

func TestContinueCarriesEachRecordAndRunOnceForAnyKind(t *testing.T) {
	p := testProject(t)
	viewsWorld(t, p)
	for _, id := range append([]model.ID{testID(1)}, viewsNonTasks...) {
		a := view_(t, p, ViewRequest{View: "continue", ID: id, Observed: richObservation()}).(*ContinueAnswer)
		records, _ := assertEachBodyOnce(t, a)
		if records != len(a.Records) || records == 0 {
			t.Fatalf("continue %s: %d bodies, %d records", id, records, len(a.Records))
		}
		if _, ok := a.Records[recordKey(*a.Record)]; !ok {
			t.Fatalf("continue %s: the root's body is missing", id)
		}
		for _, n := range append(append([]ClosureRef{}, a.Closure.Mandatory...), a.Context.Refs...) {
			if _, ok := a.Records[recordKey(n.Ref)]; ok == (n.Unresolved != nil) {
				t.Fatalf("continue %s: ref %s must have a body exactly when resolved", id, recordKey(n.Ref))
			}
		}
		if a.Observed == nil || a.Observed.ObservedAt.State != model.Known {
			t.Fatalf("continue %s must carry the caller's fresh observation", id)
		}
		isTask := a.Records[recordKey(*a.Record)].Task != nil
		if isTask != (a.Attempts != nil) || isTask != (a.Owed != nil) {
			t.Fatalf("continue %s: attempts and owed belong to tasks alone", id)
		}
	}
	// Task 1's context claims observe its own runs: each run is carried once.
	task := view_(t, p, ViewRequest{View: "continue", ID: testID(1)}).(*ContinueAnswer)
	if len(task.Context.Refs) != 2 || len(task.Records) != 3 {
		t.Fatalf("control: task 1's continuation carries its two context claims, got %d refs %d records", len(task.Context.Refs), len(task.Records))
	}
	if _, runs := assertEachBodyOnce(t, task); runs != 3 || task.Observed.Head.State != model.Unknown {
		t.Fatalf("task 1 continuation carries its three runs once and UNKNOWN when nothing was observed, got %d", runs)
	}
	plan := view_(t, p, ViewRequest{View: "continue", ID: testID(8)}).(*ContinueAnswer)
	if len(plan.Owed.Items) != 2 || plan.Owed.Items[0].Status != reduce.StatusBlocked || plan.Owed.Items[1].Status != reduce.StatusReady {
		t.Fatalf("a plan's owed items carry each item's status: %+v", plan.Owed.Items)
	}
	open := view_(t, p, ViewRequest{View: "continue", ID: testID(33)}).(*ContinueAnswer)
	if !hasAttention(open.Attention, "decision-open") || !hasAttention(open.Attention, "unresolved-link") {
		t.Fatalf("an open decision's continuation names who it waits on and its unresolved link: %+v", open.Attention)
	}
}

func hasAttention(notes []Attention, kind string) bool {
	for _, n := range notes {
		if n.Kind == kind {
			return true
		}
	}
	return false
}

func TestShowSummarisesThenListsAndKeepsInstrumentAttention(t *testing.T) {
	p := testProject(t)
	viewsWorld(t, p)
	bare := view_(t, p, ViewRequest{View: "show"}).(*ShowAnswer)
	s := bare.Summary
	if s == nil || s.Runs == nil || s.Runs.Unsealed != 2 || (*s.Tasks)["CLOSED"] != 1 || (*s.Claims)["PROVEN"] != 1 || s.Owed.AwaitingAcceptance != 2 {
		t.Fatalf("bare show opens with the summary: %+v %v %v %+v %+v", s, *s.Tasks, *s.Claims, *s.Runs, *s.Owed)
	}
	kinds := []model.Kind{}
	for _, r := range bare.Records {
		if len(kinds) == 0 || kinds[len(kinds)-1] != r.Fact.Kind {
			kinds = append(kinds, r.Fact.Kind)
		}
	}
	if !reflect.DeepEqual(kinds, []model.Kind{model.Task, model.Claim, model.Decision, model.Instrument}) {
		t.Fatalf("records are grouped by kind: %v", kinds)
	}
	assertEachBodyOnce(t, bare)
	instruments := view_(t, p, ViewRequest{View: "show", Kind: "instrument"}).(*ShowAnswer)
	if len(instruments.Records) != 2 || !hasAttention(instruments.Attention, "instrument-validation-unknown") ||
		!hasAttention(instruments.Attention, "instrument-trust-not-active") || instruments.Summary.Tasks != nil {
		t.Fatalf("show --kind instrument keeps validation and trust attention: %+v", instruments.Attention)
	}
	text := assertViewHonest(t, instruments)
	if !strings.Contains(text, "INSTRUMENT "+string(testID(11))+" rev 1 validation UNKNOWN trust UNKNOWN") {
		t.Fatalf("instruments read validation first:\n%s", text)
	}
	claim := view_(t, p, ViewRequest{View: "show", ID: testID(22)}).(*ShowAnswer)
	if claim.Runs == nil || len(*claim.Runs) != 1 || claim.Summary != nil {
		t.Fatalf("show of a claim carries its observed run and no summary: %+v", claim)
	}
	if one := claim.Records[0]; one.AdmittedBy != (model.Actor{ID: "reviewer"}) || one.SelfAdmitted != "false" {
		t.Fatalf("every record names who admitted it and whether it was self-admitted: %v %v", one.AdmittedBy, one.SelfAdmitted)
	}
	// Task 2 was appended without a review: who admitted it is not recorded.
	bare2 := view_(t, p, ViewRequest{View: "show", ID: testID(2)}).(*ShowAnswer).Records[0]
	if u, ok := bare2.AdmittedBy.(Unknown); !ok || u.State != "UNKNOWN" || u.Reason == "" || bare2.SelfAdmitted != "UNKNOWN" {
		t.Fatalf("an unattributed record's admitter is UNKNOWN with a reason, never guessed: %v %v", bare2.AdmittedBy, bare2.SelfAdmitted)
	}
}

func TestViewRequestsOutsideTheirViewAreRefused(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	if _, err := ReadView(p, ViewRequest{View: "todo"}); err != nil {
		t.Fatalf("control: plain todo must succeed: %v", err)
	}
	stale := richStale()
	for _, r := range []ViewRequest{{View: "now"}, {View: "todo", ID: testID(1)}, {View: "continue"}, {View: "show", ID: testID(1), Kind: "task"},
		{View: "show", Kind: "tasks"}, {View: "todo", Kind: "task"}, {View: "show", Limit: 2}, {View: "todo", Limit: -1},
		{View: "show", Observed: richObservation()}, {View: "todo", Stale: stale}, {View: "todo", SelfAdmitted: model.SelfAdmissionTrue},
		{View: "history", ID: testID(1), SelfAdmitted: model.SelfAdmissionTrue}, {View: "show", ID: "not-a-ulid"}} {
		if _, err := ReadView(p, r); err == nil {
			t.Errorf("%+v was accepted", r)
		}
	}
}
