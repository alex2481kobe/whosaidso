package reduce

// Event dispatch and record, invocation, criterion, and review transitions live here.
// Task lifecycle transitions and derived proof/task answers do not.

import (
	"fmt"
	"reflect"

	"datum/internal/model"
)

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
	if err := s.checkInvocationConfig(b, idx, e.Envelope); err != nil {
		return err
	}
	if err := s.checkStartFrozen(b, idx, e.Envelope); err != nil {
		return err
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
	// Only observations may change: effective config, observed conditions,
	// isolation, observation time, outcome, outputs, and visual observations.
	start, end := inv.Start, e.Envelope
	for _, field := range []struct {
		name string
		same bool
	}{
		{"invocation_id", start.InvocationID == end.InvocationID},
		{"attempt_id", start.AttemptID == end.AttemptID},
		{"instrument_ref", start.InstrumentRef == end.InstrumentRef},
		{"criterion_ref", reflect.DeepEqual(start.CriterionRef, end.CriterionRef)},
		{"execution_source_identity", reflect.DeepEqual(start.ExecutionSourceIdentity, end.ExecutionSourceIdentity)},
		{"argv", reflect.DeepEqual(start.Argv, end.Argv)},
		{"input_refs", reflect.DeepEqual(start.InputRefs, end.InputRefs)},
		{"config_requested", reflect.DeepEqual(start.ConfigRequested, end.ConfigRequested)},
		{"conditions_declared", reflect.DeepEqual(start.ConditionsDeclared, end.ConditionsDeclared)},
		{"started_at", start.StartedAt.Equal(end.StartedAt)},
	} {
		if !field.same {
			return faultAt(CodeInvalidField, b.Sequence, idx, "envelope."+field.name,
				"the seal disagrees with the admitted pre-launch intent")
		}
	}
	if err := s.checkInvocationConfig(b, idx, e.Envelope); err != nil {
		return err
	}
	seal := e.Envelope
	inv.Seal = &seal
	inv.Sealed = &o
	s.invocations[key] = inv
	return nil
}

// checkInvocationConfig holds requested and effective config names to the
// exact instrument revision whenever it is in state, so replay and admission
// share one rule. An unresolvable revision is the reference check's answer.
func (s *state) checkInvocationConfig(b model.Bundle, idx int, env model.InvocationEnvelope) error {
	rec, ok := s.records[recordKey(env.InstrumentRef)]
	if !ok || rec.Kind != model.Instrument || rec.Instrument == nil {
		return nil
	}
	if err := model.ValidateInvocationConfig(env, *rec.Instrument); err != nil {
		if f, ok := err.(*model.Fault); ok {
			return faultAt(f.Code, b.Sequence, idx, "envelope."+f.Path, f.Detail)
		}
		return faultAt(CodeInvalidField, b.Sequence, idx, "envelope", err.Error())
	}
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
	if err := s.checkPacketAuthor(b, idx, e.Author, "author"); err != nil {
		return err
	}
	s.criteria[key] = Criterion{Key: key, Fix: *e, Origin: o, RecordedAt: b.RecordedAt.UTC()}
	return nil
}

func (s *state) reviewAdmit(b model.Bundle, idx int, o Origin, e *model.ReviewAdmit) error {
	for i, p := range e.Packets {
		key := ReviewKey{Project: b.Project, CommandID: p.CommandID}
		if prior, ok := s.reviews[key]; ok {
			return faultAt(CodeInvalidTransition, b.Sequence, idx, fmt.Sprintf("packets[%d]", i),
				fmt.Sprintf("packet already dispositioned %q at sequence %d", prior.Outcome, prior.Origin.Sequence))
		}
		var invocations []model.ReviewedInvocation
		for _, inv := range e.Invocations {
			if inv.Packet == p.CommandID {
				invocations = append(invocations, inv)
			}
		}
		// Validation makes authors cover exactly the reviewed packets.
		author := e.Authors[p.CommandID]
		s.reviews[key] = Review{Key: key, Packet: p, Outcome: e.Outcome, Actor: e.Actor, Reason: e.Reason, Origin: o,
			SelfAdmission: selfAdmission(author, e.Actor), Invocations: invocations, Author: author}
	}
	s.attributeEvents(b, e)
	return nil
}
