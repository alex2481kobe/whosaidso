// Hold identity across task changes, packet ordering and handbacks belongs here.
// Production repairs and unrelated acceptance checks do not. Fixtures use TempDir.
package acceptance_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
	"whosaidso/internal/write"
)

type holdIdentityWorld struct {
	*gateVerifyFixture
	actor   model.Actor
	task    *model.TaskCreate
	ref     model.RecordRef
	hold    *model.BlockerHold
	witness model.ArtifactRef
	prefix  []model.Bundle
}

func holdIdentityCode(err error) string {
	if err == nil {
		return ""
	}
	var conflict *reduce.Conflict
	if errors.As(err, &conflict) {
		return conflict.Code()
	}
	if code := recCode(err); code != "" {
		return code
	}
	return err.Error()
}

func holdIdentityNew(t *testing.T) *holdIdentityWorld {
	t.Helper()
	root := t.TempDir()
	t.Setenv(store.HomeEnv, filepath.Join(t.TempDir(), "hold-identity-home"))
	t.Setenv(store.NoCacheEnv, "1")
	f := &gateVerifyFixture{t: t, p: store.Project{ID: recProject, Root: root, Ledger: filepath.Join(root, ".whosaidso", "events")}, n: 200}
	w := &holdIdentityWorld{gateVerifyFixture: f, actor: model.Actor{ID: "holder"}}
	w.task = &model.TaskCreate{ID: f.id(), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}}, Spec: reduceSpec(1)}
	w.ref = model.RecordRef{Project: f.p.ID, RecordID: w.task.ID, Revision: 1}
	w.hold = &model.BlockerHold{Task: w.ref, BlockerID: f.id(), Reason: model.BlockerResume, Actor: w.actor, Criterion: "owner permits resumption"}
	body := []byte(`{"ruling":"resolved","delivery":"complete"}`)
	gateVerifyPut(t, filepath.Join(root, "hold-identity-witness.json"), body)
	w.witness = gateVerifyContent(body, "hold-identity-witness.json")
	w.add(w.task)
	return w
}

func (w *holdIdentityWorld) add(events ...model.TypedEvent) {
	w.t.Helper()
	b, err := w.admit(w.actor, w.actor, events...)
	if err != nil {
		w.t.Fatalf("control admission: %v", err)
	}
	w.prefix = append(w.prefix, b)
}

func (w *holdIdentityWorld) amend(rev model.Revision) *model.TaskAmend {
	r := w.ref
	r.Revision = rev
	return &model.TaskAmend{Target: r, Replacement: w.task.Spec, Provenance: w.task.Provenance}
}

func (w *holdIdentityWorld) clear(rev model.Revision) *model.BlockerClear {
	r := w.ref
	r.Revision = rev
	return &model.BlockerClear{Task: r, BlockerID: w.hold.BlockerID, HoldRef: model.BlockerRef{Task: r, BlockerID: w.hold.BlockerID}, ResolvingWitness: w.witness}
}

func (w *holdIdentityWorld) close(rev model.Revision) *model.TaskClose {
	r := w.ref
	r.Revision = rev
	return &model.TaskClose{Task: r, Outcome: model.ClosureSuccess,
		AcceptanceWitnessRefs: []model.AcceptanceWitness{{CriterionID: w.task.Spec.AcceptanceCriteria[0].ID, CriterionRevision: 1, WitnessRef: w.witness}}, DeliveryWitnessRefs: []model.ArtifactRef{w.witness}}
}

