package write

// The dry run of admission lives here: CheckAdmission sends a packet set
// through the gate Admit uses (admissionProposal), against the published ledger
// read without the admission lock. It publishes no bundle, preserves no blob
// and writes no intake. It adds no rule: the collector below only decides
// whether a refusal ends the check or is recorded while the later, independent
// stages still run. The admission transaction and every rule live elsewhere.

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"datum/internal/evidence"
	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

// CheckRefusal is one reason the gate would refuse, and the stage that found it.
type CheckRefusal struct {
	Stage string
	Err   error
}

// ProofMemberCheck is one listed proof member as the gate's own functions see
// it: its family class, its own criterion verdict (from the evidence evaluator
// admission calls) and whether its instrument's validation holds at proof time.
type ProofMemberCheck struct {
	Invocation  model.InvocationRef
	Disposition string
	Class       reduce.MemberClass
	Verdict     evidence.Verdict // empty when the member is not an exact, sealed run
	Reason      string
	Validation  string // empty when the instrument's validation holds
}

// AdmissionCheck is what a dry run found. StoppedAt names the stage that ended
// it early; stages after it were not checked. Refusals found after an earlier
// refusal were reached only because the dry run kept going; admission stops at
// the first.
type AdmissionCheck struct {
	Head        uint64
	Refusals    []CheckRefusal
	StoppedAt   string
	Unpreserved []model.Digest
	Members     []ProofMemberCheck
}

var errCheckStopped = errors.New("dry run stopped")

// dryRun is the collector admissionProposal is handed on a dry run. Every
// method is a no-op pass-through on a nil receiver, which is a real admission.
type dryRun struct {
	check *AdmissionCheck
	after *reduce.Snapshot
}

func (d *dryRun) stop(stage string, err error) error {
	if d == nil || err == nil {
		return err
	}
	d.note(stage, err)
	d.check.StoppedAt = stage
	return errCheckStopped
}

func (d *dryRun) note(stage string, err error) error {
	if d == nil || err == nil {
		return err
	}
	for _, seen := range d.check.Refusals {
		if seen.Err.Error() == err.Error() {
			return nil
		}
	}
	d.check.Refusals = append(d.check.Refusals, CheckRefusal{Stage: stage, Err: err})
	return nil
}

func (d *dryRun) replayed(after reduce.Snapshot) {
	if d != nil {
		d.after = &after
	}
}

// preserver is preserveAdmissionBlob on admission. A dry run only records what
// admission would copy into the artifact store.
func (d *dryRun) preserver() func(string, string, []byte) error {
	if d == nil {
		return preserveAdmissionBlob
	}
	return func(_, _ string, data []byte) error {
		d.check.Unpreserved = append(d.check.Unpreserved, model.HashBytes(data))
		return nil
	}
}

// proofMembers asks each listed member the gate's own questions separately, so
// one member's refusal does not hide the next: gateProofFamily over a proof
// naming only that member (its member-level refusals), and the member's own
// verdict and instrument validation from the same functions gateProofFamily calls.
func (d *dryRun) proofMembers(ctx context.Context, project store.Project, after reduce.Snapshot, intake *pendingIntake, e *model.ProofAdmit) {
	if d == nil {
		return
	}
	resolver := evidence.NewResolverAt(project.Root, project.ArtifactDir())
	criterion, known := after.Criterion(e.CriterionRef)
	for i, member := range e.Evidence {
		alone := *e
		alone.Evidence = []model.ObservationDisposition{member}
		if err := gateProofFamily(ctx, project, after, intake, &alone); err != nil {
			var fault *model.Fault
			if errors.As(err, &fault) && strings.HasPrefix(fault.Path, "evidence[0]") {
				moved := *fault
				moved.Path = fmt.Sprintf("evidence[%d]", i) + strings.TrimPrefix(fault.Path, "evidence[0]")
				d.note("proofs", &moved)
			}
		}
		inv, class := after.ProofMember(e.CriterionRef, member.InvocationRef)
		row := ProofMemberCheck{Invocation: member.InvocationRef, Disposition: member.Disposition, Class: class}
		if class == reduce.MemberExact && inv.Seal != nil && known {
			own, err := evaluateOwn(ctx, resolver, criterion.Fix, *inv.Seal)
			row.Verdict, row.Reason = own.Verdict, own.Reason
			if err != nil {
				row.Reason = err.Error()
			}
			if err := gateInstrumentValidation(ctx, resolver, after, inv.Start.InstrumentRef, fmt.Sprintf("evidence[%d]", i)); err != nil {
				row.Validation = err.Error()
			}
		}
		d.check.Members = append(d.check.Members, row)
	}
}

