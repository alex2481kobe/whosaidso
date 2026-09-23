package query

// Describe-once task buckets: each status bucket is exactly the current tasks a
// one-by-one description files under that status, in record order, and the
// slices handed to answer sections never share a backing array.

import (
	"reflect"
	"testing"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

func TestTaskBucketsMatchDescribingEachTask(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	appendEvents(t, p, 101, testTask(2), testTask(3), testTask(4), testTask(5), testTask(6),
		&model.BlockerHold{Task: testRef(2, 1), BlockerID: testID(80), Reason: model.BlockerResume,
			Actor: model.Actor{ID: "owner"}, Criterion: "resume is authorized"},
		&model.TaskStart{Task: testRef(5, 1), AttemptID: testID(71), Actor: model.Actor{ID: "worker"}},
		&model.TaskStart{Task: testRef(6, 1), AttemptID: testID(72), Actor: model.Actor{ID: "worker"}})
	appendEvents(t, p, 102, &model.AttemptTerminal{Task: testRef(5, 1), AttemptID: testID(71), Outcome: model.AttemptSuccess,
		Reason: "done", NextAction: "owner accepts or rejects", DeliveryRefs: []model.ArtifactRef{testArtifact()}})
	prefix, err := store.ReadPrefix(p)
	if err != nil {
		t.Fatal(err)
	}
	s, err := reduce.Replay(prefix)
	if err != nil {
		t.Fatal(err)
	}
	buckets := describeTasks(s)
	statuses := []reduce.TaskStatus{reduce.StatusReady, reduce.StatusBlocked, reduce.StatusInFlight, reduce.StatusClosed}
	seen := 0
	for _, status := range statuses {
		want := []Record{}
		for _, fact := range currentOf(s, model.Task) {
			if r := describe(s, fact); r.Task.Status == status {
				want = append(want, r)
			}
		}
		got := buckets.with(status)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s bucket differs from describing each task:\ngot  %+v\nwant %+v", status, got, want)
		}
		seen += len(got)
	}
	if seen != len(currentOf(s, model.Task)) || len(buckets.with(reduce.StatusBlocked)) != 2 || len(buckets.with(reduce.StatusInFlight)) != 1 {
		t.Fatalf("buckets do not partition the current tasks: %d filed", seen)
	}
	// Each preset section reads its own status, not a neighbour's.
	state, now := statePreset(s, buckets), nowPreset(s, buckets)
	if !reflect.DeepEqual(*state.Closed, buckets.with(reduce.StatusClosed)) || !reflect.DeepEqual(*now.InFlight, buckets.with(reduce.StatusInFlight)) {
		t.Fatalf("STATE closed %d or NOW in flight %d read the wrong bucket", len(*state.Closed), len(*now.InFlight))
	}
	// Sections own their slices: growing or truncating one leaves the next intact.
	first := buckets.with(reduce.StatusBlocked)
	first[0] = Record{}
	_ = append(first[:1], Record{})
	if second := buckets.with(reduce.StatusBlocked); second[0].Fact.Key.ID == "" || second[1].Fact.Key.ID == "" {
		t.Fatal("one section's writes reached another section through a shared bucket")
	}
}
