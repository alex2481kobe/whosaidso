package model

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// TypedEvent is the closed semantic boundary after U01's raw Event. U05/U06
// consume these payloads; only admission can decide whether they fit current state.
type TypedEvent interface {
	eventPayload()
	EventType() EventType
}

// EventRef addresses an immutable intake event without minting a per-event ID.
// CommandID is the original packet ID, EventIndex its zero-based authored index.
// U08 retains that provenance when flattening a packet set into a bundle.
type EventRef struct {
	Project    ProjectID `json:"project"`
	CommandID  ID        `json:"command_id"`
	EventIndex uint32    `json:"event_index"`
}

// Creation/replacement payloads feed revision history in U05/U06 and authored
// context in U13. The enclosing packet supplies each revision's author/provenance.
type TaskCreate struct {
	ID   ID       `json:"id"`
	Spec TaskSpec `json:"spec"`
}
type TaskAmend struct {
	Target           RecordRef `json:"target"`
	ExpectedRevision Revision  `json:"expected_revision"`
	Replacement      TaskSpec  `json:"replacement"`
}
type ClaimAssert struct {
	ID   ID        `json:"id"`
	Spec ClaimSpec `json:"spec"`
}
type ClaimRevise struct {
	Target           RecordRef `json:"target"`
	ExpectedRevision Revision  `json:"expected_revision"`
	Replacement      ClaimSpec `json:"replacement"`
}
type DecisionOpen struct {
	ID   ID           `json:"id"`
	Spec DecisionSpec `json:"spec"`
}
type DecisionRevise struct {
	Target           RecordRef    `json:"target"`
	ExpectedRevision Revision     `json:"expected_revision"`
	Replacement      DecisionSpec `json:"replacement"`
}
type InstrumentDeclare struct {
	ID   ID             `json:"id"`
	Spec InstrumentSpec `json:"spec"`
}
type InstrumentRevise struct {
	Target           RecordRef      `json:"target"`
	ExpectedRevision Revision       `json:"expected_revision"`
	Replacement      InstrumentSpec `json:"replacement"`
}

func replacementRevision(target RecordRef, expected Revision, p string) error {
	if expected == 0 || expected == ^Revision(0) || target.Revision != expected {
		return invalid(p+".expected_revision", "expected revision must match target and permit a successor")
	}
	return nil
}
func (e TaskAmend) validate(p string) error {
	return replacementRevision(e.Target, e.ExpectedRevision, p)
}
func (e ClaimRevise) validate(p string) error {
	return replacementRevision(e.Target, e.ExpectedRevision, p)
}
func (e DecisionRevise) validate(p string) error {
	return replacementRevision(e.Target, e.ExpectedRevision, p)
}
func (e InstrumentRevise) validate(p string) error {
	return replacementRevision(e.Target, e.ExpectedRevision, p)
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
	HoldRef          EventRef    `json:"hold_ref"`
	ResolvingWitness ArtifactRef `json:"resolving_witness"`
}

// InvocationStart/Seal feed U06 observation history and U10 capture. Seal carries
// the complete envelope and a start link; U06/U12 enforce immutable intent equality.
type InvocationStart struct {
	Envelope InvocationEnvelope `json:"envelope"`
}
type InvocationSeal struct {
	StartRef EventRef           `json:"start_ref"`
	Envelope InvocationEnvelope `json:"envelope"`
}

func (e InvocationStart) validate(p string) error {
	if e.Envelope.Outcome.State != Unknown || e.Envelope.ObservedAt.State != Unknown || e.Envelope.OutputRefs.State != Unknown {
		return invalid(p+".envelope", "pre-launch intent cannot claim a terminal observation or outputs")
	}
	return nil
}

// SourceIntake feeds U03/U08 exact-byte capture and U13 attribution/ordering.
// Order is zero-based within the producer's source sequence, not ledger order.
type SourceIntake struct {
	SourceID       ID          `json:"source_id"`
	OriginalDigest Digest      `json:"original_digest"`
	Length         uint64      `json:"length"`
	SourceRef      ArtifactRef `json:"source_ref"`
	Speaker        Actor       `json:"speaker"`
	Order          uint64      `json:"order"`
	Referents      []RecordRef `json:"referents"`
}

