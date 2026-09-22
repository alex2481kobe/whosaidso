// Package reduce folds admitted bundles into the state every Datum answer is
// read from.
//
// This is the single point of failure the contract names. The bundles can be
// intact, every record valid and every digest correct while the fold is wrong,
// and nothing catches it: there is no proposer/accepter split here, because the
// fold IS the answer. So the rules below refuse rather than cope, and the
// package ships with its own must-fail fixtures, determinism check and golden
// log rather than acquiring them later.
//
// Replay and Apply are pure. No filesystem, no clock, no subprocess, no
// network, and no randomness. The trusted surface stays small on purpose:
// current artifact availability is a read-time observation supplied by the
// evidence resolver, never filesystem work inside the fold.
package reduce

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"datum/internal/model"
)

// Fault codes. The code and the location are the invariant. The diagnostic
// text is not, so tests assert on codes.
const (
	// CodeLedgerDiscontinuity is a broken sequence or predecessor chain.
	CodeLedgerDiscontinuity = "ledger-discontinuity"
	// CodeProjectMismatch is a bundle from another project in this ledger.
	CodeProjectMismatch = "project-mismatch"
	// CodeDuplicateCommand is a second bundle under an admitted transaction id.
	CodeDuplicateCommand = "duplicate-command"
	// CodeUnknownVersion is an envelope this build cannot account for.
	CodeUnknownVersion = "unknown-version"
	// CodeInvalidField is a structurally impossible event in context.
	CodeInvalidField = "invalid-field"
	// CodeUnknownReference names something no admitted bundle established.
	CodeUnknownReference = "unknown-reference"
	// CodeDuplicateRecord mints an identity that already exists.
	CodeDuplicateRecord = "duplicate-record"
	// CodeInvalidTransition is a legal event applied to a state that forbids it.
	CodeInvalidTransition = "invalid-transition"
	// CodeRevisionConflict is a write built on a stale view. See Conflict.
	CodeRevisionConflict = "revision-conflict"
)

// Conflict is the typed refusal for a write whose expected revision no longer
// matches. It carries the bundle sequence and the event index because "someone
// amended this first" is only actionable if you can say which write lost and
// exactly where. Which one loses is decided by ledger sequence, never by the
// order a directory happened to enumerate.
type Conflict struct {
	Target     model.RecordRef
	Expected   model.Revision
	Actual     model.Revision
	Sequence   uint64
	EventIndex int
	Path       string
}

// Code lets a caller branch on the same vocabulary model.Fault uses.
func (c *Conflict) Code() string { return CodeRevisionConflict }

func (c *Conflict) Error() string {
	return fmt.Sprintf("%s at %s in event %d (sequence %d): %s expected revision %d, admitted revision is %d",
		CodeRevisionConflict, c.Path, c.EventIndex, c.Sequence, c.Target.RecordID, c.Expected, c.Actual)
}

func faultAt(code string, seq uint64, idx int, path, detail string) *model.Fault {
	return &model.Fault{Code: code, Sequence: seq, EventIndex: idx, Path: path, Detail: detail}
}

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

// ---- state ---------------------------------------------------------------

type state struct {
	project   model.ProjectID
	watermark Watermark

	records map[RecordKey]Record
	current map[Ident]model.Revision
	closed  map[Ident]Closure

	attempts     map[AttemptKey]Attempt
	attemptOwner map[Ident]AttemptKey // attempt id -> its task, for envelope links
	blockers     map[BlockerKey]Blocker
	invocations  map[InvocationKey]Invocation
	criteria     map[CriterionKey]Criterion
	reviews      map[ReviewKey]Review
	sources      map[SourceKey]Source
	commands     map[model.ID]uint64
	events       map[Origin]model.TypedEvent

	reverseRecord     map[RecordKey][]Referrer
	reverseCriterion  map[CriterionKey][]Referrer
	reverseInvocation map[InvocationKey][]Referrer
	reverseBlocker    map[BlockerKey][]Referrer
}

func newState() *state {
	return &state{
		records:           map[RecordKey]Record{},
		current:           map[Ident]model.Revision{},
		closed:            map[Ident]Closure{},
		attempts:          map[AttemptKey]Attempt{},
		attemptOwner:      map[Ident]AttemptKey{},
		blockers:          map[BlockerKey]Blocker{},
		invocations:       map[InvocationKey]Invocation{},
		criteria:          map[CriterionKey]Criterion{},
		reviews:           map[ReviewKey]Review{},
		sources:           map[SourceKey]Source{},
		commands:          map[model.ID]uint64{},
		events:            map[Origin]model.TypedEvent{},
		reverseRecord:     map[RecordKey][]Referrer{},
		reverseCriterion:  map[CriterionKey][]Referrer{},
		reverseInvocation: map[InvocationKey][]Referrer{},
		reverseBlocker:    map[BlockerKey][]Referrer{},
	}
}

func copyMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// clone is what keeps Apply pure. Values in these maps are replaced, never
// mutated in place, so a shallow copy of each map is a real fork of the state.
func (s *state) clone() *state {
	return &state{
		project:           s.project,
		watermark:         s.watermark,
		records:           copyMap(s.records),
		current:           copyMap(s.current),
		closed:            copyMap(s.closed),
		attempts:          copyMap(s.attempts),
		attemptOwner:      copyMap(s.attemptOwner),
		blockers:          copyMap(s.blockers),
		invocations:       copyMap(s.invocations),
		criteria:          copyMap(s.criteria),
		reviews:           copyMap(s.reviews),
		sources:           copyMap(s.sources),
		commands:          copyMap(s.commands),
		events:            copyMap(s.events),
		reverseRecord:     copyMap(s.reverseRecord),
		reverseCriterion:  copyMap(s.reverseCriterion),
		reverseInvocation: copyMap(s.reverseInvocation),
		reverseBlocker:    copyMap(s.reverseBlocker),
	}
}

// addReferrer copies before extending. Appending in place would write into a
// backing array a cloned snapshot still points at, so one fork would silently
// alter another - the exact class of bug this package exists to prevent.
func addReferrer[K comparable](m map[K][]Referrer, k K, r Referrer) {
	cur := m[k]
	next := make([]Referrer, len(cur)+1)
	copy(next, cur)
	next[len(cur)] = r
	m[k] = next
}

// ---- Snapshot ------------------------------------------------------------

// Snapshot is an immutable projection of a complete ledger prefix. The zero
// value is the empty snapshot, which is a legitimate answer for a new project.
type Snapshot struct{ st *state }

func (s Snapshot) inner() *state {
	if s.st == nil {
		return newState()
	}
	return s.st
}

// Replay folds a complete ledger prefix from empty. A corrupt or conflicting
// ledger fails here explicitly: there is no partial snapshot and no skipped
// bundle, because a fold that steps over what it cannot explain answers every
// later question with a number that looks fine.
func Replay(bundles []model.Bundle) (Snapshot, error) {
	st := newState()
	for i := range bundles {
		if err := st.apply(bundles[i]); err != nil {
			return Snapshot{}, err
		}
	}
	return Snapshot{st: st}, nil
}

// Apply folds one further bundle onto a snapshot. It is pure: the input
// snapshot is never modified, and on failure the returned snapshot is empty
// rather than partial. Empty is deliberate over returning the input unchanged -
// a caller that ignores the error then gets an obviously wrong answer instead
// of a quietly stale one.
func Apply(s Snapshot, b model.Bundle) (Snapshot, error) {
	st := s.inner().clone()
	if err := st.apply(b); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{st: st}, nil
}

// ---- envelope ------------------------------------------------------------

func blank(s string) bool { return strings.TrimSpace(s) == "" }

func (s *state) checkEnvelope(b model.Bundle) error {
	if b.Version != model.WireVersion {
		return faultAt(CodeUnknownVersion, b.Sequence, -1, "bundle.version",
			fmt.Sprintf("wire version %d, expected %d", b.Version, model.WireVersion))
	}
	if blank(string(b.Project)) {
		return faultAt(CodeInvalidField, b.Sequence, -1, "bundle.project", "empty project id")
	}
	if s.project != "" && b.Project != s.project {
		return faultAt(CodeProjectMismatch, b.Sequence, -1, "bundle.project",
			fmt.Sprintf("ledger is %q, bundle is %q", s.project, b.Project))
	}
	if !model.ValidID(b.CommandID) {
		return faultAt(CodeInvalidField, b.Sequence, -1, "bundle.command_id", "not a ULID")
	}
	if seq, ok := s.commands[b.CommandID]; ok {
		return faultAt(CodeDuplicateCommand, b.Sequence, -1, "bundle.command_id",
			fmt.Sprintf("transaction id already admitted at sequence %d", seq))
	}
	if !model.ValidDigest(b.RequestDigest) {
		return faultAt(CodeInvalidField, b.Sequence, -1, "bundle.request_digest", "not lowercase sha-256 hex")
	}
	known, unknown := !blank(b.Admitter.ID), !blank(b.Admitter.UnknownReason)
	if known == unknown {
		return faultAt(CodeInvalidField, b.Sequence, -1, "bundle.admitter",
			"actor needs exactly one of an id or a stated reason it is unknown")
	}
	if len(b.Events) == 0 {
		return faultAt(CodeInvalidField, b.Sequence, -1, "bundle.events", "an empty write is not a fact")
	}

	// Contiguity, not merely monotonicity. A gap means a bundle we never read,
	// and a fold over an incomplete prefix is wrong in a way no later event
	// repairs.
	want := s.watermark.Sequence + 1
	if b.Sequence != want {
		return faultAt(CodeLedgerDiscontinuity, b.Sequence, -1, "bundle.sequence",
			fmt.Sprintf("expected sequence %d", want))
	}
	if want == 1 {
		if b.Predecessor != "" {
			return faultAt(CodeLedgerDiscontinuity, b.Sequence, -1, "bundle.predecessor",
				"the genesis bundle has no predecessor")
		}
	} else if b.Predecessor != s.watermark.CommandID {
		return faultAt(CodeLedgerDiscontinuity, b.Sequence, -1, "bundle.predecessor",
			fmt.Sprintf("expected predecessor %s", s.watermark.CommandID))
	}
	return nil
}

