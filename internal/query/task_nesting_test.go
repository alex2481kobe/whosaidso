// Prerequisite navigation groups open task identities without inventing parent
// records. Shared descendants count once; closed and unresolved links do not hide work.
package query

import (
	"reflect"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestTaskNestingUsesCurrentOpenPrerequisiteLinks(t *testing.T) {
	p := screenProject(t)
	tasks := map[int]*model.TaskCreate{}
	events := []model.TypedEvent{}
	for i := 1; i <= 9; i++ {
		tasks[i] = testTask(i)
		events = append(events, tasks[i])
	}
	link := func(parent int, children ...int) {
		for _, child := range children {
			tasks[parent].Spec.Prerequisites = append(tasks[parent].Spec.Prerequisites, model.Prerequisite{Kind: "task-success", Target: testRef(child, 1), WaiverPolicy: "forbid"})
		}
	}
	link(1, 2, 3, 2)
	link(2, 4)
	link(3, 4)
	link(5, 4)
	link(9, 6)
	tasks[8].Spec.Prerequisites = []model.Prerequisite{{Kind: "task-success", Target: model.RecordRef{Project: "elsewhere/project", RecordID: testID(7), Revision: 1}, WaiverPolicy: "forbid"}}
	events = nil
	for _, i := range []int{4, 2, 3, 1, 5, 6, 7, 8, 9} {
		events = append(events, tasks[i])
	}
	appendEvents(t, p, 1000, events...)
	appendEvents(t, p, 1001, &model.TaskClose{Task: testRef(9, 1), Outcome: model.ClosureCancelled, AcceptanceWitnessRefs: []model.AcceptanceWitness{}, DeliveryWitnessRefs: []model.ArtifactRef{}})
	read := func(under model.ID) *TaskListAnswer {
		t.Helper()
		return view_(t, p, ViewRequest{View: "todo", Top: under == "", Under: under}).(*TaskListAnswer)
	}
	ids := func(rows []ScreenRow) []model.ID {
		out := []model.ID{}
		for _, row := range rows {
			out = append(out, row.ID)
		}
		return out
	}
	top := read("")
	if !reflect.DeepEqual(ids(top.Tasks), []model.ID{testID(1), testID(5), testID(6), testID(7), testID(8)}) || top.Open != 8 || top.Tasks[0].Nested != 3 || top.Tasks[1].Nested != 1 {
		t.Fatalf("top-level open tasks: %+v", top)
	}
	limited := view_(t, p, ViewRequest{View: "todo", Top: true, Limit: 1}).(*TaskListAnswer)
	if !reflect.DeepEqual(ids(limited.Tasks), []model.ID{testID(1), testID(5), testID(6), testID(8)}) || limited.Total != 5 {
		t.Fatalf("optional limit hid mandatory parents: %+v", limited)
	}
	limitedChildren := view_(t, p, ViewRequest{View: "todo", Under: testID(1), Limit: 1}).(*TaskListAnswer)
	if len(limitedChildren.Tasks) != 2 {
		t.Fatal("optional limit hid blocked children")
	}
	under := read(testID(1))
	if !reflect.DeepEqual(ids(under.Tasks), []model.ID{testID(2), testID(3)}) || under.Tasks[0].Nested != 1 || under.Tasks[1].Nested != 1 {
		t.Fatalf("direct children: %+v", under)
	}
	if rows := read(testID(2)).Tasks; len(rows) != 1 || rows[0].ID != testID(4) || rows[0].Nested != 0 {
		t.Fatalf("deep nesting: %+v", rows)
	}
	if len(read(testID(4)).Tasks) != 0 {
		t.Fatal("leaf must not fall back to top-level tasks")
	}
	for _, id := range []model.ID{testID(9), testID(999)} {
		if read(id).Result != "UNKNOWN" {
			t.Fatal("closed or absent parent was treated as open")
		}
	}
	page, err := ReadScreen(p, ScreenRequest{View: "tasks", Sort: "oldest"})
	if err != nil || !reflect.DeepEqual(ids(page.Rows), ids(top.Tasks)) {
		t.Fatalf("viewer and CLI nesting differ: %v", err)
	}
	child, err := ReadScreen(p, ScreenRequest{View: "tasks", Under: testID(1), Sort: "oldest"})
	if err != nil || child.Parent == nil || child.Parent.ID != testID(1) || !reflect.DeepEqual(ids(child.Rows), ids(under.Tasks)) {
		t.Fatalf("viewer drill-in: %+v %v", child, err)
	}
	amended := tasks[1].Spec
	amended.Prerequisites = amended.Prerequisites[:1]
	appendEvents(t, p, 1002, &model.TaskAmend{Target: testRef(1, 1), Provenance: tasks[1].Provenance, Replacement: amended})
	if rows := read(testID(1)).Tasks; len(rows) != 1 || rows[0].ID != testID(2) {
		t.Fatal("navigation used old prerequisites")
	}
	top = read("")
	if top.Tasks[0].Nested != 2 || top.Tasks[1].ID != testID(3) {
		t.Fatal("amendment did not release a former child to the top level")
	}
	for _, bad := range []ViewRequest{{View: "todo", Top: true, Under: testID(1)}, {View: "show", Top: true}, {View: "todo", Under: "bad"}} {
		if CheckView(bad) == nil {
			t.Fatalf("invalid navigation request accepted: %+v", bad)
		}
	}
	assertViewHonest(t, top)
}

func TestTaskNestingCyclesAreBoundedAndVisible(t *testing.T) {
	p := screenProject(t)
	a, b := testTask(1), testTask(2)
	appendEvents(t, p, 1000, a, b)
	a.Spec.Prerequisites = []model.Prerequisite{{Kind: "task-success", Target: testRef(2, 1), WaiverPolicy: "forbid"}}
	b.Spec.Prerequisites = []model.Prerequisite{{Kind: "task-success", Target: testRef(1, 1), WaiverPolicy: "forbid"}}
	appendEvents(t, p, 1001, &model.TaskAmend{Target: testRef(1, 1), Provenance: a.Provenance, Replacement: a.Spec}, &model.TaskAmend{Target: testRef(2, 1), Provenance: b.Provenance, Replacement: b.Spec})
	top := view_(t, p, ViewRequest{View: "todo", Top: true}).(*TaskListAnswer)
	if len(top.Tasks) != 0 || top.Open != 2 || top.Note == "" {
		t.Fatalf("cyclic work silently disappeared: %+v", top)
	}
	under := view_(t, p, ViewRequest{View: "todo", Under: testID(1)}).(*TaskListAnswer)
	if len(under.Tasks) != 1 || under.Tasks[0].ID != testID(2) || under.Tasks[0].Nested != 1 {
		t.Fatalf("cycle traversal: %+v", under)
	}
}
