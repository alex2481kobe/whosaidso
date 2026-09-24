package model

// Typed event dependency discovery and project filtering live here.
// Artifact resolution, event encoding, and stateful dependency checks do not.
// This file stays below 200 lines because the reference walker is one complete unit.

import (
	"fmt"
	"strings"
)

// Reference is one exact dependency discovered by the model, with its field path.
// Exactly one branch is set. Artifact pins are deliberately not record referents;
// Evaluation resolves bytes, while admission and the views use this walker for
// project ledger references.
type Reference struct {
	Path       string
	Record     *RecordRef
	Criterion  *CriterionRef
	Invocation *InvocationRef
	Blocker    *BlockerRef
}

func (r Reference) Project() ProjectID {
	if r.Record != nil {
		return r.Record.Project
	}
	if r.Criterion != nil {
		return r.Criterion.Claim.Project
	}
	if r.Invocation != nil {
		return r.Invocation.Project
	}
	if r.Blocker != nil {
		return r.Blocker.Task.Project
	}
	return ""
}

// EventReferences is the sole typed reference walker for admission and queries.
// A new event must acquire an explicit branch here; JSON field-name heuristics
// cannot distinguish an authored target from a coincidentally named config knob.
func EventReferences(event TypedEvent) ([]Reference, error) {
	if err := ValidateSchema(event); err != nil {
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
		if e.Authority != nil {
			w.authority(*e.Authority, "authority")
		}
	case *BlockerHold:
		w.record(e.Task, "task")
	case *BlockerClear:
		w.record(e.Task, "task")
		w.blocker(e.HoldRef, "hold_ref")
	case *InvocationStart:
		w.envelope(e.Envelope, "envelope")
	case *InvocationSeal:
		w.invocation(e.StartRef, "start_ref")
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
			w.invocation(o.InvocationRef, fmt.Sprintf("evidence[%d].invocation_ref", i))
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
func (w *referenceWalker) invocation(r InvocationRef, p string) {
	w.refs = append(w.refs, Reference{Path: p, Invocation: &r})
}
func (w *referenceWalker) blocker(r BlockerRef, p string) {
	w.refs = append(w.refs, Reference{Path: p, Blocker: &r})
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