func (s *state) apply(b model.Bundle) error {
	if err := s.checkEnvelope(b); err != nil {
		return err
	}
	s.project = b.Project
	for i, raw := range b.Events {
		typed, err := model.DecodeEvent(raw)
		if err != nil {
			// Keep the model's own diagnostic and add where in the ledger it is.
			if f, ok := err.(*model.Fault); ok {
				return faultAt(f.Code, b.Sequence, i, f.Path, f.Detail)
			}
			return faultAt(CodeInvalidField, b.Sequence, i, "bundle.events", err.Error())
		}
		if err := s.checkSubject(b, i, typed); err != nil {
			return err
		}
		if err := s.checkExpectations(b, i, typed); err != nil {
			return err
		}
		if err := s.checkReferences(b, i, typed); err != nil {
			return err
		}
		if err := s.route(b, i, typed); err != nil {
			return err
		}
		s.events[Origin{Sequence: b.Sequence, EventIndex: i}] = typed
		s.recordReferrers(b, i, typed)
	}
	s.commands[b.CommandID] = b.Sequence
	s.watermark = Watermark{
		Sequence:   b.Sequence,
		CommandID:  b.CommandID,
		RecordedAt: b.RecordedAt.UTC(),
		Bundles:    s.watermark.Bundles + 1,
		Events:     s.watermark.Events + len(b.Events),
	}
	return nil
}

// checkSubject refuses a bundle that authors or mutates another project's
// record. Referring across projects is by design. Writing across them is not,
// and one ledger holding a foreign record makes both projects wrong.
func (s *state) checkSubject(b model.Bundle, idx int, e model.TypedEvent) error {
	var project model.ProjectID
	var path string
	switch t := e.(type) {
	case *model.TaskAmend:
		project, path = t.Target.Project, "target.project"
	case *model.ClaimRevise:
		project, path = t.Target.Project, "target.project"
	case *model.DecisionRevise:
		project, path = t.Target.Project, "target.project"
	case *model.InstrumentRevise:
		project, path = t.Target.Project, "target.project"
	case *model.TaskStart:
		project, path = t.Task.Project, "task.project"
	case *model.TaskTakeover:
		project, path = t.Task.Project, "task.project"
	case *model.AttemptTerminal:
		project, path = t.Task.Project, "task.project"
	case *model.TaskClose:
		project, path = t.Task.Project, "task.project"
	case *model.BlockerHold:
		project, path = t.Task.Project, "task.project"
	case *model.BlockerClear:
		project, path = t.Task.Project, "task.project"
	case *model.CriterionFix:
		project, path = t.Claim.Project, "claim.project"
	case *model.ProofAdmit:
		project, path = t.Claim.Project, "claim.project"
	case *model.DecisionDispose:
		project, path = t.Decision.Project, "decision.project"
	case *model.Supersede:
		project, path = t.Prior.Project, "prior.project"
	case *model.TrustWithdraw:
		project, path = t.Instrument.Project, "instrument.project"
	case *model.InvocationStart:
		project, path = t.Envelope.ExecutionSourceIdentity.Project, "envelope.execution_source_identity.project"
	case *model.InvocationSeal:
		project, path = t.Envelope.ExecutionSourceIdentity.Project, "envelope.execution_source_identity.project"
	default:
		return nil
	}
	if project != b.Project {
		return faultAt(CodeInvalidField, b.Sequence, idx, path,
			fmt.Sprintf("a bundle in %q cannot author into %q", b.Project, project))
	}
	return nil
}

// checkExpectations runs before reference checking so a stale write gets the
// conflict that names the winner, not a generic missing-reference complaint.
// An expected revision that does not exist at all falls through to the
// reference check, where "no such revision" is the honest answer.
func (s *state) checkExpectations(b model.Bundle, idx int, e model.TypedEvent) error {
	var target model.RecordRef
	var expected model.Revision
	var path string
	switch t := e.(type) {
	case *model.TaskAmend:
		target, expected, path = t.Target, t.ExpectedRevision, "target"
	case *model.ClaimRevise:
		target, expected, path = t.Target, t.ExpectedRevision, "target"
	case *model.DecisionRevise:
		target, expected, path = t.Target, t.ExpectedRevision, "target"
	case *model.InstrumentRevise:
		target, expected, path = t.Target, t.ExpectedRevision, "target"

	// These name the task revision the writer acted on. It is an expected
	// revision under another name: acting on a superseded contract is exactly
	// the stale write the conflict exists to stop.
	case *model.TaskStart:
		target, expected, path = t.Task, t.Task.Revision, "task"
	case *model.TaskTakeover:
		target, expected, path = t.Task, t.Task.Revision, "task"
	case *model.AttemptTerminal:
		target, expected, path = t.Task, t.Task.Revision, "task"
	case *model.TaskClose:
		target, expected, path = t.Task, t.Task.Revision, "task"
	case *model.BlockerHold:
		target, expected, path = t.Task, t.Task.Revision, "task"
	case *model.BlockerClear:
		target, expected, path = t.Task, t.Task.Revision, "task"
	default:
		return nil
	}
	actual, ok := s.current[ident(target)]
	if !ok {
		return nil
	}
	if actual != expected {
		return &Conflict{
			Target:     target,
			Expected:   expected,
			Actual:     actual,
			Sequence:   b.Sequence,
			EventIndex: idx,
			Path:       path,
		}
	}
	return nil
}