func (w *holdIdentityWorld) replay(events []model.TypedEvent, want string) reduce.Snapshot {
	w.t.Helper()
	raw := []model.Event{}
	for _, e := range events {
		raw = append(raw, recEncode(w.t, e))
	}
	p := model.Packet{Version: model.WireVersion, Project: w.p.ID, CommandID: w.id(), RequestDigest: model.HashBytes([]byte("hold-identity-packet")), Author: w.actor, CapturedAt: time.Now().UTC(), Events: raw}
	data, err := model.Encode(p)
	if err != nil {
		w.t.Fatal(err)
	}
	prior := w.prefix[len(w.prefix)-1]
	b := model.Bundle{Version: model.WireVersion, Project: w.p.ID, Sequence: prior.Sequence + 1, CommandID: w.id(), Predecessor: prior.CommandID, RequestDigest: model.HashBytes([]byte("hold-identity-bundle")), Admitter: w.actor, RecordedAt: p.CapturedAt.Add(time.Second), Packets: []model.PacketRef{{CommandID: p.CommandID, Digest: model.HashBytes(data)}}}
	b.Events = reduceReview(w.t, w.actor, b.Packets, []model.Packet{p})
	s, err := reduce.Replay(append(append([]model.Bundle{}, w.prefix...), b))
	if holdIdentityCode(err) != want {
		w.t.Errorf("ledger-only replay: want %q, got %v", want, err)
	}
	return s
}

// check and admit consume the same packets; replay independently checks the
// intended ledger order, which is not necessarily the packets' capture order.
func (w *holdIdentityWorld) probe(groups [][]model.TypedEvent, replay []model.TypedEvent, want string) {
	w.t.Helper()
	w.replay(replay, want)
	ids := []model.ID{}
	for _, group := range groups {
		raw := []model.Event{}
		for _, e := range group {
			raw = append(raw, recEncode(w.t, e))
		}
		ids = append(ids, w.capture(w.actor, raw...).CommandID)
	}
	before := gateVerifyLedger(w.t, w.p)
	check, err := write.CheckAdmission(context.Background(), w.p, ids, nil, w.actor)
	if err != nil {
		w.t.Fatal(err)
	}
	checked := ""
	if len(check.Refusals) > 0 {
		checked = holdIdentityCode(check.Refusals[0].Err)
	}
	if checked != want {
		w.t.Errorf("check admission: want %q, got %+v", want, check.Refusals)
	}
	if !reflect.DeepEqual(before, gateVerifyLedger(w.t, w.p)) {
		w.t.Fatal("dry run changed ledger")
	}
	b, err := write.Admit(context.Background(), w.p, write.AdmitRequest{CommandID: w.id(), PacketIDs: ids, Admitter: w.actor, Outcome: "accepted", Reason: "confirm hold identity"})
	if holdIdentityCode(err) != checked {
		w.t.Errorf("check/admit disagree: check %q, admit %v", checked, err)
	}
	if holdIdentityCode(err) != want {
		w.t.Errorf("admission: want %q, got %v", want, err)
	}
	if err != nil {
		if !reflect.DeepEqual(before, gateVerifyLedger(w.t, w.p)) {
			w.t.Error("refusal changed ledger")
		}
		return
	}
	w.prefix = append(w.prefix, b)
	if _, err := reduce.Replay(w.prefix); err != nil {
		w.t.Errorf("admitted ledger cannot replay: %v", err)
	}
}

// Proposals naming a superseded revision are stale by design (optimistic concurrency); the gate does not reorder packets to rescue them.
// Captured after the amendment, the r1 hold is refused as stale; admission,
// check admission and replay of the order admission applies (the clear waits
// for the amendment and the hold, otherwise capture order) agree.
func TestHoldIdentityAmendClearCaptureOrders(t *testing.T) {
	admitted := map[string]string{"HAC": "HAC", "HCA": "HAC", "AHC": "AHC", "ACH": "AHC", "CHA": "HAC", "CAH": "AHC"}
	for _, order := range []string{"HAC", "HCA", "AHC", "ACH", "CHA", "CAH"} {
		t.Run(order, func(t *testing.T) {
			w := holdIdentityNew(t)
			events := map[byte]model.TypedEvent{'H': w.hold, 'A': w.amend(1), 'C': w.clear(2)}
			groups := [][]model.TypedEvent{}
			for i := range order {
				groups = append(groups, []model.TypedEvent{events[order[i]]})
			}
			replay, want := []model.TypedEvent{}, ""
			for i := range admitted[order] {
				replay = append(replay, events[admitted[order][i]])
			}
			if admitted[order] != "HAC" {
				want = reduce.CodeRevisionConflict
			}
			w.probe(groups, replay, want)
		})
	}
	t.Run("one-packet", func(t *testing.T) {
		w := holdIdentityNew(t)
		events := []model.TypedEvent{w.hold, w.amend(1), w.clear(2)}
		w.probe([][]model.TypedEvent{events}, events, "")
	})
}