func (e SourceIntake) validate(p string) error {
	if e.SourceRef.Selector.Kind != "whole" {
		return invalid(p+".source_ref", "intake identifies whole original bytes")
	}
	if c := e.SourceRef.Content; c != nil && (c.SHA256 != e.OriginalDigest || c.Length != e.Length) {
		return invalid(p+".source_ref", "source digest/length disagree with the content pin")
	}
	return nil
}

// CriterionFix feeds U06 independent criterion revisions and U07/U12 execution.
// Author is the actual predicate author, not inferred from the eventual admitter.
type CriterionFix struct {
	Claim       RecordRef           `json:"claim"`
	CriterionID ID                  `json:"criterion_id"`
	Revision    Revision            `json:"revision"`
	Expression  CriterionExpression `json:"expression"`
	Policy      EvaluationPolicy    `json:"policy"`
	Author      Actor               `json:"author"`
}
type ObservationDisposition struct {
	InvocationID ID       `json:"invocation_id"`
	SealRef      EventRef `json:"seal_ref"`
	Disposition  string   `json:"disposition"`
	Reason       string   `json:"reason" semantic:"text"`
}

func (d ObservationDisposition) validate(p string) error {
	return oneOf(d.Disposition, p+".disposition", "supports", "contradicts", "inapplicable", "inconclusive")
}

type ResponsibleJudgment struct {
	Actor  Actor  `json:"actor"`
	Reason string `json:"reason" semantic:"text"`
}

func (j ResponsibleJudgment) validate(p string) error {
	if strings.TrimSpace(j.Actor.ID) == "" {
		return invalid(p+".actor", "proof judgment requires a named responsible actor")
	}
	return nil
}

// ProofAdmit supplies U06/U12 the family and responsible judgment, not a writable
// PROVEN. Family completeness, timing and unresolved contradiction are gate checks.
type ProofAdmit struct {
	Claim        RecordRef                `json:"claim"`
	CriterionRef CriterionRef             `json:"criterion_ref"`
	Evidence     []ObservationDisposition `json:"evidence"`
	Judgment     ResponsibleJudgment      `json:"judgment"`
}

func (e ProofAdmit) validate(p string) error {
	if e.Claim != e.CriterionRef.Claim {
		return invalid(p+".criterion_ref", "criterion must name the same exact assertion")
	}
	if len(e.Evidence) == 0 {
		return invalid(p+".evidence", "proof needs an evidence family")
	}
	seen := map[ID]bool{}
	seals := map[EventRef]bool{}
	for i, o := range e.Evidence {
		if seen[o.InvocationID] || seals[o.SealRef] {
			return invalid(fmt.Sprintf("%s.evidence[%d]", p, i), "duplicate observation")
		}
		seen[o.InvocationID] = true
		seals[o.SealRef] = true
	}
	return nil
}

// DecisionDispose supplies U06/U12 a scoped attributable ruling; U13 preserves
// the exact quote, including its original whitespace around nonblank words.
type DecisionDispose struct {
	Decision    RecordRef `json:"decision"`
	Disposition string    `json:"disposition"`
	Quote       string    `json:"quote" semantic:"text"`
	Scope       Scope     `json:"scope"`
	Authority   Authority `json:"authority"`
}

func (e DecisionDispose) validate(p string) error {
	return oneOf(e.Disposition, p+".disposition", "approved", "rejected", "withdrawn")
}

// Supersede preserves U06/U13 canonical history. U12 requires Authority when an
// owner ruling is affected; only stateful admission can identify that circumstance.
type Supersede struct {
	Prior       RecordRef  `json:"prior"`
	Replacement RecordRef  `json:"replacement"`
	Reason      string     `json:"reason" semantic:"text"`
	Authority   *Authority `json:"authority,omitempty"`
}