// checkReferences resolves every same-project reference the model walker finds.
// Cross-project links are exempt by design: they resolve on read and may dangle,
// and reporting an unresolved one is not the same as treating it as absent.
func (s *state) checkReferences(b model.Bundle, idx int, e model.TypedEvent) error {
	refs, err := model.SameProjectReferences(e, b.Project)
	if err != nil {
		if f, ok := err.(*model.Fault); ok {
			return faultAt(f.Code, b.Sequence, idx, f.Path, f.Detail)
		}
		return faultAt(CodeInvalidField, b.Sequence, idx, "event", err.Error())
	}
	for _, r := range refs {
		switch {
		case r.Record != nil:
			if _, ok := s.records[recordKey(*r.Record)]; !ok {
				return faultAt(CodeUnknownReference, b.Sequence, idx, r.Path,
					fmt.Sprintf("no admitted revision %d of %s", r.Record.Revision, r.Record.RecordID))
			}
		case r.Criterion != nil:
			if _, ok := s.criteria[criterionKey(*r.Criterion)]; !ok {
				return faultAt(CodeUnknownReference, b.Sequence, idx, r.Path,
					fmt.Sprintf("no admitted criterion %s revision %d", r.Criterion.CriterionID, r.Criterion.Revision))
			}
		case r.Invocation != nil:
			if _, ok := s.invocations[invocationKey(*r.Invocation)]; !ok {
				return faultAt(CodeUnknownReference, b.Sequence, idx, r.Path,
					fmt.Sprintf("no admitted invocation %s", r.Invocation.InvocationID))
			}
		case r.Blocker != nil:
			if _, ok := s.blockers[blockerKey(*r.Blocker)]; !ok {
				return faultAt(CodeUnknownReference, b.Sequence, idx, r.Path,
					fmt.Sprintf("no admitted blocker %s", r.Blocker.BlockerID))
			}
		}
	}
	return nil
}

func (s *state) recordReferrers(b model.Bundle, idx int, e model.TypedEvent) {
	refs, err := model.SameProjectReferences(e, b.Project)
	if err != nil {
		return // already refused upstream, unreachable in a valid apply
	}
	r := Referrer{Origin: Origin{Sequence: b.Sequence, EventIndex: idx}, Type: e.EventType()}
	for _, ref := range refs {
		r.Path = ref.Path
		switch {
		case ref.Record != nil:
			addReferrer(s.reverseRecord, recordKey(*ref.Record), r)
		case ref.Criterion != nil:
			addReferrer(s.reverseCriterion, criterionKey(*ref.Criterion), r)
		case ref.Invocation != nil:
			addReferrer(s.reverseInvocation, invocationKey(*ref.Invocation), r)
		case ref.Blocker != nil:
			addReferrer(s.reverseBlocker, blockerKey(*ref.Blocker), r)
		}
	}
}

// ---- routing -------------------------------------------------------------

func (s *state) route(b model.Bundle, idx int, e model.TypedEvent) error {
	o := Origin{Sequence: b.Sequence, EventIndex: idx}
	switch t := e.(type) {
	case *model.TaskCreate:
		return s.create(b, idx, o, model.Task, t.ID, t.Provenance, func(r *Record) { r.Task = &t.Spec })
	case *model.ClaimAssert:
		return s.create(b, idx, o, model.Claim, t.ID, t.Provenance, func(r *Record) { r.Claim = &t.Spec })
	case *model.DecisionOpen:
		return s.create(b, idx, o, model.Decision, t.ID, t.Provenance, func(r *Record) { r.Decision = &t.Spec })
	case *model.InstrumentDeclare:
		return s.create(b, idx, o, model.Instrument, t.ID, t.Provenance, func(r *Record) { r.Instrument = &t.Spec })

	case *model.TaskAmend:
		return s.amend(b, idx, o, model.Task, t.Target, t.Provenance, func(r *Record) { r.Task = &t.Replacement })
	case *model.ClaimRevise:
		return s.amend(b, idx, o, model.Claim, t.Target, t.Provenance, func(r *Record) { r.Claim = &t.Replacement })
	case *model.DecisionRevise:
		return s.amend(b, idx, o, model.Decision, t.Target, t.Provenance, func(r *Record) { r.Decision = &t.Replacement })
	case *model.InstrumentRevise:
		return s.amend(b, idx, o, model.Instrument, t.Target, t.Provenance, func(r *Record) { r.Instrument = &t.Replacement })

	case *model.TaskStart:
		return s.start(b, idx, o, t)
	case *model.TaskTakeover:
		return s.takeover(b, idx, o, t)
	case *model.AttemptTerminal:
		return s.terminal(b, idx, o, t)
	case *model.TaskClose:
		return s.close(b, idx, o, t)
	case *model.BlockerHold:
		return s.hold(b, idx, o, t)
	case *model.BlockerClear:
		return s.clear(b, idx, o, t)

	case *model.SourceIntake:
		key := SourceKey{Project: b.Project, Source: t.SourceID}
		if _, ok := s.sources[key]; ok {
			return faultAt(CodeDuplicateRecord, b.Sequence, idx, "source_id", "source already captured")
		}
		s.sources[key] = Source{Key: key, Intake: *t, Origin: o}
		return nil

	case *model.InvocationStart:
		return s.invocationStart(b, idx, o, t)
	case *model.InvocationSeal:
		return s.invocationSeal(b, idx, o, t)
	case *model.CriterionFix:
		return s.criterionFix(b, idx, o, t)
	case *model.ReviewAdmit:
		return s.reviewAdmit(b, idx, o, t)

	case *model.ProofAdmit:
		return s.proofAdmit(b, idx, t)
	case *model.DecisionDispose:
		return s.decisionDispose(b, idx, t)
	case *model.Supersede:
		return s.supersede(b, idx, t)
	case *model.TrustWithdraw:
		return s.requireKind(b, idx, t.Instrument, model.Instrument, "instrument")
	case *model.Correction, *model.ArtifactDispose:
		// Loss stays as an admitted fact. Query-time graph expansion also
		// reaches dependents introduced after this bundle.
		return nil
	}
	return faultAt("unknown-event", b.Sequence, idx, "event.type",
		"no reducer route for "+string(e.EventType()))
}

