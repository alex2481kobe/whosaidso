package query

import (
	"reflect"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
)

func TestHistoryPreservesRevisionsOrderSourcesAndIncomingReferences(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	control := historyOf(t, p, testID(1))
	if len(control.Events) != 1 || control.Events[0].Event.Type != "task.create" {
		t.Fatalf("control task history must contain its creation, got %+v", control.Events)
	}
	spec := testTask(1).Spec
	spec.Intent = "Build the read slice with explicit JSON export"
	dependent := testTask(2)
	dependent.Spec.Prerequisites = []model.Prerequisite{{Kind: "task-success", Target: testRef(1, 1), WaiverPolicy: "forbid"}}
	appendEvents(t, p, 101,
		&model.TaskAmend{Target: testRef(1, 1), Replacement: spec, Provenance: testTask(1).Provenance},
		&model.SourceIntake{SourceID: testID(50), SourceRef: testArtifact(), OriginalDigest: testArtifact().Content.SHA256,
			Length: 3, Speaker: model.Actor{UnknownReason: "speaker was not identified"}, Referents: []model.RecordRef{testRef(1, 1)}},
		dependent, testTask(3),
		&model.TaskStart{Task: testRef(1, 2), AttemptID: testID(70), Actor: model.Actor{ID: "worker"}})
	a := historyOf(t, p, testID(1))
	types := []model.EventType{}
	for _, event := range a.Events {
		types = append(types, event.Event.Type)
	}
	want := []model.EventType{"task.create", "task.amend", "source.intake", "task.create", "task.start"}
	if !reflect.DeepEqual(types, want) || a.Events[4].Origin != (reduce.Origin{Sequence: 2, EventIndex: 4}) {
		t.Fatalf("history must retain creation, amendment, explicit source, incoming reference and start in ledger order, excluding unrelated task 3; got %+v", a.Events)
	}
	if a.Events[0].CommandID != testID(100) || a.Events[0].Admitter.ID != "reviewer" || a.Watermark.Sequence != 2 {
		t.Fatalf("history event origins must be distinct from the query watermark, got %+v", a)
	}
	show := showOf(t, p, testID(1))
	if show.Records[0].Fact.Key.Revision != 2 || len(show.Records[0].Sources) != 1 || show.Records[0].Sources[0].Intake.Speaker.ID != "" {
		t.Fatalf("current task must show revision 2 and its explicit source with unknown speaker, got %+v", show.Records)
	}
	if got := showOf(t, p, testID(3)); len(got.Records[0].Sources) != 0 {
		t.Fatalf("unrelated task must not borrow the neighboring source, got %+v", got.Records[0].Sources)
	}
	all := historyOf(t, p, "")
	if len(all.Events) != 6 {
		t.Fatalf("unfiltered history must retain every admitted event, got %d", len(all.Events))
	}
}

func TestUnknownPrerequisiteAndUnknownAttemptHolderRemainExplicit(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	dependent := testTask(2)
	dependent.Spec.Prerequisites = []model.Prerequisite{{Kind: "task-success", Target: model.RecordRef{
		Project: "another/project", RecordID: testID(1), Revision: 1}, WaiverPolicy: "forbid"}}
	appendEvents(t, p, 101, dependent, &model.TaskStart{Task: testRef(1, 1), AttemptID: testID(70),
		Actor: model.Actor{UnknownReason: "holder attribution was not supplied"}})
	blocked := showOf(t, p, testID(2)).Records[0].Task
	if blocked.Status != reduce.StatusBlocked || blocked.Prerequisites[0].Truth != reduce.TruthUnknown || blocked.Reasons[0].Actor.ID != "" || blocked.Reasons[0].Actor.UnknownReason == "" {
		t.Fatalf("cross-project dependency must remain BLOCKED/UNKNOWN without borrowing this project's actor, got %+v", blocked)
	}
	holder := showOf(t, p, testID(1)).Records[0].Task.AttemptHolders[0].Actor
	if holder != (Unknown{"UNKNOWN", "holder attribution was not supplied"}) {
		t.Fatalf("unknown attempt holder must not become the expected next actor, got %+v", holder)
	}
}
