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

// Replay, bundle sequencing, and the state fork that keeps Apply pure live here.
// Event admission, transitions, fact types, and snapshot reads live in separate files.

import (
	"fmt"
	"strings"

	"datum/internal/model"
)

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
	eventPackets map[Origin]ReviewKey // accepted event -> the packet that carried it
	sources      map[SourceKey]Source
	commands     map[model.ID]uint64
	events       map[Origin]model.TypedEvent

	reverseRecord     map[RecordKey][]Referrer
	reverseCriterion  map[CriterionKey][]Referrer
	reverseInvocation map[InvocationKey][]Referrer
	reverseBlocker    map[BlockerKey][]Referrer

	// bundle is the candidate bundle's inventory while it is applied, nil
	// otherwise. clone never copies it.
	bundle *bundleFacts
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
		eventPackets:      map[Origin]ReviewKey{},
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
		eventPackets:      copyMap(s.eventPackets),
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
	facts, err := indexBundle(b)
	if err != nil {
		return err
	}
	s.project = b.Project
	s.bundle = facts
	defer func() { s.bundle = nil }()
	for i, typed := range facts.events {
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