func (s *state) create(b model.Bundle, idx int, o Origin, kind model.Kind, id model.ID, p model.Provenance, fill func(*Record)) error {
	who := Ident{Project: b.Project, ID: id}
	if _, ok := s.current[who]; ok {
		return faultAt(CodeDuplicateRecord, b.Sequence, idx, "id",
			fmt.Sprintf("%s already exists in this project", id))
	}
	key := RecordKey{Project: b.Project, ID: id, Revision: 1}
	r := Record{Key: key, Kind: kind, Provenance: p, Origin: o}
	fill(&r)
	s.records[key] = r
	s.current[who] = 1
	if kind == model.Task {
		return s.checkPrerequisites(b, idx, *r.Task)
	}
	return nil
}

func (s *state) amend(b model.Bundle, idx int, o Origin, kind model.Kind, target model.RecordRef, p model.Provenance, fill func(*Record)) error {
	prior, ok := s.records[recordKey(target)]
	if !ok {
		return faultAt(CodeUnknownReference, b.Sequence, idx, "target",
			fmt.Sprintf("no admitted revision %d of %s", target.Revision, target.RecordID))
	}
	if prior.Kind != kind {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, "target",
			fmt.Sprintf("%s is a %s, not a %s", target.RecordID, prior.Kind, kind))
	}
	who := ident(target)
	if kind == model.Task {
		// Amending a closed task would let the acceptance criteria a closure
		// witnessed be rewritten after the fact, which turns a settled answer
		// into a moving one. Supersession is the path for revisiting it.
		if _, closed := s.closed[who]; closed {
			return faultAt(CodeInvalidTransition, b.Sequence, idx, "target", "a closed task cannot be amended")
		}
	}
	key := RecordKey{Project: b.Project, ID: target.RecordID, Revision: target.Revision + 1}
	r := Record{Key: key, Kind: kind, Provenance: p, Origin: o}
	fill(&r)
	s.records[key] = r
	s.current[who] = key.Revision
	if kind == model.Task {
		return s.checkPrerequisites(b, idx, *r.Task)
	}
	return nil
}

// checkPrerequisites refuses a typed prerequisite pointed at the wrong kind of
// record. A task-success prerequisite naming a CLAIM would evaluate to UNKNOWN
// forever, which reads as "waiting" when it is really "malformed".
func (s *state) checkPrerequisites(b model.Bundle, idx int, spec model.TaskSpec) error {
	for i, r := range spec.Prerequisites {
		if r.Target.Project != b.Project {
			continue // resolves on read
		}
		rec, ok := s.records[recordKey(r.Target)]
		if !ok {
			continue // the reference check already refused, or it is cross-project
		}
		var want model.Kind
		switch r.Kind {
		case "task-success":
			want = model.Task
		case "claim-proof":
			want = model.Claim
		case "decision-approved":
			want = model.Decision
		}
		if rec.Kind != want {
			return faultAt(CodeInvalidField, b.Sequence, idx,
				fmt.Sprintf("prerequisites[%d].target", i),
				fmt.Sprintf("%s prerequisite must name a %s, not a %s", r.Kind, want, rec.Kind))
		}
	}
	return nil
}

func (s *state) taskAt(b model.Bundle, idx int, ref model.RecordRef, path string) (Record, error) {
	rec, ok := s.records[recordKey(ref)]
	if !ok {
		return Record{}, faultAt(CodeUnknownReference, b.Sequence, idx, path,
			fmt.Sprintf("no admitted revision %d of %s", ref.Revision, ref.RecordID))
	}
	if rec.Kind != model.Task {
		return Record{}, faultAt(CodeInvalidTransition, b.Sequence, idx, path,
			fmt.Sprintf("%s is a %s", ref.RecordID, rec.Kind))
	}
	if _, closed := s.closed[ident(ref)]; closed {
		return Record{}, faultAt(CodeInvalidTransition, b.Sequence, idx, path, "the task is closed")
	}
	return rec, nil
}

