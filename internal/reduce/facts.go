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
	Project model.ProjectID
	ID      model.ID
}

// RecordKey addresses exactly one admitted revision of one record.
type RecordKey struct {
	Project  model.ProjectID
	ID       model.ID
	Revision model.Revision
}

// AttemptKey addresses one attempt under one task. Attempts are subordinate
// data, not a fifth record kind.
type AttemptKey struct {
	Project model.ProjectID
	Task    model.ID
	Attempt model.ID
}

// BlockerKey addresses one hold under one task.
type BlockerKey struct {
	Project model.ProjectID
	Task    model.ID
	Blocker model.ID
}

// InvocationKey addresses one start/seal pair.
type InvocationKey struct {
	Project      model.ProjectID
	InvocationID model.ID
}

// CriterionKey carries the claim revision as well as the criterion revision,
// because the two revise independently and a criterion belongs to one assertion
// revision. Collapsing them would let a revised claim inherit an old predicate.
type CriterionKey struct {
	Project           model.ProjectID
	Claim             model.ID
	ClaimRevision     model.Revision
	CriterionID       model.ID
	CriterionRevision model.Revision
}

// ReviewKey addresses one admitted disposition of one intake packet.
type ReviewKey struct {
	Project   model.ProjectID
	CommandID model.ID
}

// SourceKey addresses one captured source.
type SourceKey struct {
	Project model.ProjectID
	Source  model.ID
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
	Sequence   uint64
	EventIndex int
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
	Key        RecordKey
	Kind       model.Kind
	Provenance model.Provenance
	Origin     Origin
	Task       *model.TaskSpec
	Claim      *model.ClaimSpec
	Decision   *model.DecisionSpec
	Instrument *model.InstrumentSpec
}

// Terminal is an attempt's one receipt. An attempt has at most one, so a second
// receipt is a refusal rather than an overwrite.
type Terminal struct {
	Outcome            model.AttemptOutcome
	Reason             string
	NextAction         string
	DeliveryRefs       []model.ArtifactRef
	CommitsDenied      bool
	ReconciliationOwed bool
	Origin             Origin
}

// Attempt is one admitted start or takeover. A takeover mints a new attempt and
// leaves the prior one exactly as it was: erasing it would hide that two writers
// were in the task, which is the failure takeover exists to record.
type Attempt struct {
	Key          AttemptKey
	TaskRevision model.Revision
	Actor        model.Actor
	Takeover     bool
	PriorAttempt model.ID
	Started      Origin
	Terminal     *Terminal
}

// Live reports whether this attempt has no terminal receipt. Holder identity is
// not evidence a process lives, and this deliberately does not ask.
func (a Attempt) Live() bool { return a.Terminal == nil }

// Blocker is one hold and its clearance. A cleared hold stays in the snapshot,
// because "this was blocked and got unblocked" is a fact queries need.
type Blocker struct {
	Key          BlockerKey
	TaskRevision model.Revision
	Reason       model.BlockerReason
	Actor        model.Actor
	Criterion    string
	Held         Origin
	Cleared      *Origin
	Witness      *model.ArtifactRef
}

// Open reports whether the hold is still owed.
func (b Blocker) Open() bool { return b.Cleared == nil }

// Closure is one admitted authorised closure. It is retained even when it does
// not take effect, because an unwitnessed success closure is a fact a reader
// must see rather than an event that silently vanished.
type Closure struct {
	Task                model.RecordRef
	Outcome             model.ClosureOutcome
	Authority           model.Authority
	AcceptanceWitnesses []model.AcceptanceWitness
	DeliveryWitnesses   []model.ArtifactRef
	Origin              Origin
}

// Invocation pairs an immutable pre-launch intent with its seal, if one was
// admitted. An unsealed invocation is an unknown terminal state, not a failure.
type Invocation struct {
	Key     InvocationKey
	Attempt AttemptKey
	Start   model.InvocationEnvelope
	Started Origin
	Seal    *model.InvocationEnvelope
	Sealed  *Origin
}

// Review is one admitted disposition of one intake packet.
type Review struct {
	Key     ReviewKey
	Packet  model.PacketRef
	Outcome string
	Actor   model.Actor
	Reason  string
	Origin  Origin
	// SelfAdmission is computed from Author and Actor (C39), never read from
	// the review's stored legacy field.
	SelfAdmission model.SelfAdmissionState
	// Author is the Actor this packet was captured with, or an unknown Actor
	// when a legacy review did not record it (R10.1 revised).
	Author model.Actor
	// Invocations are the invocation facts this packet carried when it was not
	// accepted (R10.3). Empty for accepted packets.
	Invocations []model.ReviewedInvocation
}

// Source is one captured original.
type Source struct {
	Key    SourceKey
	Intake model.SourceIntake
	Origin Origin
}

// Criterion is one frozen executable predicate at its own revision.
type Criterion struct {
	Key    CriterionKey
	Fix    model.CriterionFix
	Origin Origin
	// RecordedAt is when the bundle that fixed it was recorded: the ledger's
	// own time, never an author's. During admission's validation replay the
	// candidate bundle's time is synthetic, so only prior bundles' times are
	// observations.
	RecordedAt time.Time
}

// Referrer is one reverse edge: an admitted event that named a referent. U06
// expands correction and supersession through these, and U13 traverses them.
type Referrer struct {
	Origin Origin
	Type   model.EventType
	Path   string
}

// Deferred preserves the diagnostic vocabulary for callers of the partial fold.
// Every event in the closed model vocabulary now has a semantic route.
type Deferred struct {
	Origin Origin
	Type   model.EventType
}

// Watermark is the selected ledger position, kept out of the keyed maps because
// it describes the read, not a record.
type Watermark struct {
	Sequence   uint64
	CommandID  model.ID
	RecordedAt time.Time
	Bundles    int
	Events     int
}