func TestHoldIdentityLifecycle(t *testing.T) {
	for _, variant := range []string{"clear-current", "clear-stale", "duplicate-clear", "reuse-cleared-id", "another-task", "same-id-other-task", "open-close", "cleared-close", "supersede", "supersede-current", "correction", "correction-current", "changed-contract", "claim-revise"} {
		t.Run(variant, func(t *testing.T) {
			w := holdIdentityNew(t)
			w.add(w.hold)
			w.add(w.amend(1))
			w.add(w.amend(2))
			events, want := []model.TypedEvent{w.clear(3)}, ""
			switch variant {
			case "clear-stale":
				events, want = []model.TypedEvent{w.clear(1)}, reduce.CodeRevisionConflict
			case "duplicate-clear":
				w.add(w.clear(3))
				want = reduce.CodeInvalidTransition
			case "reuse-cleared-id":
				w.add(w.clear(3))
				h := *w.hold
				h.Task.Revision = 3
				events, want = []model.TypedEvent{&h}, reduce.CodeDuplicateRecord
			case "another-task", "same-id-other-task", "supersede", "supersede-current":
				other := *w.task
				other.ID = w.id()
				w.add(&other)
				r := w.ref
				r.RecordID = other.ID
				if variant == "another-task" {
					c := w.clear(1)
					c.Task, c.HoldRef.Task = r, r
					events, want = []model.TypedEvent{c}, reduce.CodeUnknownReference
				} else if variant == "same-id-other-task" {
					h := *w.hold
					h.Task = r
					w.add(&h)
				} else {
					prior := w.ref
					if variant == "supersede-current" {
						prior.Revision = 3
					}
					w.add(&model.Supersede{Prior: prior, Replacement: r, Reason: "replacement task"})
				}
			case "correction", "correction-current":
				r := w.ref
				if variant == "correction-current" {
					r.Revision = 3
				}
				w.add(&model.Correction{Target: model.CorrectionTarget{Kind: "record", Record: &r}, AffectedRevisions: []model.RecordRef{r}, Reason: "original scope was incorrect", CorrectiveRef: w.witness})
			case "changed-contract":
				a := w.amend(3)
				a.Replacement = reduceSpec(2)
				a.Replacement.Intent = "deliver a different contract"
				w.add(a)
				events = []model.TypedEvent{w.clear(4)}
			case "claim-revise":
				c := w.claim(w.actor)
				w.add(c)
				r := w.ref
				r.RecordID = c.ID
				w.add(&model.ClaimRevise{Target: r, Provenance: c.Provenance, Replacement: c.Spec})
			case "open-close":
				events, want = []model.TypedEvent{w.close(3)}, reduce.CodeInvalidTransition
			case "cleared-close":
				events = append(events, w.close(3))
			}
			w.probe([][]model.TypedEvent{events}, events, want)
			if want == "" {
				s := reduceReplay(t, w.prefix...)
				b, ok := s.Hold(w.clear(3).HoldRef)
				if !ok || b.Open() || b.TaskRevision != 1 {
					t.Errorf("original hold identity lost: %+v %v", b, ok)
				}
				for _, task := range s.Tasks() {
					if variant == "same-id-other-task" && task.Task.ID != w.task.ID && (len(task.Blockers) != 1 || !task.Blockers[0].Open()) {
						t.Error("clearing this task changed the other task's same-id hold")
					}
				}
			}
		})
	}
}

