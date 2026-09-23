package query

// Mandatory closure tests, read through the continue view: deep constraint
// and prerequisite chains, cycles, supersession and correction expansion,
// cross-project links, and a limit that can cut only optional topic refs.

import (
	"testing"

	"datum/internal/model"
	"datum/internal/store"
)

func taskWith(n int, edit func(*model.TaskSpec)) *model.TaskCreate {
	task := testTask(n)
	edit(&task.Spec)
	return task
}

// closureWorld: task 1 is constrained by task 2, which needs task 3, which is
// constrained by task 2 again (a cycle). Task 3 also names decision 31, which
// was superseded by decision 32. A correction on claim 20 names claim 21 too.
// Task 1 has three topic refs and one cross-project constraint.
func closureWorld(t *testing.T) (*ContinueAnswer, func(limit int) *ContinueAnswer) {
	t.Helper()
	p := testProject(t)
	foreign := model.RecordRef{Project: "other/project", RecordID: testID(900), Revision: 1}
	appendEvents(t, p, 100,
		&model.DecisionOpen{ID: testID(31), Provenance: prov("lane-c"), Spec: decisionSpec()},
		&model.DecisionOpen{ID: testID(32), Provenance: prov("lane-c"), Spec: decisionSpec()},
		&model.ClaimAssert{ID: testID(20), Provenance: prov("lane-e"), Spec: claimSpec()},
		&model.ClaimAssert{ID: testID(21), Provenance: prov("lane-e"), Spec: claimSpec()},
		&model.ClaimAssert{ID: testID(40), Provenance: prov("lane-e"), Spec: claimSpec()},
		&model.ClaimAssert{ID: testID(41), Provenance: prov("lane-e"), Spec: claimSpec()},
		&model.ClaimAssert{ID: testID(42), Provenance: prov("lane-e"), Spec: claimSpec()})
	appendEvents(t, p, 101, taskWith(3, func(s *model.TaskSpec) {
		s.ConstraintRefs = []model.RecordRef{testRef(31, 1), testRef(20, 1)}
	}))
	appendEvents(t, p, 102, taskWith(2, func(s *model.TaskSpec) {
		s.Prerequisites = []model.Prerequisite{{Kind: "task-success", Target: testRef(3, 1), WaiverPolicy: "forbid"}}
	}))
	appendEvents(t, p, 103, &model.TaskAmend{Target: testRef(3, 1), ExpectedRevision: 1, Provenance: prov("author"),
		Replacement: taskWith(3, func(s *model.TaskSpec) {
			s.ConstraintRefs = []model.RecordRef{testRef(31, 1), testRef(20, 1), testRef(2, 1)}
		}).Spec})
	appendEvents(t, p, 104, taskWith(1, func(s *model.TaskSpec) {
		s.ConstraintRefs = []model.RecordRef{testRef(2, 1), foreign}
		s.ContextRefs = []model.RecordRef{testRef(40, 1), testRef(41, 1), testRef(42, 1), testRef(2, 1)}
	}))
	appendEvents(t, p, 105,
		&model.Supersede{Prior: testRef(31, 1), Replacement: testRef(32, 1), Reason: "the owner restated the ruling"},
		&model.Correction{Target: model.CorrectionTarget{Kind: "record", Record: ptrRef(testRef(20, 1))},
			AffectedRevisions: []model.RecordRef{testRef(20, 1), testRef(21, 1)}, Reason: "wrong denominator", CorrectiveRef: testArtifact()})
	at := func(limit int) *ContinueAnswer { return continueOf(t, p, testID(1), limit) }
	return at(0), at
}

func continueOf(t *testing.T, p store.Project, id model.ID, limit int) *ContinueAnswer {
	t.Helper()
	return view_(t, p, ViewRequest{View: "continue", ID: id, Limit: limit}).(*ContinueAnswer)
}

func ptrRef(r model.RecordRef) *model.RecordRef { return &r }

func mandatoryRefs(a *ContinueAnswer) map[model.RecordRef]ClosureRef {
	out := map[model.RecordRef]ClosureRef{}
	for _, n := range a.Closure.Mandatory {
		out[n.Ref] = n
	}
	return out
}

func TestClosureExpandsEveryMandatoryLinkFullyWithCycleDetection(t *testing.T) {
	a, _ := closureWorld(t)
	assertViewHonest(t, a)
	c := a.Closure
	got := mandatoryRefs(a)
	// Task 2 constrains; task 3 is its prerequisite; decision 31 and claim 20
	// constrain task 3 revision 1 only through task 2's exact-revision link.
	for _, want := range []model.RecordRef{testRef(2, 1), testRef(3, 1), testRef(31, 1), testRef(32, 1), testRef(20, 1), testRef(21, 1)} {
		if _, ok := got[want]; !ok {
			t.Fatalf("mandatory closure is missing %v; one hop is not enough for authority. Got %v", want, c.Mandatory)
		}
	}
	if n := got[testRef(32, 1)]; a.Records[recordKey(n.Ref)].Decision == nil || n.Via[0].Relation != "superseded-by" {
		t.Fatalf("a superseded constraint must lead to its replacement, got %+v", n)
	}
	if n := a.Records[recordKey(testRef(20, 1))]; len(n.Corrections) != 1 || n.Claim == nil {
		t.Fatalf("a corrected constraint must carry its correction, got %+v", n)
	}
	foreign := got[model.RecordRef{Project: "other/project", RecordID: testID(900), Revision: 1}]
	if foreign.Unresolved == nil || foreign.Unresolved.State != "UNKNOWN" || foreign.Unresolved.Reason == "" {
		t.Fatalf("a cross-project constraint must stay visible as UNKNOWN, got %+v", foreign)
	}
	if len(c.Cycles) != 0 {
		t.Fatalf("task 2 names task 3 at revision 1, which has no back edge; expected no cycle, got %v", c.Cycles)
	}
}