// SupportLink names the exact dependent revision and the evidence whose support
// is corrected. U06/U12 expand this through the reverse graph, with cycle detection.
type SupportLink struct {
	Dependent RecordRef   `json:"dependent"`
	Evidence  ArtifactRef `json:"evidence"`
}
type CorrectionTarget struct {
	Kind      string        `json:"kind"`
	Record    *RecordRef    `json:"record,omitempty"`
	Criterion *CriterionRef `json:"criterion,omitempty"`
	Support   *SupportLink  `json:"support,omitempty"`
}

func (r CorrectionTarget) validate(p string) error {
	n := 0
	if r.Record != nil {
		n++
	}
	if r.Criterion != nil {
		n++
	}
	if r.Support != nil {
		n++
	}
	if n != 1 {
		return invalid(p, "correction needs exactly one typed target")
	}
	switch r.Kind {
	case "record":
		if r.Record != nil {
			return nil
		}
	case "criterion":
		if r.Criterion != nil {
			return nil
		}
	case "support":
		if r.Support != nil {
			return nil
		}
	}
	return invalid(p, "target tag does not select its branch")
}

type Correction struct {
	Target            CorrectionTarget `json:"target"`
	AffectedRevisions []RecordRef      `json:"affected_revisions"`
	Reason            string           `json:"reason" semantic:"text"`
	CorrectiveRef     ArtifactRef      `json:"corrective_ref"`
}

func (e Correction) validate(p string) error {
	if len(e.AffectedRevisions) == 0 {
		return invalid(p+".affected_revisions", "exact affected revisions are required")
	}
	return nil
}

// TrustWithdraw supplies U06/U12 scope and the condition for restoring support.
type TrustWithdraw struct {
	Instrument            RecordRef `json:"instrument"`
	Scope                 Scope     `json:"scope"`
	RevalidationCondition string    `json:"revalidation_condition" semantic:"text"`
}

// ReviewAdmit feeds U08 admission and U13 visible pending/rejected dispositions.
// Packet refs identify exact intake bytes; they are not record references.
type ReviewAdmit struct {
	Packets []PacketRef `json:"packets"`
	Outcome string      `json:"outcome"`
	Actor   Actor       `json:"actor"`
	Reason  string      `json:"reason" semantic:"text"`
}

func (e ReviewAdmit) validate(p string) error {
	if len(e.Packets) == 0 {
		return invalid(p+".packets", "review must name its packets")
	}
	seen := map[ID]bool{}
	for i, r := range e.Packets {
		if seen[r.CommandID] {
			return invalid(fmt.Sprintf("%s.packets[%d]", p, i), "duplicate packet")
		}
		seen[r.CommandID] = true
	}
	return oneOf(e.Outcome, p+".outcome", "accepted", "correction-requested", "rejected")
}

// ArtifactDispose feeds U12's manual loss accounting and U06/U13 current support.
// An empty support-loss list explicitly states no known dependent revision.
type ArtifactDispose struct {
	Artifact         ArtifactRef   `json:"artifact"`
	Digest           Digest        `json:"digest"`
	PreviousLocation string        `json:"previous_location"`
	SupportLoss      []SupportLoss `json:"support_loss"`
	Authority        Authority     `json:"authority"`
}
type SupportLoss struct {
	Target RecordRef `json:"target"`
	Reason string    `json:"reason" semantic:"text"`
}

func (e ArtifactDispose) validate(p string) error {
	if e.Artifact.Selector.Kind != "whole" {
		return invalid(p+".artifact", "disposal identifies whole bytes")
	}
	if e.Artifact.Content != nil && e.Artifact.Content.SHA256 != e.Digest {
		return invalid(p+".digest", "digest disagrees with artifact identity")
	}
	return relativePath(e.PreviousLocation, p+".previous_location")
}

