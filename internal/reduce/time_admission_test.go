package reduce_test

// UTC-only timestamps through real intake and the application admission gate live here.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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

// Ruling R8.4 flipped this test. It was written to PROVE the hole: real intake
// retained a +00:37 invocation timestamp, and only the invocation admission
// gate stood between it and the ledger. The wire now carries only UTC: an
// in-process timestamp is captured as the same instant in UTC, and event bytes
// carrying an offset are refused when decoded. U12 opened invocation.start, so
// the invariant is now end to end: the start admits and the ledger holds UTC.
func TestInvocationTimestampIsUTCThroughApplicationAdmission(t *testing.T) {
	// Intake intentionally uses an isolated home so this real capture never
	// writes to the user's inbox.
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	project := store.Project{ID: "test/zone-admission", Root: root, Ledger: filepath.Join(root, ".datum", "events")}
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
	if stamp.Location() != time.UTC || !stamp.Equal(env.StartedAt) {
		t.Fatalf("real intake must hold the same instant in UTC: %s", stamp.Format(time.RFC3339Nano))
	}
	// The same event with its offset spelled in the bytes is refused, not normalised.
	offset := packets[0].Events[0]
	offset.Data = []byte(strings.Replace(string(offset.Data), "2026-09-22T11:23:00Z", "2026-09-22T12:00:00+00:37", 1))
	if _, err := model.DecodeEvent(offset); !errors.As(err, new(*model.Fault)) || !strings.Contains(err.Error(), "envelope.started_at") {
		t.Fatalf("an offset in event bytes must be refused at started_at: %v", err)
	}
	if _, err := admit(15, packet); err != nil {
		t.Fatalf("invocation.start must admit since U12: %v", err)
	}
	prefix, err := store.ReadPrefix(project)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reduce.Replay(prefix)
	if err != nil || len(prefix) != 3 || len(snapshot.Invocations()) != 1 {
		t.Fatalf("admitted invocation did not replay: bundles=%d error=%v", len(prefix), err)
	}
	admitted := snapshot.Invocations()[0].Start.StartedAt
	if admitted.Location() != time.UTC || !admitted.Equal(env.StartedAt) {
		t.Fatalf("the ledger must hold the same instant in UTC: %s", admitted.Format(time.RFC3339Nano))
	}
	if snapshot.Watermark().RecordedAt.Location() != time.UTC {
		t.Fatal("the actually admitted watermark is not UTC")
	}
}
