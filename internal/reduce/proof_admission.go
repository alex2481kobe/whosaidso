package reduce

// Proof, decision, and supersession transition checks and observation validity live here.
// Snapshot projections and support-loss traversal do not.
// This file stays below 200 lines because these transition checks form a complete responsibility.

import (
	"fmt"
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

// proofAdmit checks applicability recoverable from the ledger: each listed
// member by its family class, support, and closure over the whole family
// (proof_family.go), and that the judgment is the packet author's
// (packet_author.go). Evaluation of artifact bytes and pending intake remain
// admission gate work.
func (s *state) proofAdmit(b model.Bundle, idx int, e *model.ProofAdmit) error {
	// Every refusal goes through refuseProof: a fold stops at the first, in
	// this order; a dry run (proof_check.go) records it and keeps checking.
	if err := s.requireKind(b, idx, e.Claim, model.Claim, "claim"); err != nil {
		if err = s.refuseProof(err); err != nil {
			return err
		}
	}
	if err := s.checkPacketAuthor(b, idx, e.Judgment.Actor, "judgment.actor"); err != nil {
		if err = s.refuseProof(err); err != nil {
			return err
		}
	}
	// R14.1: a supports proof counts supporting members and may not carry an
	// unresolved contradiction; a refutes proof counts contradicting members
	// and never counts a member as support. Either way the counted members
	// meet the same observation and instrument bar.
	refutes := e.Refutes()
	// Every proof, either verdict, judges the claim's current criterion
	// revision: the latest proof decides status, so a supports proof on a
	// superseded revision would flip a refuted claim back to PROVEN.
	if s.laterCriterionRevision(e.CriterionRef) {
		if err := s.refuseProof(faultAt(CodeInvalidTransition, b.Sequence, idx, "criterion_ref.revision",
			"a proof judges the claim's current criterion revision, and a later one is admitted")); err != nil {
			return err
		}
	}
	counted := false
	listed := map[InvocationKey]string{}
	for i, member := range e.Evidence {
		key := invocationKey(member.InvocationRef)
		listed[key] = member.Disposition
		inv, class := s.memberClass(e.CriterionRef, key)
		var refusal error
		switch {
		case class == MemberRejected:
			if !setAside(member.Disposition) {
				refusal = faultAt(CodeRejectedFamilyMember, b.Sequence, idx, fmt.Sprintf("evidence[%d].disposition", i),
					"a rejected run can only be dispositioned inapplicable or inconclusive, never "+member.Disposition)
			}
			// R10.3: accounted for, never support
		case class == MemberOutside || inv.Seal == nil:
			refusal = faultAt(CodeInvalidTransition, b.Sequence, idx, "evidence", "proof requires local sealed observations of the exact criterion")
		case class == MemberEarlier:
			if !setAside(member.Disposition) {
				refusal = faultAt(CodeInvalidTransition, b.Sequence, idx, "evidence", "a run under an earlier criterion revision can only be dispositioned inapplicable or inconclusive")
			}
		case member.Disposition == "supports" && refutes:
			refusal = faultAt(CodeInvalidTransition, b.Sequence, idx, fmt.Sprintf("evidence[%d].disposition", i), "a refuting proof counts no member as support")
		case member.Disposition == "contradicts" && !refutes:
			refusal = faultAt(CodeInvalidTransition, b.Sequence, idx, "evidence", "contradicting evidence is unresolved")
		case member.Disposition != "supports" && member.Disposition != "contradicts":
		default:
			refusal = s.countedMember(b, idx, e, inv)
			counted = counted || refusal == nil
		}
		if refusal == nil && member.CodeChange != nil {
			refusal = s.checkCodeChange(b, idx, i, e, inv, class)
		}
		if refusal != nil {
			if err := s.refuseProof(refusal); err != nil {
				return err
			}
		}
	}
	if err := s.checkCurrentCommit(b, idx, e); err != nil {
		if err = s.refuseProof(err); err != nil {
			return err
		}
	}
	if !counted {
		detail := "proof has no supporting local observation"
		if refutes {
			detail = "a refuting proof names no contradicting run of its criterion revision"
		}
		if err := s.refuseProof(faultAt(CodeInvalidTransition, b.Sequence, idx, "evidence", detail)); err != nil {
			return err
		}
	}
	if err := s.checkFamilyClosure(b, idx, e, listed); err != nil {
		return err
	}
	if err := s.checkRejectedFamily(b, idx, e, listed); err != nil {
		return err
	}
	// A refutation needs no support from the claim: losing support never
	// stops a failing run from being recorded against it.
	if !refutes && len(s.supportLosses(recordNode(e.Claim))) != 0 {
		return s.refuseProof(faultAt(CodeInvalidTransition, b.Sequence, idx, "claim", "claim has unresolved support loss"))
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
	return s.supersedeRules(b, idx, e)
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