func (s *state) start(b model.Bundle, idx int, o Origin, e *model.TaskStart) error {
	if _, err := s.taskAt(b, idx, e.Task, "task"); err != nil {
		return err
	}
	key := AttemptKey{Project: b.Project, Task: e.Task.RecordID, Attempt: e.AttemptID}
	if _, ok := s.attemptOwner[Ident{Project: b.Project, ID: e.AttemptID}]; ok {
		return faultAt(CodeDuplicateRecord, b.Sequence, idx, "attempt_id", "attempt already admitted")
	}
	// A second start while an attempt is live would give one task two owners
	// with no record that the first was displaced. That is what takeover is,
	// and takeover carries the confirmation that the prior writer stopped.
	for _, a := range s.attemptsFor(ident(e.Task)) {
		if a.Live() {
			return faultAt(CodeInvalidTransition, b.Sequence, idx, "attempt_id",
				fmt.Sprintf("attempt %s is still live; use takeover", a.Key.Attempt))
		}
	}
	s.attempts[key] = Attempt{
		Key:          key,
		TaskRevision: e.Task.Revision,
		Actor:        e.Actor,
		Started:      o,
	}
	s.attemptOwner[Ident{Project: b.Project, ID: e.AttemptID}] = key
	return nil
}

func (s *state) takeover(b model.Bundle, idx int, o Origin, e *model.TaskTakeover) error {
	if _, err := s.taskAt(b, idx, e.Task, "task"); err != nil {
		return err
	}
	prior := AttemptKey{Project: b.Project, Task: e.Task.RecordID, Attempt: e.PriorAttemptID}
	if _, ok := s.attempts[prior]; !ok {
		return faultAt(CodeUnknownReference, b.Sequence, idx, "prior_attempt_id",
			"no admitted prior attempt under this task")
	}
	key := AttemptKey{Project: b.Project, Task: e.Task.RecordID, Attempt: e.AttemptID}
	if _, ok := s.attemptOwner[Ident{Project: b.Project, ID: e.AttemptID}]; ok {
		return faultAt(CodeDuplicateRecord, b.Sequence, idx, "attempt_id", "attempt already admitted")
	}
	// The prior attempt is left exactly as it was. A takeover does not write a
	// terminal receipt on someone else's behalf, so an abandoned attempt stays
	// visibly live and the task stays IN FLIGHT until its own holder answers.
	s.attempts[key] = Attempt{
		Key:          key,
		TaskRevision: e.Task.Revision,
		Actor:        e.Actor,
		Takeover:     true,
		PriorAttempt: e.PriorAttemptID,
		Started:      o,
	}
	s.attemptOwner[Ident{Project: b.Project, ID: e.AttemptID}] = key
	return nil
}

func (s *state) terminal(b model.Bundle, idx int, o Origin, e *model.AttemptTerminal) error {
	key := AttemptKey{Project: b.Project, Task: e.Task.RecordID, Attempt: e.AttemptID}
	a, ok := s.attempts[key]
	if !ok {
		return faultAt(CodeUnknownReference, b.Sequence, idx, "attempt_id",
			"no admitted attempt under this task")
	}
	if a.Terminal != nil {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, "attempt_id",
			"the attempt already has a terminal receipt")
	}
	a.Terminal = &Terminal{
		Outcome:            e.Outcome,
		Reason:             e.Reason,
		NextAction:         e.NextAction,
		DeliveryRefs:       e.DeliveryRefs,
		CommitsDenied:      e.CommitsDenied,
		ReconciliationOwed: e.ReconciliationOwed,
		Origin:             o,
	}
	s.attempts[key] = a
	return nil
}

func (s *state) close(b model.Bundle, idx int, o Origin, e *model.TaskClose) error {
	if _, err := s.taskAt(b, idx, e.Task, "task"); err != nil {
		return err
	}
	// Whether every attempt is terminal, and whether a success closure has
	// applicable witnesses, are PROJECTION questions, evaluated in order in
	// task.go. They are not refused here: an authorised closure is a fact, and
	// dropping it would hide that someone closed a task with a writer still in
	// it. The admission gate refuses, the reducer records and then projects.
	who := ident(e.Task)
	if _, ok := s.closed[who]; ok {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, "task", "the task is already closed")
	}
	s.closed[who] = Closure{
		Task:                e.Task,
		Outcome:             e.Outcome,
		Authority:           e.Authority,
		AcceptanceWitnesses: e.AcceptanceWitnessRefs,
		DeliveryWitnesses:   e.DeliveryWitnessRefs,
		Origin:              o,
	}
	return nil
}

func (s *state) hold(b model.Bundle, idx int, o Origin, e *model.BlockerHold) error {
	if _, err := s.taskAt(b, idx, e.Task, "task"); err != nil {
		return err
	}
	key := BlockerKey{Project: b.Project, Task: e.Task.RecordID, Blocker: e.BlockerID}
	// Reuse is refused even after the hold was cleared. Rebinding a blocker id
	// would rewrite what an already-admitted clear resolved.
	if _, ok := s.blockers[key]; ok {
		return faultAt(CodeDuplicateRecord, b.Sequence, idx, "blocker_id", "blocker id already used on this task")
	}
	s.blockers[key] = Blocker{
		Key:          key,
		TaskRevision: e.Task.Revision,
		Reason:       e.Reason,
		Actor:        e.Actor,
		Criterion:    e.Criterion,
		Held:         o,
	}
	return nil
}