func TestClosureReportsACycleInsteadOfLooping(t *testing.T) {
	p := testProject(t)
	appendEvents(t, p, 100, testTask(2))
	appendEvents(t, p, 101, taskWith(3, func(s *model.TaskSpec) { s.ConstraintRefs = []model.RecordRef{testRef(2, 1)} }))
	appendEvents(t, p, 102, &model.TaskAmend{Target: testRef(2, 1), ExpectedRevision: 1, Provenance: prov("author"),
		Replacement: taskWith(2, func(s *model.TaskSpec) { s.ConstraintRefs = []model.RecordRef{testRef(3, 1)} }).Spec})
	appendEvents(t, p, 103, &model.Supersede{Prior: testRef(2, 1), Replacement: testRef(2, 2), Reason: "restated"})
	a := continueOf(t, p, testID(3), 0)
	assertViewHonest(t, a)
	c := a.Closure
	if len(c.Cycles) != 1 || len(c.Cycles[0]) != 4 || c.Cycles[0][0] != testRef(3, 1) || c.Cycles[0][3] != testRef(3, 1) {
		t.Fatalf("3 -> 2@1 -> 2@2 -> 3 must be reported once as a cycle, got %v", c.Cycles)
	}
	found := false
	for _, note := range a.Attention {
		found = found || note.Kind == "closure-cycle"
	}
	if !found || len(c.Mandatory) != 2 {
		t.Fatalf("the cycle must be raised and each node listed once, got %+v", c)
	}
}

func TestClosureLimitCutsOnlyOptionalTopicRefs(t *testing.T) {
	_, at := closureWorld(t)
	full := at(0)
	for _, limit := range []int{1, 2} {
		cut := at(limit)
		if len(cut.Closure.Mandatory) != len(full.Closure.Mandatory) || len(cut.Closure.Cycles) != len(full.Closure.Cycles) {
			t.Fatalf("limit %d changed the mandatory closure: %d vs %d nodes", limit, len(cut.Closure.Mandatory), len(full.Closure.Mandatory))
		}
		if len(cut.Context.Refs) != limit || cut.Context.Limit.Offered != 3 || cut.Context.Limit.Omitted != 3-limit || cut.Context.Limit.Requested != limit {
			t.Fatalf("limit %d must keep %d optional refs and report the rest, got %+v", limit, limit, cut.Context.Limit)
		}
	}
	if len(full.Context.Refs) != 3 || full.Context.Limit.Requested != "none" {
		t.Fatalf("with no limit every topic ref not already mandatory is offered, got %+v", full.Context.Refs)
	}
	for _, n := range full.Context.Refs {
		if n.Ref == testRef(2, 1) {
			t.Fatal("a record already in the mandatory closure must not reappear as an optional topic")
		}
	}
}

// TestClosureTaskNodeIsTheExactRevision: a consumer naming task 5 revision 1
// must see revision 1's own prerequisites, not those of the amended revision 2.
func TestClosureTaskNodeIsTheExactRevision(t *testing.T) {
	p := testProject(t)
	appendEvents(t, p, 100, testTask(6))
	appendEvents(t, p, 101, taskWith(5, func(s *model.TaskSpec) {
		s.Prerequisites = []model.Prerequisite{{Kind: "task-success", Target: testRef(6, 1), WaiverPolicy: "forbid"}}
	}))
	appendEvents(t, p, 102, &model.TaskAmend{Target: testRef(5, 1), ExpectedRevision: 1, Provenance: prov("author"),
		Replacement: testTask(5).Spec})
	appendEvents(t, p, 103, taskWith(7, func(s *model.TaskSpec) { s.ConstraintRefs = []model.RecordRef{testRef(5, 1)} }))
	a := continueOf(t, p, testID(7), 0)
	_, ok := mandatoryRefs(a)[testRef(5, 1)]
	n := a.Records[recordKey(testRef(5, 1))]
	if !ok || n.Task == nil || n.CurrentRevision != 2 {
		t.Fatalf("control: the constrained task must resolve with current revision 2, got %+v", n)
	}
	if n.Task.Revision != 1 || len(n.Task.Prerequisites) != 1 {
		t.Fatalf("closure node for revision 1 shows revision %d with %d prerequisites; it described the current revision",
			n.Task.Revision, len(n.Task.Prerequisites))
	}
}
