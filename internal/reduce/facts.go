package reduce

// Admitted fact types, their keys, and ledger origins live here.
// State transitions and snapshot queries do not.

import (
	"time"

	"datum/internal/model"
)

// ---- identities ----------------------------------------------------------

// Ident is a record or attempt identity without a revision. Revisions accrue
// and identity does not, so the two are never the same key.
type Ident struct {
	Project model.ProjectID `json:"project"`
	ID      model.ID        `json:"id"`
}

// RecordKey addresses exactly one admitted revision of one record.
type RecordKey struct {
	Project  model.ProjectID `json:"project"`
	ID       model.ID        `json:"id"`
	Revision model.Revision  `json:"revision"`
}

// AttemptKey addresses one attempt under one task. Attempts are subordinate
// data, not a fifth record kind.
type AttemptKey struct {
	Project model.ProjectID `json:"project"`
	Task    model.ID        `json:"task"`
	Attempt model.ID        `json:"attempt"`
}

// BlockerKey addresses one hold under one task.
type BlockerKey struct {
	Project model.ProjectID `json:"project"`
	Task    model.ID        `json:"task"`
	Blocker model.ID        `json:"blocker"`
}

// InvocationKey addresses one start/seal pair.
type InvocationKey struct {
	Project      model.ProjectID `json:"project"`
	InvocationID model.ID        `json:"invocation_id"`
}

// CriterionKey carries the claim revision as well as the criterion revision,
// because the two revise independently and a criterion belongs to one assertion
// revision. Collapsing them would let a revised claim inherit an old predicate.
type CriterionKey struct {
	Project           model.ProjectID `json:"project"`
	Claim             model.ID        `json:"claim"`
	ClaimRevision     model.Revision  `json:"claim_revision"`
	CriterionID       model.ID        `json:"criterion_id"`
	CriterionRevision model.Revision  `json:"criterion_revision"`
}

// ReviewKey addresses one admitted disposition of one intake packet.
type ReviewKey struct {
	Project   model.ProjectID `json:"project"`
	CommandID model.ID        `json:"command_id"`
}

// SourceKey addresses one captured source.
type SourceKey struct {
	Project model.ProjectID `json:"project"`
	Source  model.ID        `json:"source"`
}

func ident(r model.RecordRef) Ident { return Ident{Project: r.Project, ID: r.RecordID} }

func recordKey(r model.RecordRef) RecordKey {
	return RecordKey{Project: r.Project, ID: r.RecordID, Revision: r.Revision}
}

func criterionKey(r model.CriterionRef) CriterionKey {
	return CriterionKey{
		Project:           r.Claim.Project,
		Claim:             r.Claim.RecordID,
		ClaimRevision:     r.Claim.Revision,
		CriterionID:       r.CriterionID,
		CriterionRevision: r.Revision,
	}
}

func invocationKey(r model.InvocationRef) InvocationKey {
	return InvocationKey{Project: r.Project, InvocationID: r.InvocationID}
}

func blockerKey(r model.BlockerRef) BlockerKey {
	return BlockerKey{Project: r.Task.Project, Task: r.Task.RecordID, Blocker: r.BlockerID}
}

// ---- admitted facts ------------------------------------------------------

// Origin locates a fact in the ledger. Every derived answer can name the bundle
// and event it came from, which is what makes a wrong answer findable.
type Origin struct {
	Sequence   uint64 `json:"sequence"`
	EventIndex int    `json:"event_index"`
}

func (o Origin) before(other Origin) bool {
	if o.Sequence != other.Sequence {
		return o.Sequence < other.Sequence
	}
	return o.EventIndex < other.EventIndex
}

// Record is one admitted revision of one authored record. Exactly one spec
// pointer is set, chosen by Kind. The specs are shared with the snapshot and
// must be treated as read-only: Go cannot enforce that, and deep-copying every
// spec on every read would cost more than the guarantee is worth here.
type Record struct {
	Key        RecordKey             `json:"key"`
	Kind       model.Kind            `json:"kind"`
	Provenance model.Provenance      `json:"provenance"`
	Origin     Origin                `json:"origin"`
	Task       *model.TaskSpec       `json:"task"`
	Claim      *model.ClaimSpec      `json:"claim"`
	Decision   *model.DecisionSpec   `json:"decision"`
	Instrument *model.InstrumentSpec `json:"instrument"`
}

// Terminal is an attempt's one receipt. An attempt has at most one, so a second
// receipt is a refusal rather than an overwrite.
type Terminal struct {
	Outcome            model.AttemptOutcome `json:"outcome"`
	Reason             string               `json:"reason"`
	NextAction         string               `json:"next_action"`
	DeliveryRefs       []model.ArtifactRef  `json:"delivery_refs"`
	CommitsDenied      bool                 `json:"commits_denied"`
	ReconciliationOwed bool                 `json:"reconciliation_owed"`
	Origin             Origin               `json:"origin"`
}