func (s *state) clear(b model.Bundle, idx int, o Origin, e *model.BlockerClear) error {
	key := BlockerKey{Project: b.Project, Task: e.Task.RecordID, Blocker: e.BlockerID}
	held, ok := s.blockers[key]
	if !ok {
		return faultAt(CodeUnknownReference, b.Sequence, idx, "blocker_id", "no admitted hold with this id")
	}
	if held.Cleared != nil {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, "blocker_id", "the hold is already cleared")
	}
	witness := e.ResolvingWitness
	held.Cleared = &o
	held.Witness = &witness
	s.blockers[key] = held
	return nil
}

func (s *state) invocationStart(b model.Bundle, idx int, o Origin, e *model.InvocationStart) error {
	key := InvocationKey{Project: b.Project, InvocationID: e.Envelope.InvocationID}
	if _, ok := s.invocations[key]; ok {
		return faultAt(CodeDuplicateRecord, b.Sequence, idx, "envelope.invocation_id", "invocation already admitted")
	}
	owner, ok := s.attemptOwner[Ident{Project: b.Project, ID: e.Envelope.AttemptID}]
	if !ok {
		return faultAt(CodeUnknownReference, b.Sequence, idx, "envelope.attempt_id",
			"no admitted attempt owns this invocation")
	}
	s.invocations[key] = Invocation{Key: key, Attempt: owner, Start: e.Envelope, Started: o}
	return nil
}

func (s *state) invocationSeal(b model.Bundle, idx int, o Origin, e *model.InvocationSeal) error {
	key := invocationKey(e.StartRef)
	inv, ok := s.invocations[key]
	if !ok {
		return faultAt(CodeUnknownReference, b.Sequence, idx, "start_ref", "no admitted invocation start")
	}
	if inv.Seal != nil {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, "start_ref", "the invocation is already sealed")
	}
	// Intent is immutable: a seal that renames the instrument, the argv or the
	// criterion is a different run wearing this one's identity.
	if e.Envelope.AttemptID != inv.Start.AttemptID || e.Envelope.InstrumentRef != inv.Start.InstrumentRef {
		return faultAt(CodeInvalidField, b.Sequence, idx, "envelope",
			"the seal disagrees with the admitted pre-launch intent")
	}
	seal := e.Envelope
	inv.Seal = &seal
	inv.Sealed = &o
	s.invocations[key] = inv
	return nil
}

func (s *state) criterionFix(b model.Bundle, idx int, o Origin, e *model.CriterionFix) error {
	rec, ok := s.records[recordKey(e.Claim)]
	if !ok {
		return faultAt(CodeUnknownReference, b.Sequence, idx, "claim",
			fmt.Sprintf("no admitted revision %d of %s", e.Claim.Revision, e.Claim.RecordID))
	}
	if rec.Kind != model.Claim {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, "claim",
			fmt.Sprintf("%s is a %s", e.Claim.RecordID, rec.Kind))
	}
	key := CriterionKey{
		Project:           b.Project,
		Claim:             e.Claim.RecordID,
		ClaimRevision:     e.Claim.Revision,
		CriterionID:       e.CriterionID,
		CriterionRevision: e.Revision,
	}
	if _, ok := s.criteria[key]; ok {
		return faultAt(CodeDuplicateRecord, b.Sequence, idx, "revision", "criterion revision already fixed")
	}
	s.criteria[key] = Criterion{Key: key, Fix: *e, Origin: o}
	return nil
}

func (s *state) reviewAdmit(b model.Bundle, idx int, o Origin, e *model.ReviewAdmit) error {
	for i, p := range e.Packets {
		key := ReviewKey{Project: b.Project, CommandID: p.CommandID}
		if prior, ok := s.reviews[key]; ok {
			return faultAt(CodeInvalidTransition, b.Sequence, idx, fmt.Sprintf("packets[%d]", i),
				fmt.Sprintf("packet already dispositioned %q at sequence %d", prior.Outcome, prior.Origin.Sequence))
		}
		s.reviews[key] = Review{Key: key, Packet: p, Outcome: e.Outcome, Actor: e.Actor, Reason: e.Reason, Origin: o}
	}
	return nil
}

// ---- accessors -----------------------------------------------------------

// Project is the declared project this ledger belongs to, empty for an empty
// snapshot.
func (s Snapshot) Project() model.ProjectID { return s.inner().project }

// Watermark is the selected ledger position this snapshot answers from.
func (s Snapshot) Watermark() Watermark { return s.inner().watermark }

// Record returns one exact admitted revision.
func (s Snapshot) Record(ref model.RecordRef) (Record, bool) {
	r, ok := s.inner().record(ref)
	return deepCopy(r), ok
}

// record is the uncopied read. Callers inside this package scan without
// paying for a copy they are about to discard; only the exported accessor,
// where a value leaves the package, copies.
func (s *state) record(ref model.RecordRef) (Record, bool) {
	r, ok := s.records[recordKey(ref)]
	return r, ok
}

// CurrentRevision is the highest admitted revision of a record.
func (s Snapshot) CurrentRevision(id Ident) (model.Revision, bool) {
	r, ok := s.inner().current[id]
	return r, ok
}

