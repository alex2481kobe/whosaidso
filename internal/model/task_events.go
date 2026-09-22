package model

// Task attempt ownership, terminal receipts, closure, and blocker payloads live here.
// Authored task revisions, invocation evidence, and state transitions do not.
// This file stays below 200 lines to keep task execution payloads together.

// BlockerRef identifies the hold under its exact task revision. U05 refuses
// reusing that blocker ID to replace the hold's authored meaning.
type BlockerRef struct {
	Task      RecordRef `json:"task"`
	BlockerID ID        `json:"blocker_id"`
}

// Attempt ownership and receipts feed U05/U11; U13 renders the same facts.
type TaskStart struct {
	Task      RecordRef `json:"task"`
	Actor     Actor     `json:"actor"`
	AttemptID ID        `json:"attempt_id"`
}
type TaskTakeover struct {
	Task                   RecordRef   `json:"task"`
	Actor                  Actor       `json:"actor"`
	AttemptID              ID          `json:"attempt_id"`
	PriorAttemptID         ID          `json:"prior_attempt_id"`
	StoppedConfirmationRef ArtifactRef `json:"stopped_confirmation_ref"`
}

func (e TaskTakeover) validate(p string) error {
	if e.AttemptID == e.PriorAttemptID {
		return invalid(p+".attempt_id", "takeover creates a distinct attempt")
	}
	return nil
}

type AttemptOutcome string

const (
	AttemptSuccess               AttemptOutcome = "success"
	AttemptStopped               AttemptOutcome = "stopped"
	AttemptRefused               AttemptOutcome = "refused"
	AttemptNoReading             AttemptOutcome = "no-reading"
	AttemptMeasurementImpossible AttemptOutcome = "measurement-impossible"
	AttemptRunnerDied            AttemptOutcome = "runner-died"
	AttemptHarnessBroken         AttemptOutcome = "harness-broken"
	AttemptOutOfScope            AttemptOutcome = "out-of-scope"
	AttemptBlockedMidTask        AttemptOutcome = "blocked-mid-task"
)

func (o AttemptOutcome) validate(p string) error {
	return oneOf(string(o), p, "success", "stopped", "refused", "no-reading", "measurement-impossible", "runner-died", "harness-broken", "out-of-scope", "blocked-mid-task")
}

type AttemptTerminal struct {
	Task               RecordRef      `json:"task"`
	AttemptID          ID             `json:"attempt_id"`
	Outcome            AttemptOutcome `json:"outcome"`
	Reason             string         `json:"reason" semantic:"text"`
	NextAction         string         `json:"next_action" semantic:"text"`
	DeliveryRefs       []ArtifactRef  `json:"delivery_refs"`
	CommitsDenied      bool           `json:"commits_denied"`
	ReconciliationOwed bool           `json:"reconciliation_owed"`
}

// TaskClose supplies U05/U12 authority and revision-applicable witnesses. Whether
// all attempts are terminal and witnesses resolve is a gate/reducer question.
type TaskClose struct {
	Task                  RecordRef           `json:"task"`
	Outcome               ClosureOutcome      `json:"outcome"`
	Authority             Authority           `json:"authority"`
	AcceptanceWitnessRefs []AcceptanceWitness `json:"acceptance_witness_refs"`
	DeliveryWitnessRefs   []ArtifactRef       `json:"delivery_witness_refs"`
}
type AcceptanceWitness struct {
	CriterionID       ID          `json:"criterion_id"`
	CriterionRevision Revision    `json:"criterion_revision"`
	WitnessRef        ArtifactRef `json:"witness_ref"`
}
type ClosureOutcome string

const (
	ClosureSuccess   ClosureOutcome = "success"
	ClosureCancelled ClosureOutcome = "cancelled"
	ClosureWithdrawn ClosureOutcome = "withdrawn"
	ClosureWaived    ClosureOutcome = "waived"
)

func (o ClosureOutcome) validate(p string) error {
	return oneOf(string(o), p, "success", "cancelled", "withdrawn", "waived")
}

// BlockerReason gives U05/U11/U13 separate acceptance, resume and reconciliation
// queues. Prerequisite covers other unmet prerequisites without inventing a status.
type BlockerReason string

const (
	BlockerPrerequisite       BlockerReason = "prerequisite"
	BlockerAwaitingAcceptance BlockerReason = "awaiting-acceptance"
	BlockerResume             BlockerReason = "resume"
	BlockerReconciliation     BlockerReason = "reconciliation"
)

func (r BlockerReason) validate(p string) error {
	return oneOf(string(r), p, "prerequisite", "awaiting-acceptance", "resume", "reconciliation")
}

type BlockerHold struct {
	Task      RecordRef     `json:"task"`
	BlockerID ID            `json:"blocker_id"`
	Reason    BlockerReason `json:"reason"`
	Actor     Actor         `json:"actor"`
	Criterion string        `json:"criterion" semantic:"text"`
}
type BlockerClear struct {
	Task             RecordRef   `json:"task"`
	BlockerID        ID          `json:"blocker_id"`
	HoldRef          BlockerRef  `json:"hold_ref"`
	ResolvingWitness ArtifactRef `json:"resolving_witness"`
}

func (e BlockerClear) validate(p string) error {
	if e.HoldRef.Task != e.Task || e.HoldRef.BlockerID != e.BlockerID {
		return invalid(p+".hold_ref", "clear must link this blocker at the same task revision")
	}
	return nil
}