// evaluateOwn is the member evaluation gateProofFamily performs: Observe the
// run's own output, then Evaluate the frozen criterion over it alone.
func evaluateOwn(ctx context.Context, resolver *evidence.Resolver, fix model.CriterionFix, seal model.InvocationEnvelope) (evidence.Evaluation, error) {
	observation, err := resolver.Observe(ctx, fix, seal)
	if err != nil {
		return evidence.Evaluation{Verdict: evidence.Unknown}, err
	}
	return evidence.Evaluate(fix, []evidence.Observation{observation})
}

// UncapturedPacket wraps events the author has not captured yet as the packet
// capture would publish, without writing intake. It carries no blobs.
func UncapturedPacket(project store.Project, author model.Actor, events []model.Event) (model.Packet, error) {
	at := time.Now().UTC()
	id, err := model.NewID(at, rand.Reader)
	if err != nil {
		return model.Packet{}, err
	}
	data, err := model.Encode(events)
	if err != nil {
		return model.Packet{}, err
	}
	return model.Packet{Version: model.WireVersion, Project: project.ID, CommandID: id, RequestDigest: model.HashBytes(data), Author: author, CapturedAt: at, Events: events}, nil
}

// CheckAdmission runs an accepted admission of the named intake packets plus
// the uncaptured ones through the gate and reports every refusal it collected.
// The error return is for a check that could not run at all.
func CheckAdmission(ctx context.Context, project store.Project, packetIDs []model.ID, uncaptured []model.Packet, admitter model.Actor) (AdmissionCheck, error) {
	loaded, err := store.Load(project)
	if err != nil {
		return AdmissionCheck{}, err
	}
	snapshot := loaded.Snapshot()
	check := AdmissionCheck{Head: snapshot.Watermark().Sequence}
	var packets []model.Packet
	var refs []model.PacketRef
	if len(packetIDs) > 0 {
		// ReadVerifiedIntake with no ids reads every packet; only named ones are wanted.
		verified, err := store.ReadVerifiedIntake(project, packetIDs)
		if err != nil {
			return AdmissionCheck{}, err
		}
		for _, v := range verified {
			packets, refs = append(packets, v.Packet), append(refs, v.Ref)
		}
	}
	for _, p := range uncaptured {
		data, err := model.Encode(p)
		if err != nil {
			return AdmissionCheck{}, err
		}
		packets, refs = append(packets, p), append(refs, model.PacketRef{CommandID: p.CommandID, Digest: model.HashBytes(data)})
	}
	if len(packets) == 0 {
		return AdmissionCheck{}, admissionFault("invalid-field", "packets", "a check needs events or packets")
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].CommandID < refs[j].CommandID })
	dry := &dryRun{check: &check}
	for _, ref := range refs {
		if prior, ok := snapshot.Review(reduce.ReviewKey{Project: project.ID, CommandID: ref.CommandID}); ok {
			dry.stop("gate", admissionFault("conflict", "packets", fmt.Sprintf("packet %s already has disposition %s", ref.CommandID, prior.Outcome)))
			return check, nil
		}
	}
	command, err := model.NewID(time.Now(), rand.Reader)
	if err != nil {
		return AdmissionCheck{}, err
	}
	request := AdmitRequest{CommandID: command, Admitter: admitter, Outcome: "accepted", Reason: "dry run: datum proof check"}
	digest, err := admissionDigest(project, request, refs)
	if err != nil {
		return AdmissionCheck{}, err
	}
	if _, err := admissionProposal(ctx, project, request, digest, snapshot, packets, refs, dry); err != nil && !errors.Is(err, errCheckStopped) {
		return check, err
	}
	return check, nil
}

// unwritten keeps a refusal from citing a bundle that was never written
// (DOGFOOD entry 13): the reducer stamps its faults with the sequence the
// proposal would have taken, and a refused proposal takes none.
func unwritten(err error, proposed uint64) error {
	var fault *model.Fault
	if errors.As(err, &fault) && fault.Sequence == proposed {
		named := *fault
		named.Sequence = 0
		named.Detail += " (in the proposed bundle; nothing was written)"
		return &named
	}
	var conflict *reduce.Conflict
	if errors.As(err, &conflict) && conflict.Sequence == proposed {
		named := *conflict
		named.Sequence = 0
		return unwrittenConflict{&named}
	}
	return err
}

type unwrittenConflict struct{ conflict *reduce.Conflict }

func (u unwrittenConflict) Unwrap() error { return u.conflict }
func (u unwrittenConflict) Error() string {
	c := u.conflict
	return fmt.Sprintf("%s at %s in event %d of the proposed bundle (nothing was written): %s expected revision %d, admitted revision is %d",
		c.Code(), c.Path, c.EventIndex, c.Target.RecordID, c.Expected, c.Actual)
}