func TestHoldIdentityHandbackAcrossAmendment(t *testing.T) {
	for _, outcome := range []model.AttemptOutcome{model.AttemptBlockedMidTask, model.AttemptOutOfScope} {
		for _, split := range []bool{false, true} {
			for _, variant := range []string{"amended-before-handback", "hold-receipt-amend", "hold-amend-receipt", "receipt-amend-hold", "hold-amend-receipt-clear"} {
				t.Run(fmt.Sprintf("%s/%s/split-%t", outcome, variant, split), func(t *testing.T) {
					w := holdIdentityNew(t)
					attempt := w.id()
					w.add(&model.TaskStart{Task: w.ref, Actor: w.actor, AttemptID: attempt})
					receipt := &model.AttemptTerminal{Task: w.ref, AttemptID: attempt, Outcome: outcome, Reason: "work cannot continue", NextAction: "owner resolves hold", DeliveryRefs: []model.ArtifactRef{}}
					hold := *w.hold
					events, replay, want := []model.TypedEvent{}, []model.TypedEvent{}, ""
					if variant == "amended-before-handback" {
						w.add(w.amend(1))
						events = []model.TypedEvent{receipt, &hold}
						r := *receipt
						r.Task.Revision = 2
						h := hold
						h.Task.Revision = 2
						replay = []model.TypedEvent{&r, &h}
					} else {
						switch variant {
						case "hold-receipt-amend":
							events = []model.TypedEvent{&hold, receipt, w.amend(1)}
						case "hold-amend-receipt", "hold-amend-receipt-clear":
							events = []model.TypedEvent{&hold, w.amend(1), receipt}
						case "receipt-amend-hold":
							events = []model.TypedEvent{receipt, w.amend(1), &hold}
						}
						if variant == "hold-amend-receipt-clear" {
							events = append(events, w.clear(2))
							want = reduce.CodeMissingHold
						}
						replay = append(replay, events...)
						if variant == "receipt-amend-hold" {
							h := hold
							h.Task.Revision = 2
							replay[2] = &h
						}
						if variant == "hold-amend-receipt" || variant == "hold-amend-receipt-clear" {
							r := *receipt
							r.Task.Revision = 2
							replay[2] = &r
						}
						if outcome == model.AttemptOutOfScope {
							want = reduce.CodeInvalidTransition
						}
					}
					groups := [][]model.TypedEvent{events}
					if split {
						groups = nil
						for _, e := range events {
							groups = append(groups, []model.TypedEvent{e})
						}
					}
					w.probe(groups, replay, want)
					if want == "" && !t.Failed() {
						w.add(w.amend(2))
						events = []model.TypedEvent{w.clear(3), w.close(3)}
						w.probe([][]model.TypedEvent{events}, events, "")
					}
				})
			}
		}
	}
}

func TestHoldIdentityTakeoverKeepsAmendedHold(t *testing.T) {
	for _, variant := range []string{"live", "terminal-open", "terminal-cleared", "stale"} {
		t.Run(variant, func(t *testing.T) {
			w := holdIdentityNew(t)
			attempt := w.id()
			w.add(&model.TaskStart{Task: w.ref, Actor: w.actor, AttemptID: attempt})
			if variant != "live" && variant != "stale" {
				w.add(&model.AttemptTerminal{Task: w.ref, AttemptID: attempt, Outcome: model.AttemptStopped, Reason: "writer stopped", NextAction: "resolve hold", DeliveryRefs: []model.ArtifactRef{}})
			}
			w.add(w.hold)
			w.add(w.amend(1))
			r := w.ref
			r.Revision = 2
			want := ""
			if variant == "terminal-open" {
				want = reduce.CodeInvalidTransition
			}
			if variant == "terminal-cleared" {
				w.add(w.clear(2))
			}
			if variant == "stale" {
				r.Revision = 1
				want = reduce.CodeRevisionConflict
			}
			events := []model.TypedEvent{&model.TaskTakeover{Task: r, Actor: w.actor, PriorAttemptID: attempt, AttemptID: w.id(), StoppedConfirmationRef: w.witness}}
			w.probe([][]model.TypedEvent{events}, events, want)
			s := reduceReplay(t, w.prefix...)
			h, ok := s.Hold(w.clear(2).HoldRef)
			if !ok || h.Open() != (variant != "terminal-cleared") {
				t.Errorf("takeover lost or changed hold: %+v %v", h, ok)
			}
		})
	}
}
