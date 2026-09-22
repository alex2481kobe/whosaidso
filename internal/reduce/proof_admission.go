package reduce

// Proof, decision, and supersession transition checks and observation validity live here.
// Snapshot projections and support-loss traversal do not.
// This file stays below 200 lines because these transition checks form a complete responsibility.

import (
	"reflect"

	"datum/internal/model"
)

// completedObservation uses the immutable start's exact local assertion link.
// A failed run with captured output is still an observation. An unknown terminal
// state, a launch failure or absent output cannot establish a reading.
func completedObservation(inv Invocation, claim model.RecordRef) bool {
	start := inv.Start
	if inv.Key.Project != claim.Project || start.ExecutionSourceIdentity.Project != claim.Project || start.InstrumentRef.Project != claim.Project ||
		start.CriterionRef.State != model.Known || start.CriterionRef.Value == nil || start.CriterionRef.Value.Claim != claim || inv.Seal == nil {
		return false
	}
	if !matchesInvocationIntent(inv) {
		return false
	}
	seal := inv.Seal
	return seal.ObservedAt.State == model.Known && seal.Outcome.State == model.Known && seal.Outcome.Value != nil &&
		seal.Outcome.Value.Kind != "spawn-failed" && seal.OutputRefs.State == model.Known && seal.OutputRefs.Value != nil && len(*seal.OutputRefs.Value) > 0
}

func (s *state) requireKind(b model.Bundle, idx int, ref model.RecordRef, kind model.Kind, path string) error {
	rec, ok := s.records[recordKey(ref)]
	if !ok {
		return faultAt(CodeUnknownReference, b.Sequence, idx, path, "no local admitted revision")
	}
	if rec.Kind != kind {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, path, "target is not a "+string(kind))
	}
	return nil
}

// proofAdmit checks applicability recoverable from admitted facts. Evaluation of
// artifact bytes, family closure and semantic judgment remain admission gate work.
func (s *state) proofAdmit(b model.Bundle, idx int, e *model.ProofAdmit) error {
	if err := s.requireKind(b, idx, e.Claim, model.Claim, "claim"); err != nil {
		return err
	}
	supported := false
	criterion := s.criteria[criterionKey(e.CriterionRef)]
	for _, member := range e.Evidence {
		inv, ok := s.invocations[invocationKey(member.InvocationRef)]
		if !ok || inv.Key.Project != b.Project || inv.Seal == nil || inv.Start.CriterionRef.Value == nil || *inv.Start.CriterionRef.Value != e.CriterionRef {
			return faultAt(CodeInvalidTransition, b.Sequence, idx, "evidence", "proof requires local sealed observations of the exact criterion")
		}
		if !criterion.Origin.before(inv.Started) {
			return faultAt(CodeInvalidTransition, b.Sequence, idx, "criterion_ref", "criterion was not fixed before the invocation start")
		}
		if member.Disposition == "contradicts" {
			return faultAt(CodeInvalidTransition, b.Sequence, idx, "evidence", "contradicting evidence is unresolved")
		}
		if member.Disposition != "supports" {
			continue
		}
		instrument, ok := s.records[recordKey(inv.Start.InstrumentRef)]
		if !completedObservation(inv, e.Claim) || !ok || instrument.Kind != model.Instrument || instrument.Instrument.Validation.State != model.Known {
			return faultAt(CodeInvalidTransition, b.Sequence, idx, "evidence", "support requires a completed observation from a validated local instrument")
		}
		if len(s.supportLosses(invocationNode(inv.Key))) != 0 {
			return faultAt(CodeInvalidTransition, b.Sequence, idx, "evidence", "observation has unresolved support loss")
		}
		supported = true
	}
	if !supported {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, "evidence", "proof has no supporting local observation")
	}
	if len(s.supportLosses(recordNode(e.Claim))) != 0 {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, "claim", "claim has unresolved support loss")
	}
	return nil
}

func sameSet[T comparable](a, b []T) bool {
	left, right := map[T]bool{}, map[T]bool{}
	for _, v := range a {
		left[v] = true
	}
	for _, v := range b {
		right[v] = true
	}
	return reflect.DeepEqual(left, right)
}

// Scope prose has no executable containment relation. Exact declared semantics
// are the supported case until admission supplies a richer authored relation.
func sameScope(a, b model.Scope) bool {
	return a.AppliesWhen == b.AppliesWhen && a.Limitations == b.Limitations && sameSet(a.SourcePaths, b.SourcePaths) && sameSet(a.ContextRefs, b.ContextRefs)
}

func (s *state) decisionDispose(b model.Bundle, idx int, e *model.DecisionDispose) error {
	if err := s.requireKind(b, idx, e.Decision, model.Decision, "decision"); err != nil {
		return err
	}
	rec := s.records[recordKey(e.Decision)]
	if model.Blank(e.Authority.Actor.ID) || !sameScope(rec.Decision.Scope, e.Scope) || !sameScope(e.Scope, e.Authority.Scope) {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, "authority", "disposition requires a named authority and the exact declared scope")
	}
	return nil
}

func (s *state) supersede(b model.Bundle, idx int, e *model.Supersede) error {
	prior := s.records[recordKey(e.Prior)]
	replacement, ok := s.records[recordKey(e.Replacement)]
	if e.Prior == e.Replacement || (ok && prior.Kind != replacement.Kind) {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, "replacement", "supersession needs a different record revision of the same kind")
	}
	return nil
}

func matchesInvocationIntent(inv Invocation) bool {
	if inv.Seal == nil {
		return false
	}
	start, seal := inv.Start, *inv.Seal
	if start.InvocationID != seal.InvocationID || start.AttemptID != seal.AttemptID || start.InstrumentRef != seal.InstrumentRef ||
		!reflect.DeepEqual(start.CriterionRef, seal.CriterionRef) || !reflect.DeepEqual(start.ExecutionSourceIdentity, seal.ExecutionSourceIdentity) ||
		!reflect.DeepEqual(start.Argv, seal.Argv) || !reflect.DeepEqual(start.InputRefs, seal.InputRefs) ||
		!reflect.DeepEqual(start.ConfigRequested, seal.ConfigRequested) || !reflect.DeepEqual(start.ConditionsDeclared, seal.ConditionsDeclared) ||
		!start.StartedAt.Equal(seal.StartedAt) {
		return false
	}
	return true
}
