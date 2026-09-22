package write

import (
	"context"
	"time"
	"unicode/utf8"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

// HandbackRequest contains authored meaning, never an inferred outcome. Author
// identifies the speaker; it is not inferred from the attempt's holder. CommandID
// and hold identities can be supplied for exact retries, including after admission.
// Envelope is only an optional source of attempt/project identity, not a reading.
type HandbackRequest struct {
	CommandID          model.ID
	Author             model.Actor
	AttemptID          model.ID
	Envelope           *model.InvocationEnvelope
	Outcome            model.AttemptOutcome
	Reason             string
	NextAction         string
	DeliveryRefs       []model.ArtifactRef
	CommitsDenied      bool
	ReconciliationOwed bool
	Holds              []HandbackHold
}

// HandbackHold preserves the author's assignment and discharge criterion. A
// resume hold on an out-of-scope receipt proposes reassignment without amending
// the task's scope. No reassignment target or hold criterion is synthesized.
type HandbackHold struct {
	BlockerID model.ID
	Reason    model.BlockerReason
	Actor     model.Actor
	Criterion string
}

// Handback captures only: an admission failure must never erase the receipt.
// WriteIntake fsyncs packet bytes and directories, publishes without replacement,
// and syncs the parent before acknowledging. A cancelled work context cannot
// suppress this final capture; a separate bounded context matches Run's seal.
// Invalid requests and storage failures return errors, never a durability claim.
func Handback(ctx context.Context, project store.Project, r HandbackRequest) (model.PacketRef, error) {
	// Like the model codec's strictUnmarshal, check the original bytes before
	// encoding/json can replace them; CLI handbacks share this boundary.
	for _, field := range []struct{ path, value string }{
		{"reason", r.Reason}, {"next_action", r.NextAction},
		{"author.id", r.Author.ID}, {"author.unknown_reason", r.Author.UnknownReason},
	} {
		if !utf8.Valid([]byte(field.value)) {
			return model.PacketRef{}, admissionFault("invalid-field", field.path, "input contains invalid UTF-8")
		}
	}
	if r.Envelope != nil {
		if r.Envelope.ExecutionSourceIdentity.Project != project.ID || r.AttemptID != "" && r.AttemptID != r.Envelope.AttemptID {
			return model.PacketRef{}, admissionFault("invalid-field", "envelope", "envelope must identify the same project and attempt")
		}
		r.AttemptID = r.Envelope.AttemptID
	}
	prefix, err := store.ReadPrefix(project)
	if err != nil {
		return model.PacketRef{}, err
	}
	snapshot, err := reduce.Replay(prefix)
	if err != nil {
		return model.PacketRef{}, err
	}
	var task model.RecordRef
	for _, projection := range snapshot.Tasks() {
		for _, attempt := range projection.Attempts {
			if attempt.Key.Attempt == r.AttemptID {
				task = model.RecordRef{Project: project.ID, RecordID: attempt.Key.Task, Revision: attempt.TaskRevision}
			}
		}
	}
	if task.RecordID == "" {
		return model.PacketRef{}, admissionFault("unknown-reference", "attempt_id", "handback needs an admitted attempt")
	}
	if r.DeliveryRefs == nil {
		r.DeliveryRefs = []model.ArtifactRef{}
	}
	terminal, err := model.EncodeEvent(&model.AttemptTerminal{
		Task: task, AttemptID: r.AttemptID, Outcome: r.Outcome, Reason: r.Reason,
		NextAction: r.NextAction, DeliveryRefs: r.DeliveryRefs,
		CommitsDenied: r.CommitsDenied, ReconciliationOwed: r.ReconciliationOwed,
	})
	if err != nil {
		return model.PacketRef{}, err
	}
	events := []model.Event{terminal}
	for _, hold := range r.Holds {
		event, err := model.EncodeEvent(&model.BlockerHold{Task: task, BlockerID: hold.BlockerID, Reason: hold.Reason, Actor: hold.Actor, Criterion: hold.Criterion})
		if err != nil {
			return model.PacketRef{}, err
		}
		events = append(events, event)
	}
	capture, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return store.WriteIntake(capture, project, store.IntakeRequest{CommandID: r.CommandID, Author: r.Author, Events: events})
}

// gateHandbacks runs under Admit's transaction lock after packet ordering. Only
// clerical task revisions change in the proposal; immutable packets remain the
// authored record and the bundle's packet digest binds their original bytes.
func gateHandbacks(base reduce.Snapshot, packets []model.Packet) ([]model.Packet, error) {
	attempts := map[model.ID]reduce.Attempt{}
	revisions := map[reduce.Ident]model.Revision{}
	for _, task := range base.Tasks() {
		revisions[reduce.Ident{Project: task.Task.Project, ID: task.Task.ID}] = task.Task.Revision
		for _, attempt := range task.Attempts {
			attempts[attempt.Key.Attempt] = attempt
		}
	}
	var terminals []*model.AttemptTerminal
	var holds []*model.BlockerHold
	cleared := map[gateKey]bool{}
	amended := map[reduce.Ident]bool{}
	// Original refs associate bundled holds with receipts, including a hold in
	// a separate packet. Otherwise a mid-flight amendment would stale its hold.
	originals := map[model.RecordRef]bool{}
	for _, packet := range packets {
		for _, raw := range packet.Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				return nil, err
			}
			if terminal, ok := event.(*model.AttemptTerminal); ok {
				originals[terminal.Task] = true
			}
		}
	}
	for i, packet := range packets {
		packets[i].Events = append([]model.Event(nil), packet.Events...)
		for j, raw := range packet.Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				return nil, err
			}
			rewrite := false
			switch e := event.(type) {
			case *model.TaskCreate:
				revisions[reduce.Ident{Project: packet.Project, ID: e.ID}] = 1
			case *model.TaskAmend:
				who := reduce.Ident{Project: e.Target.Project, ID: e.Target.RecordID}
				revisions[who], amended[who] = e.Target.Revision+1, true
			case *model.TaskStart:
				attempts[e.AttemptID] = reduce.Attempt{Key: reduce.AttemptKey{Project: e.Task.Project, Task: e.Task.RecordID, Attempt: e.AttemptID}, TaskRevision: e.Task.Revision, Actor: e.Actor}
			case *model.TaskTakeover:
				if !model.SameActor(e.Actor, packet.Author) {
					return nil, admissionFault("attribution-mismatch", "actor", "takeover actor must match the packet author")
				}
				attempts[e.AttemptID] = reduce.Attempt{Key: reduce.AttemptKey{Project: e.Task.Project, Task: e.Task.RecordID, Attempt: e.AttemptID}, TaskRevision: e.Task.Revision, Actor: e.Actor}
			case *model.AttemptTerminal:
				a, ok := attempts[e.AttemptID]
				if !ok || a.Key.Project != e.Task.Project || a.Key.Task != e.Task.RecordID {
					return nil, admissionFault("unknown-reference", "attempt_id", "receipt must name its own task and attempt")
				}
				if !model.SameActor(a.Actor, packet.Author) {
					return nil, admissionFault("attribution-mismatch", "author", "receipt must be authored by the attempt holder")
				}
				if e.Task.Revision != a.TaskRevision {
					return nil, admissionFault("revision-conflict", "task", "receipt must carry the revision the attempt started against")
				}
				// RULED (owner, R8.2, 2026-09-22): a handback carries the task
				// revision the attempt STARTED against, and is admitted against the
				// CURRENT revision. Both are recorded: intake + attempt retain the
				// starting contract; this bundle records the admission revision.
				// Work actually done was against the old contract. Rejecting it for
				// another person's amendment punishes the lane and pressures it to
				// backdate receipts. U05 still requires CURRENT and rejects stale
				// events unchanged; this translation is the ruled exception.
				current := revisions[reduce.Ident{Project: e.Task.Project, ID: e.Task.RecordID}]
				rewrite, e.Task.Revision = current != e.Task.Revision, current
				terminals = append(terminals, e)
			case *model.BlockerHold:
				if originals[e.Task] {
					current := revisions[reduce.Ident{Project: e.Task.Project, ID: e.Task.RecordID}]
					rewrite, e.Task.Revision = current != e.Task.Revision, current
				}
				holds = append(holds, e)
			case *model.BlockerClear:
				cleared[gateKey{Record: model.RecordRef{Project: e.Task.Project, RecordID: e.Task.RecordID}, Blocker: e.BlockerID}] = true
			}
			if rewrite {
				packets[i].Events[j], err = model.EncodeEvent(event)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	for _, terminal := range terminals {
		if terminal.Outcome != model.AttemptBlockedMidTask && terminal.Outcome != model.AttemptOutOfScope {
			continue // success/reconciliation queues are already derived by U05
		}
		who := reduce.Ident{Project: terminal.Task.Project, ID: terminal.Task.RecordID}
		if terminal.Outcome == model.AttemptOutOfScope && amended[who] {
			return nil, admissionFault("invalid-transition", "task.amend", "out-of-scope handback cannot amend its task scope in the same bundle")
		}
		found := false
		for _, hold := range holds {
			key := gateKey{Record: model.RecordRef{Project: hold.Task.Project, RecordID: hold.Task.RecordID}, Blocker: hold.BlockerID}
			if hold.Task == terminal.Task && !cleared[key] && (terminal.Outcome != model.AttemptOutOfScope || hold.Reason == model.BlockerResume) {
				found = true
			}
		}
		if !found {
			return nil, admissionFault("missing-hold", "attempt.terminal", "blocked-mid-task needs a bundled open hold; out-of-scope needs an authored resume/reassignment hold")
		}
	}
	return packets, nil
}
