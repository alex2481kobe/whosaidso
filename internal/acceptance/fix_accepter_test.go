// The named-accepter bypass (principles review 2026-09-24, item 1) belongs
// here: an amendment that removes or changes a task's accepter, followed by a
// closure the original accepter never wrote, through admission and through
// ledger-only replay. Other acceptance specifications do not.
package acceptance_test

import (
	"context"
	"path/filepath"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
)

func fixAccepterNew(t *testing.T) *gateVerifyFixture {
	t.Helper()
	root := t.TempDir()
	t.Setenv(store.HomeEnv, filepath.Join(t.TempDir(), "machine"))
	pvPut(t, root, "whosaidso.toml", []byte("id = 'datum/acceptance'\nledger = '.whosaidso/events'\n"))
	if _, err := store.Bind(context.Background(), root, root); err != nil {
		t.Fatal(err)
	}
	p, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return &gateVerifyFixture{t: t, p: p, n: 300}
}

// fixAccepterTask admits a task naming reviewer as its accepter, authored by lane.
func fixAccepterTask(t *testing.T, f *gateVerifyFixture, lane, reviewer model.Actor) (model.RecordRef, model.TaskSpec) {
	t.Helper()
	spec := laneEReduceSpec(1)
	spec.Accepter = &reviewer
	create := &model.TaskCreate{ID: f.id(), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}}, Spec: spec}
	if _, err := f.admit(lane, lane, create); err != nil {
		t.Fatalf("control: a task naming its accepter must admit: %v", err)
	}
	return model.RecordRef{Project: f.p.ID, RecordID: create.ID, Revision: 1}, spec
}

func fixAccepterAmend(ref model.RecordRef, author model.Actor, replacement model.TaskSpec) *model.TaskAmend {
	return &model.TaskAmend{Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}},
		Target: ref, Replacement: replacement}
}

func TestFixAccepterAmendmentCannotRemoveTheAccepterThenClose(t *testing.T) {
	lane, reviewer := model.Actor{ID: "lane-b-audit"}, model.Actor{ID: "lane-b-reviewer"}
	for _, change := range []string{"remove", "replace"} {
		t.Run(change, func(t *testing.T) {
			f := fixAccepterNew(t)
			ref, spec := fixAccepterTask(t, f, lane, reviewer)
			replacement := spec
			replacement.Accepter = nil
			if change == "replace" {
				replacement.Accepter = &lane
			}
			if _, err := f.admit(lane, lane, fixAccepterAmend(ref, lane, replacement)); recCode(err) != reduce.CodeAccepterMismatch {
				t.Fatalf("expected an amendment by %s that would %s accepter %s to be refused (%s); got %v. Anyone who may amend could otherwise name no one and close the task",
					lane.ID, change, reviewer.ID, reduce.CodeAccepterMismatch, err)
			}
			// The accepter still stands: the lane cannot close the task either way.
			withdraw := &model.TaskClose{Task: ref, Outcome: model.ClosureWithdrawn,
				AcceptanceWitnessRefs: []model.AcceptanceWitness{}, DeliveryWitnessRefs: []model.ArtifactRef{}}
			if _, err := f.admit(lane, lane, withdraw); recCode(err) != reduce.CodeAccepterMismatch {
				t.Fatalf("expected the lane's closure of a task naming %s to be refused; got %v", reviewer.ID, err)
			}
			// Controls: another author may still amend what is not the accepter,
			// and the accepter may hand the task over itself.
			clearer := spec
			clearer.Intent = "preserve the admitted task obligation, stated more clearly"
			if _, err := f.admit(lane, lane, fixAccepterAmend(ref, lane, clearer)); err != nil {
				t.Fatalf("control: an amendment keeping the accepter must admit for any author: %v", err)
			}
			ref.Revision = 2
			if _, err := f.admit(reviewer, reviewer, fixAccepterAmend(ref, reviewer, replacement)); err != nil {
				t.Fatalf("control: the accepter itself may %s its accepter: %v", change, err)
			}
		})
	}
}

// Replay decides from the ledger alone: an admitted amendment in which the
// accepter handed over, re-attributed to another packet author, is refused.
func TestFixAccepterReplayRefusesAReattributedHandover(t *testing.T) {
	f := fixAccepterNew(t)
	lane, reviewer := model.Actor{ID: "lane-b-audit"}, model.Actor{ID: "lane-b-reviewer"}
	ref, spec := fixAccepterTask(t, f, lane, reviewer)
	replacement := spec
	replacement.Accepter = nil
	if _, err := f.admit(reviewer, reviewer, fixAccepterAmend(ref, reviewer, replacement)); err != nil {
		t.Fatalf("control: the accepter's own handover must admit: %v", err)
	}
	prefix, err := store.ReadPrefix(f.p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reduce.Replay(prefix); err != nil {
		t.Fatalf("control: the honest ledger must replay: %v", err)
	}
	last := &prefix[len(prefix)-1]
	for i, raw := range last.Events {
		e, err := model.DecodeEvent(raw)
		if err != nil {
			t.Fatal(err)
		}
		if review, ok := e.(*model.ReviewAdmit); ok {
			for packet := range review.Authors {
				review.Authors[packet] = lane
			}
			last.Events[i] = recEncode(t, review)
		}
	}
	if _, err := reduce.Replay(prefix); recCode(err) != reduce.CodeAccepterMismatch {
		t.Errorf("expected replay to refuse the accepter's removal by %s as admission would; got %v", lane.ID, err)
	}
}