// Attempt is one admitted start or takeover. A takeover mints a new attempt and
// leaves the prior one exactly as it was: erasing it would hide that two writers
// were in the task, which is the failure takeover exists to record.
type Attempt struct {
	Key          AttemptKey     `json:"key"`
	TaskRevision model.Revision `json:"task_revision"`
	Actor        model.Actor    `json:"actor"`
	Takeover     bool           `json:"takeover"`
	PriorAttempt model.ID       `json:"prior_attempt"`
	Started      Origin         `json:"started"`
	Terminal     *Terminal      `json:"terminal"`
}

// Live reports whether this attempt has no terminal receipt. Holder identity is
// not evidence a process lives, and this deliberately does not ask.
func (a Attempt) Live() bool { return a.Terminal == nil }

// Blocker is one hold and its clearance. A cleared hold stays in the snapshot,
// because "this was blocked and got unblocked" is a fact queries need.
type Blocker struct {
	Key          BlockerKey          `json:"key"`
	TaskRevision model.Revision      `json:"task_revision"`
	Reason       model.BlockerReason `json:"reason"`
	Actor        model.Actor         `json:"actor"`
	Criterion    string              `json:"criterion"`
	Held         Origin              `json:"held"`
	Cleared      *Origin             `json:"cleared"`
	Witness      *model.ArtifactRef  `json:"witness"`
}

// Open reports whether the hold is still owed.
func (b Blocker) Open() bool { return b.Cleared == nil }

// Closure is one admitted authorised closure. It is retained even when it does
// not take effect, because an unwitnessed success closure is a fact a reader
// must see rather than an event that silently vanished.
type Closure struct {
	Task                model.RecordRef           `json:"task"`
	Outcome             model.ClosureOutcome      `json:"outcome"`
	Authority           model.Authority           `json:"authority"`
	AcceptanceWitnesses []model.AcceptanceWitness `json:"acceptance_witnesses"`
	DeliveryWitnesses   []model.ArtifactRef       `json:"delivery_witnesses"`
	Origin              Origin                    `json:"origin"`
}

// Invocation pairs an immutable pre-launch intent with its seal, if one was
// admitted. An unsealed invocation is an unknown terminal state, not a failure.
type Invocation struct {
	Key     InvocationKey             `json:"key"`
	Attempt AttemptKey                `json:"attempt"`
	Start   model.InvocationEnvelope  `json:"start"`
	Started Origin                    `json:"started"`
	Seal    *model.InvocationEnvelope `json:"seal"`
	Sealed  *Origin                   `json:"sealed"`
}

// Review is one admitted disposition of one intake packet.
type Review struct {
	Key     ReviewKey       `json:"key"`
	Packet  model.PacketRef `json:"packet"`
	Outcome string          `json:"outcome"`
	Actor   model.Actor     `json:"actor"`
	Reason  string          `json:"reason"`
	Origin  Origin          `json:"origin"`
	// SelfAdmission is computed from Author and Actor (C39), never read from
	// the review's stored legacy field.
	SelfAdmission model.SelfAdmissionState `json:"self_admission"`
	// Author is the Actor this packet was captured with, or an unknown Actor
	// when a legacy review did not record it (R10.1 revised).
	Author model.Actor `json:"author"`
	// Invocations are the invocation facts this packet carried when it was not
	// accepted (R10.3). Empty for accepted packets.
	Invocations []model.ReviewedInvocation `json:"invocations"`
}

// Source is one captured original.
type Source struct {
	Key    SourceKey          `json:"key"`
	Intake model.SourceIntake `json:"intake"`
	Origin Origin             `json:"origin"`
}

// Criterion is one frozen executable predicate at its own revision.
type Criterion struct {
	Key    CriterionKey       `json:"key"`
	Fix    model.CriterionFix `json:"fix"`
	Origin Origin             `json:"origin"`
	// RecordedAt is when the bundle that fixed it was recorded: the ledger's
	// own time, never an author's. During admission's validation replay the
	// candidate bundle's time is synthetic, so only prior bundles' times are
	// observations.
	RecordedAt time.Time `json:"recorded_at"`
}

// Referrer is one reverse edge: an admitted event that named a referent. U06
// expands correction and supersession through these, and U13 traverses them.
type Referrer struct {
	Origin Origin          `json:"origin"`
	Type   model.EventType `json:"type"`
	Path   string          `json:"path"`
}

// Deferred preserves the diagnostic vocabulary for callers of the partial fold.
// Every event in the closed model vocabulary now has a semantic route.
type Deferred struct {
	Origin Origin          `json:"origin"`
	Type   model.EventType `json:"type"`
}

// Watermark is the selected ledger position, kept out of the keyed maps because
// it describes the read, not a record.
type Watermark struct {
	Sequence   uint64    `json:"sequence"`
	CommandID  model.ID  `json:"command_id"`
	RecordedAt time.Time `json:"recorded_at"`
	Bundles    int       `json:"bundles"`
	Events     int       `json:"events"`
}