func (*TaskCreate) eventPayload()               {}
func (*TaskCreate) EventType() EventType        { return "task.create" }
func (*TaskAmend) eventPayload()                {}
func (*TaskAmend) EventType() EventType         { return "task.amend" }
func (*TaskStart) eventPayload()                {}
func (*TaskStart) EventType() EventType         { return "task.start" }
func (*TaskTakeover) eventPayload()             {}
func (*TaskTakeover) EventType() EventType      { return "task.takeover" }
func (*AttemptTerminal) eventPayload()          {}
func (*AttemptTerminal) EventType() EventType   { return "attempt.terminal" }
func (*TaskClose) eventPayload()                {}
func (*TaskClose) EventType() EventType         { return "task.close" }
func (*BlockerHold) eventPayload()              {}
func (*BlockerHold) EventType() EventType       { return "blocker.hold" }
func (*BlockerClear) eventPayload()             {}
func (*BlockerClear) EventType() EventType      { return "blocker.clear" }
func (*InvocationStart) eventPayload()          {}
func (*InvocationStart) EventType() EventType   { return "invocation.start" }
func (*InvocationSeal) eventPayload()           {}
func (*InvocationSeal) EventType() EventType    { return "invocation.seal" }
func (*SourceIntake) eventPayload()             {}
func (*SourceIntake) EventType() EventType      { return "source.intake" }
func (*ClaimAssert) eventPayload()              {}
func (*ClaimAssert) EventType() EventType       { return "claim.assert" }
func (*ClaimRevise) eventPayload()              {}
func (*ClaimRevise) EventType() EventType       { return "claim.revise" }
func (*CriterionFix) eventPayload()             {}
func (*CriterionFix) EventType() EventType      { return "criterion.fix" }
func (*ProofAdmit) eventPayload()               {}
func (*ProofAdmit) EventType() EventType        { return "proof.admit" }
func (*DecisionOpen) eventPayload()             {}
func (*DecisionOpen) EventType() EventType      { return "decision.open" }
func (*DecisionRevise) eventPayload()           {}
func (*DecisionRevise) EventType() EventType    { return "decision.revise" }
func (*DecisionDispose) eventPayload()          {}
func (*DecisionDispose) EventType() EventType   { return "decision.dispose" }
func (*Supersede) eventPayload()                {}
func (*Supersede) EventType() EventType         { return "supersede" }
func (*Correction) eventPayload()               {}
func (*Correction) EventType() EventType        { return "correction" }
func (*InstrumentDeclare) eventPayload()        {}
func (*InstrumentDeclare) EventType() EventType { return "instrument.declare" }
func (*InstrumentRevise) eventPayload()         {}
func (*InstrumentRevise) EventType() EventType  { return "instrument.revise" }
func (*TrustWithdraw) eventPayload()            {}
func (*TrustWithdraw) EventType() EventType     { return "trust.withdraw" }
func (*ReviewAdmit) eventPayload()              {}
func (*ReviewAdmit) EventType() EventType       { return "review.admit" }
func (*ArtifactDispose) eventPayload()          {}
func (*ArtifactDispose) EventType() EventType   { return "artifact.dispose" }

// DecodeEvent decodes one raw payload once, into the closed event set. Required
// fields, exact JSON key spelling and tagged unions are checked before reduction.
func DecodeEvent(raw Event) (TypedEvent, error) {
	var event TypedEvent
	switch raw.Type {
	case "task.create":
		event = &TaskCreate{}
	case "task.amend":
		event = &TaskAmend{}
	case "task.start":
		event = &TaskStart{}
	case "task.takeover":
		event = &TaskTakeover{}
	case "attempt.terminal":
		event = &AttemptTerminal{}
	case "task.close":
		event = &TaskClose{}
	case "blocker.hold":
		event = &BlockerHold{}
	case "blocker.clear":
		event = &BlockerClear{}
	case "invocation.start":
		event = &InvocationStart{}
	case "invocation.seal":
		event = &InvocationSeal{}
	case "source.intake":
		event = &SourceIntake{}
	case "claim.assert":
		event = &ClaimAssert{}
	case "claim.revise":
		event = &ClaimRevise{}
	case "criterion.fix":
		event = &CriterionFix{}
	case "proof.admit":
		event = &ProofAdmit{}
	case "decision.open":
		event = &DecisionOpen{}
	case "decision.revise":
		event = &DecisionRevise{}
	case "decision.dispose":
		event = &DecisionDispose{}
	case "supersede":
		event = &Supersede{}
	case "correction":
		event = &Correction{}
	case "instrument.declare":
		event = &InstrumentDeclare{}
	case "instrument.revise":
		event = &InstrumentRevise{}
	case "trust.withdraw":
		event = &TrustWithdraw{}
	case "review.admit":
		event = &ReviewAdmit{}
	case "artifact.dispose":
		event = &ArtifactDispose{}
	default:
		return nil, fault("unknown-event", "event.type", "event is outside the closed set: "+string(raw.Type))
	}
	tree, err := parseOrdered(raw.Data)
	if err != nil {
		return nil, err
	}
	if err = checkJSONShape(tree, reflect.TypeOf(event), "event.data"); err != nil {
		return nil, err
	}
	if err = strictUnmarshal(raw.Data, event, "event.data"); err != nil {
		return nil, err
	}
	if err = validateValue(reflect.ValueOf(event), "event.data"); err != nil {
		return nil, err
	}
	return event, nil
}

