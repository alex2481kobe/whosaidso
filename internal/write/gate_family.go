package write

// Proof admission's artifact evaluation lives here: closure over pending,
// unreviewed intake carrying the criterion or an earlier revision of it, each
// exact-revision member's disposition against its own computed verdict (with
// the git check of a recorded code change, gate_code_change.go),
// criterion satisfaction, and re-resolution of each supporting instrument's
// validation artifact. Family membership, ledger closure, rejected members and
// earlier revisions are decided once, by the reducer's proof family checker,
// when the proposal replays. Enabling operations, criterion freezing and the
// transaction do not live here.

import (
	"context"
	"fmt"

	"github.com/alex2481kobe/whosaidso/internal/evidence"
	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func gateProofFamily(ctx context.Context, project store.Project, after reduce.Snapshot, intake *pendingIntake, e *model.ProofAdmit, dry *dryRun) error {
	criterion, ok := after.Criterion(e.CriterionRef)
	if !ok {
		return admissionFault("unknown-reference", "criterion_ref", "proof names no admitted criterion")
	}
	// The family includes runs under earlier revisions of this criterion: a new
	// revision cannot erase known counterevidence. The ledger's closure was
	// decided when the proposal replayed; unreviewed intake is outside it.
	carries := func(env model.InvocationEnvelope) bool {
		member, _ := reduce.CriterionFamily(env.CriterionRef, e.CriterionRef)
		return member
	}
	if err := gatePendingIntake(intake, after, carries); err != nil {
		return err
	}
	resolver := dry.resolver(project)
	supports := []evidence.Observation{}
	for i, member := range e.Evidence {
		path := fmt.Sprintf("evidence[%d]", i)
		// Earlier-revision and rejected-only members are accounted for under the
		// judgment by the reducer's rules, never evaluated or support.
		inv, class := after.ProofMember(e.CriterionRef, member.InvocationRef)
		if class != reduce.MemberExact {
			continue
		}
		if member.CodeChange != nil {
			if err := gateCodeChange(ctx, project, after, e.Claim, *member.CodeChange, path+".code_change"); err != nil {
				return err
			}
		}
		observation, err := resolver.Observe(ctx, criterion.Fix, *inv.Seal)
		if err != nil {
			return err
		}
		own, err := evidence.Evaluate(criterion.Fix, []evidence.Observation{observation})
		if err != nil {
			return err
		}
		// FALSE is a computed counterexample. Only "contradicts" names it
		// honestly; the reducer refuses it in a supports proof and counts it
		// in a refutes proof. It may be set aside as inapplicable only
		// beside a code change git verified above. And "contradicts" names
		// nothing else: a run that does not fail cannot refute.
		setAside := member.Disposition == "inapplicable" && member.CodeChange != nil
		if own.Verdict == evidence.False && member.Disposition != "contradicts" && !setAside {
			return admissionFault("counterevidence-unresolved", path+".disposition", "member fails the criterion: "+own.Reason)
		}
		if own.Verdict != evidence.False && member.Disposition == "contradicts" {
			return admissionFault("contradiction-unfounded", path+".disposition", fmt.Sprintf("member does not fail the criterion: it evaluates %s", own.Verdict))
		}
		counted := "supports"
		if e.Refutes() {
			counted = "contradicts"
		}
		if member.Disposition != counted {
			continue
		}
		if err := gateInstrumentValidation(ctx, resolver, after, inv.Start.InstrumentRef, path); err != nil {
			return err
		}
		supports = append(supports, observation)
	}
	// A refutation stands on its own counted FALSE members, each checked
	// above; there is no supporting family to satisfy.
	if e.Refutes() {
		return nil
	}
	// Every supporting member must be TRUE and comparable with the others;
	// one UNKNOWN or incomparable member leaves the family unsatisfied.
	family, err := evidence.Evaluate(criterion.Fix, supports)
	if err != nil {
		return err
	}
	if family.Verdict != evidence.True {
		return admissionFault("criterion-unsatisfied", "evidence", fmt.Sprintf("supporting family is %s: %s", family.Verdict, family.Reason))
	}
	return nil
}

// gateInstrumentValidation re-resolves the validation artifact at proof time.
// UNKNOWN validation, or KNOWN validation whose bytes no longer resolve inside
// the root, is not applicable validation.
func gateInstrumentValidation(ctx context.Context, resolver *evidence.Resolver, after reduce.Snapshot, ref model.RecordRef, path string) error {
	record, ok := after.Record(ref)
	if !ok || record.Instrument == nil || record.Instrument.Validation.State != model.Known || record.Instrument.Validation.Value == nil {
		return admissionFault("validation-unknown", path, "supporting member's instrument has no known validation")
	}
	artifact := record.Instrument.Validation.Value.Ref
	resolved, err := resolver.Resolve(ctx, artifact)
	if err != nil {
		return admissionFault("validation-unavailable", path, "instrument validation artifact does not resolve: "+err.Error())
	}
	reading, err := evidence.Select(resolved, artifact.Selector)
	if err != nil || reading.Kind == evidence.ReadingAbsent {
		return admissionFault("validation-unavailable", path, "instrument validation selector reads nothing")
	}
	return nil
}

// gatePendingIntake: durable intake nobody has reviewed yet that carries the
// criterion must be admitted in this set, so a proof is not admitted ahead of a
// run waiting in review. This is an admission-time "not yet", never a validity
// rule: reviewed packets, accepted or not, are judged from the ledger alone.
func gatePendingIntake(intake *pendingIntake, after reduce.Snapshot, carries func(model.InvocationEnvelope) bool) error {
	packets, err := intake.all()
	if err != nil {
		return err
	}
	for _, packet := range packets {
		if _, reviewed := after.Review(reduce.ReviewKey{Project: intake.project.ID, CommandID: packet.CommandID}); reviewed {
			continue
		}
		for _, raw := range packet.Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				return err
			}
			if env := invocationEnvelope(event); env != nil && carries(*env) {
				return admissionFault("pending-reconciliation", "intake/"+string(packet.CommandID),
					fmt.Sprintf("unadmitted intake carries invocation %s of this criterion; admit it in the same set", env.InvocationID))
			}
		}
	}
	return nil
}