// Current returns a record at its current revision.
func (s Snapshot) Current(id Ident) (Record, bool) {
	r, ok := s.inner().currentRecord(id)
	return deepCopy(r), ok
}

func (s *state) currentRecord(id Ident) (Record, bool) {
	rev, ok := s.current[id]
	if !ok {
		return Record{}, false
	}
	r, ok := s.records[RecordKey{Project: id.Project, ID: id.ID, Revision: rev}]
	return r, ok
}

// Records returns every admitted revision, sorted by project, id then revision.
// Sorting is not cosmetic: a map range would make the answer depend on Go's
// randomized iteration, and two runs of the same ledger must agree exactly.
func (s Snapshot) Records() []Record {
	return deepCopySlice(s.inner().recordsSorted())
}

func (s *state) recordsSorted() []Record {
	out := make([]Record, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return lessRecordKey(out[i].Key, out[j].Key) })
	return out
}

func lessRecordKey(a, b RecordKey) bool {
	if a.Project != b.Project {
		return a.Project < b.Project
	}
	if a.ID != b.ID {
		return a.ID < b.ID
	}
	return a.Revision < b.Revision
}

func (s *state) attemptsFor(id Ident) []Attempt {
	out := []Attempt{}
	for _, a := range s.attempts {
		if a.Key.Project == id.Project && a.Key.Task == id.ID {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Started != out[j].Started {
			return out[i].Started.before(out[j].Started)
		}
		return out[i].Key.Attempt < out[j].Key.Attempt
	})
	return out
}

func (s *state) blockersFor(id Ident) []Blocker {
	out := []Blocker{}
	for _, bl := range s.blockers {
		if bl.Key.Project == id.Project && bl.Key.Task == id.ID {
			out = append(out, bl)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Held != out[j].Held {
			return out[i].Held.before(out[j].Held)
		}
		return out[i].Key.Blocker < out[j].Key.Blocker
	})
	return out
}

// Attempts returns one task's attempts in ledger order.
func (s Snapshot) Attempts(id Ident) []Attempt { return deepCopySlice(s.inner().attemptsFor(id)) }

// Blockers returns one task's holds in ledger order, cleared ones included.
func (s Snapshot) Blockers(id Ident) []Blocker { return deepCopySlice(s.inner().blockersFor(id)) }

// Closure returns the admitted closure, whether or not it takes effect.
func (s Snapshot) Closure(id Ident) (Closure, bool) {
	c, ok := s.inner().closed[id]
	return deepCopy(c), ok
}

// Invocation returns one start/seal pair.
func (s Snapshot) Invocation(key InvocationKey) (Invocation, bool) {
	i, ok := s.inner().invocations[key]
	return deepCopy(i), ok
}

// Invocations returns every invocation, sorted by project then id.
func (s Snapshot) Invocations() []Invocation {
	return deepCopySlice(s.inner().invocationsSorted())
}

func (s *state) invocationsSorted() []Invocation {
	out := make([]Invocation, 0, len(s.invocations))
	for _, i := range s.invocations {
		out = append(out, i)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key.Project != out[j].Key.Project {
			return out[i].Key.Project < out[j].Key.Project
		}
		return out[i].Key.InvocationID < out[j].Key.InvocationID
	})
	return out
}

// Criterion returns one frozen predicate at its exact claim and criterion
// revisions.
func (s Snapshot) Criterion(ref model.CriterionRef) (Criterion, bool) {
	c, ok := s.inner().criteria[criterionKey(ref)]
	return deepCopy(c), ok
}

// Review returns the admitted disposition of one intake packet.
func (s Snapshot) Review(key ReviewKey) (Review, bool) {
	r, ok := s.inner().reviews[key]
	return deepCopy(r), ok
}

// Sources returns every captured source, sorted by project then id.
func (s Snapshot) Sources() []Source {
	return deepCopySlice(s.inner().sourcesSorted())
}

func (s *state) sourcesSorted() []Source {
	out := make([]Source, 0, len(s.sources))
	for _, v := range s.sources {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key.Project != out[j].Key.Project {
			return out[i].Key.Project < out[j].Key.Project
		}
		return out[i].Key.Source < out[j].Key.Source
	})
	return out
}

func sortedReferrers(in []Referrer) []Referrer {
	out := deepCopySlice(in)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Origin != out[j].Origin {
			return out[i].Origin.before(out[j].Origin)
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// RecordReferrers returns every admitted event that named this exact revision.
func (s Snapshot) RecordReferrers(ref model.RecordRef) []Referrer {
	return sortedReferrers(s.inner().reverseRecord[recordKey(ref)])
}

// CriterionReferrers returns every admitted event that named this criterion.
func (s Snapshot) CriterionReferrers(ref model.CriterionRef) []Referrer {
	return sortedReferrers(s.inner().reverseCriterion[criterionKey(ref)])
}

// InvocationReferrers returns every admitted event that named this invocation.
func (s Snapshot) InvocationReferrers(ref model.InvocationRef) []Referrer {
	return sortedReferrers(s.inner().reverseInvocation[invocationKey(ref)])
}

// Deferred is empty because all named events now have reducer semantics.
func (s Snapshot) Deferred() []Deferred { return nil }