// EncodeEvent validates through the same boundary used for untrusted bytes, so
// an in-process producer cannot bypass a refusal by constructing a Go value.
func EncodeEvent(event TypedEvent) (Event, error) {
	if event == nil || (reflect.ValueOf(event).Kind() == reflect.Pointer && reflect.ValueOf(event).IsNil()) {
		return Event{}, invalid("event", "nil typed event")
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return Event{}, invalid("event.data", err.Error())
	}
	e := Event{Type: event.EventType(), Data: raw}
	decoded, err := DecodeEvent(e)
	if err != nil {
		return Event{}, err
	}
	if reflect.TypeOf(decoded) != reflect.TypeOf(event) {
		return Event{}, fault("unknown-event", "event.type", "payload is not a member of the closed set")
	}
	return e, nil
}

// Reference is one exact dependency discovered by the model, with its field path.
// Exactly one branch is set. Artifact pins are deliberately not record referents;
// U07 resolves bytes, while U08/U13 use this walker for project ledger references.
type Reference struct {
	Path      string
	Record    *RecordRef
	Criterion *CriterionRef
	Event     *EventRef
}

func (r Reference) Project() ProjectID {
	if r.Record != nil {
		return r.Record.Project
	}
	if r.Criterion != nil {
		return r.Criterion.Claim.Project
	}
	if r.Event != nil {
		return r.Event.Project
	}
	return ""
}

// EventReferences is the sole typed reference walker for admission and queries.
// A new event must acquire an explicit branch here; JSON field-name heuristics
// cannot distinguish an authored target from a coincidentally named config knob.
func EventReferences(event TypedEvent) ([]Reference, error) {
	if _, err := EncodeEvent(event); err != nil {
		return nil, err
	}
	w := referenceWalker{refs: []Reference{}}
	switch e := event.(type) {
	case *TaskCreate:
		w.task(e.Spec, "spec")
	case *TaskAmend:
		w.record(e.Target, "target")
		w.task(e.Replacement, "replacement")
	case *TaskStart:
		w.record(e.Task, "task")
	case *TaskTakeover:
		w.record(e.Task, "task")
	case *AttemptTerminal:
		w.record(e.Task, "task")
	case *TaskClose:
		w.record(e.Task, "task")
		w.authority(e.Authority, "authority")
	case *BlockerHold:
		w.record(e.Task, "task")
	case *BlockerClear:
		w.record(e.Task, "task")
		w.event(e.HoldRef, "hold_ref")
	case *InvocationStart:
		w.envelope(e.Envelope, "envelope")
	case *InvocationSeal:
		w.event(e.StartRef, "start_ref")
		w.envelope(e.Envelope, "envelope")
	case *SourceIntake:
		w.records(e.Referents, "referents")
	case *ClaimAssert:
		w.claim(e.Spec, "spec")
	case *ClaimRevise:
		w.record(e.Target, "target")
		w.claim(e.Replacement, "replacement")
	case *CriterionFix:
		w.record(e.Claim, "claim")
	case *ProofAdmit:
		w.record(e.Claim, "claim")
		w.criterion(e.CriterionRef, "criterion_ref")
		for i, o := range e.Evidence {
			w.event(o.SealRef, fmt.Sprintf("evidence[%d].seal_ref", i))
		}
	case *DecisionOpen:
		w.scope(e.Spec.Scope, "spec.scope")
	case *DecisionRevise:
		w.record(e.Target, "target")
		w.scope(e.Replacement.Scope, "replacement.scope")
	case *DecisionDispose:
		w.record(e.Decision, "decision")
		w.scope(e.Scope, "scope")
		w.authority(e.Authority, "authority")
	case *Supersede:
		w.record(e.Prior, "prior")
		w.record(e.Replacement, "replacement")
		if e.Authority != nil {
			w.authority(*e.Authority, "authority")
		}
	case *Correction:
		switch e.Target.Kind {
		case "record":
			w.record(*e.Target.Record, "target.record")
		case "criterion":
			w.criterion(*e.Target.Criterion, "target.criterion")
		case "support":
			w.record(e.Target.Support.Dependent, "target.support.dependent")
		}
		w.records(e.AffectedRevisions, "affected_revisions")
	case *InstrumentDeclare: // only pinned artifact links
	case *InstrumentRevise:
		w.record(e.Target, "target")
	case *TrustWithdraw:
		w.record(e.Instrument, "instrument")
		w.scope(e.Scope, "scope")
	case *ReviewAdmit: // packet existence is checked against intake, not record state
	case *ArtifactDispose:
		for i, s := range e.SupportLoss {
			w.record(s.Target, fmt.Sprintf("support_loss[%d].target", i))
		}
		w.authority(e.Authority, "authority")
	default:
		return nil, fault("unknown-event", "event.type", "missing typed reference walker")
	}
	return w.refs, nil
}