// pendingIntake is the retained intake one admission's pending checks read:
// every packet listed in the inbox, read and fully verified by store.ReadIntake
// (owner-only real files, packet.json decoded and matched to its location,
// every blob hashed against its name, the request digest recomputed over
// author, events and the complete blob inventory). Any failure refuses the
// admission exactly as a per-check read did.
//
// It is read at most once per admission proposal, lazily: the first proof or
// UNKNOWN-outcome seal that needs it reads it, and every later check in the
// same proposal reuses that read, or its error. An admission with neither
// reads nothing. gateProofs creates it and it dies with that call, so no
// verification is ever reused across commands or across proposals.
//
// Observation boundary: the inventory is the inbox as listed and read when the
// first check asked, which is after the proposal replayed and its artifacts
// were materialized, and inside the admission lock on a real admission (a dry
// run holds no lock). The lock serializes admissions; it does not stop
// capture. A packet published to intake after that read is seen by no check in
// this admission, where before a later check could have seen it. That is the
// boundary every check already had: a packet published after the last check
// and before publication was never seen. Pending intake is an admission-time
// "not yet", never a validity rule, so where the boundary falls cannot change
// what the ledger means.
type pendingIntake struct {
	project store.Project
	read    bool
	packets []model.Packet
	err     error
}

// scanIntake is the one full verified intake read behind a pendingIntake.
// Tests wrap it to count reads.
var scanIntake = func(project store.Project) ([]model.Packet, error) {
	return store.ReadIntake(project, nil)
}

func newPendingIntake(project store.Project) *pendingIntake {
	return &pendingIntake{project: project}
}

func (p *pendingIntake) all() ([]model.Packet, error) {
	if !p.read {
		p.read = true
		p.packets, p.err = scanIntake(p.project)
	}
	return p.packets, p.err
}

func invocationEnvelope(event model.TypedEvent) *model.InvocationEnvelope {
	switch t := event.(type) {
	case *model.InvocationStart:
		return &t.Envelope
	case *model.InvocationSeal:
		return &t.Envelope
	}
	return nil
}
