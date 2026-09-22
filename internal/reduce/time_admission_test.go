package reduce_test

// The application admission gate's current timestamp reachability lives here.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
	"datum/internal/write"
)

func admissionUnknown[T any]() model.Availability[T] {
	return model.Availability[T]{State: model.Unknown, Reason: "not observed"}
}

func TestInvocationLocationThroughApplicationAdmission(t *testing.T) {
	// Intake intentionally uses an isolated home so this real capture never
	// writes to the user's inbox.
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	project := store.Project{ID: "test/zone-admission", Root: root, Ledger: filepath.Join(root, "record", "events")}
	id := func(n int) model.ID { return model.ID(fmt.Sprintf("%026d", n)) }
	author := model.Actor{ID: "runner"}
	capture := func(command int, event model.TypedEvent) model.PacketRef {
		t.Helper()
		raw, err := model.EncodeEvent(event)
		if err != nil {
			t.Fatal(err)
		}
		ref, err := store.WriteIntake(context.Background(), project, store.IntakeRequest{
			CommandID: id(command), Author: author, Events: []model.Event{raw},
		})
		if err != nil {
			t.Fatal(err)
		}
		return ref
	}
	admit := func(command int, packet model.PacketRef) (model.Bundle, error) {
		return write.Admit(context.Background(), project, write.AdmitRequest{
			CommandID: id(command), PacketIDs: []model.ID{packet.CommandID},
			Admitter: model.Actor{ID: "reviewer"}, Outcome: "accepted", Reason: "timestamp reachability experiment",
		})
	}
	task := &model.TaskCreate{ID: id(1), Provenance: model.Provenance{Author: author, SourceRefs: []model.ArtifactRef{}},
		Spec: model.TaskSpec{
			Intent: "test admission", Subject: "timestamp reachability",
			Scope:    model.Scope{SourcePaths: []string{}, ContextRefs: []model.RecordRef{}, AppliesWhen: "this test", Limitations: "fixture"},
			NonGoals: []string{"production measurements"}, AcceptanceCriteria: []model.AcceptanceCriterion{{ID: id(2), Revision: 1, Criterion: "trace the timestamp"}},
			ContextRefs: []model.RecordRef{}, ConstraintRefs: []model.RecordRef{}, Prerequisites: []model.Prerequisite{}, NextActor: author,
		}}
	if _, err := admit(11, capture(10, task)); err != nil {
		t.Fatal(err)
	}
	start := &model.TaskStart{Task: model.RecordRef{Project: project.ID, RecordID: task.ID, Revision: 1}, Actor: author, AttemptID: id(3)}
	if _, err := admit(13, capture(12, start)); err != nil {
		t.Fatal(err)
	}
	env := model.InvocationEnvelope{
		InvocationID: id(4), AttemptID: start.AttemptID,
		InstrumentRef: model.RecordRef{Project: "external/instrument", RecordID: id(5), Revision: 1},
		CriterionRef:  admissionUnknown[model.CriterionRef](),
		ExecutionSourceIdentity: model.ExecutionIdentity{Project: project.ID, MachineID: admissionUnknown[model.ID](),
			SourceRefs: []model.ArtifactRef{}, Head: admissionUnknown[model.GitHead](), Dirty: admissionUnknown[bool]()},
		Argv: []string{"fixture"}, InputRefs: []model.ArtifactRef{}, ConfigRequested: map[string]model.Scalar{},
		ConditionsDeclared: map[string]model.Scalar{}, ConfigEffective: admissionUnknown[map[string]model.Availability[model.Scalar]](),
		ConditionsObserved: admissionUnknown[map[string]model.Availability[model.Scalar]](), Isolation: admissionUnknown[model.Isolation](),
		StartedAt:  time.Date(2026, 9, 22, 12, 0, 0, 0, time.FixedZone("fixture", 37*60)),
		ObservedAt: admissionUnknown[time.Time](), Outcome: admissionUnknown[model.ProcessOutcome](),
		OutputRefs: admissionUnknown[[]model.ArtifactRef](), Visual: admissionUnknown[model.VisualObservation](),
	}
	packet := capture(14, &model.InvocationStart{Envelope: env})
	packets, err := store.ReadIntake(project, []model.ID{packet.CommandID})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := model.DecodeEvent(packets[0].Events[0])
	if err != nil {
		t.Fatal(err)
	}
	stamp := decoded.(*model.InvocationStart).Envelope.StartedAt
	_, offset := stamp.Zone()
	if stamp.Location() == time.UTC || offset != 37*60 {
		t.Fatalf("real intake lost the non-UTC timestamp: %s", stamp)
	}
	_, err = admit(15, packet)
	var fault *model.Fault
	if !errors.As(err, &fault) || fault.Code != "unavailable-until-integrated" || fault.Path != "event.type" {
		t.Fatalf("invocation admission policy changed; re-evaluate reachability: %v", err)
	}
	t.Logf("real intake retains %s, location=%q; write.Admit refuses: %v", stamp.Format(time.RFC3339Nano), stamp.Location(), err)
	prefix, err := store.ReadPrefix(project)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reduce.Replay(prefix)
	if err != nil || len(prefix) != 2 || len(snapshot.Invocations()) != 0 {
		t.Fatalf("refused invocation changed the ledger: bundles=%d error=%v", len(prefix), err)
	}
	if snapshot.Watermark().RecordedAt.Location() != time.UTC {
		t.Fatal("the actually admitted watermark is not UTC")
	}
}