// SameProjectReferences leaves cross-project links for read-time resolution.
// Empty output means no local dependencies, never that external links vanished.
func SameProjectReferences(event TypedEvent, project ProjectID) ([]Reference, error) {
	if strings.TrimSpace(string(project)) == "" {
		return nil, invalid("project", "empty project")
	}
	refs, err := EventReferences(event)
	if err != nil {
		return nil, err
	}
	local := []Reference{}
	for _, r := range refs {
		if r.Project() == project {
			local = append(local, r)
		}
	}
	return local, nil
}

type referenceWalker struct{ refs []Reference }

func (w *referenceWalker) record(r RecordRef, p string) {
	w.refs = append(w.refs, Reference{Path: p, Record: &r})
}
func (w *referenceWalker) criterion(r CriterionRef, p string) {
	w.refs = append(w.refs, Reference{Path: p, Criterion: &r})
}
func (w *referenceWalker) event(r EventRef, p string) {
	w.refs = append(w.refs, Reference{Path: p, Event: &r})
}
func (w *referenceWalker) records(rs []RecordRef, p string) {
	for i, r := range rs {
		w.record(r, fmt.Sprintf("%s[%d]", p, i))
	}
}
func (w *referenceWalker) scope(s Scope, p string)         { w.records(s.ContextRefs, p+".context_refs") }
func (w *referenceWalker) authority(a Authority, p string) { w.scope(a.Scope, p+".scope") }
func (w *referenceWalker) task(s TaskSpec, p string) {
	w.scope(s.Scope, p+".scope")
	w.records(s.ContextRefs, p+".context_refs")
	w.records(s.ConstraintRefs, p+".constraint_refs")
	for i, r := range s.Prerequisites {
		at := fmt.Sprintf("%s.prerequisites[%d]", p, i)
		w.record(r.Target, at+".target")
		if r.Authority != nil {
			w.authority(*r.Authority, at+".authority")
		}
	}
}
func (w *referenceWalker) claim(s ClaimSpec, p string) {
	w.scope(s.Scope, p+".scope")
	for i, r := range s.ExternalRefs {
		if r.RecordRef != nil {
			w.record(*r.RecordRef, fmt.Sprintf("%s.external_refs[%d].record_ref", p, i))
		}
	}
}
func (w *referenceWalker) envelope(e InvocationEnvelope, p string) {
	w.record(e.InstrumentRef, p+".instrument_ref")
	if e.CriterionRef.State == Known && e.CriterionRef.Value != nil {
		w.criterion(*e.CriterionRef.Value, p+".criterion_ref.value")
	}
}
