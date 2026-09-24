package model

// Invocation capture, source intake, proof, review, and evidence withdrawal payloads live here.
// Authored record revisions, task ownership, and stateful admission decisions do not.

import (
	"fmt"
	"time"
)

// InvocationRef addresses the immutable start/seal pair by its subordinate ID.
// Observation history and proof require a seal when it is used as an observation. No event UUID or
// future admission coordinate is needed while both packets are still in intake.
type InvocationRef struct {
	Project      ProjectID `json:"project"`
	InvocationID ID        `json:"invocation_id"`
}

// InvocationStart/Seal feed observation history and run capture. Seal carries
// the complete envelope and a start link; history and proof enforce immutable
// intent equality.
type InvocationStart struct {
	Envelope InvocationEnvelope `json:"envelope"`
}
type InvocationSeal struct {
	StartRef InvocationRef      `json:"start_ref"`
	Envelope InvocationEnvelope `json:"envelope"`
}

func (e InvocationStart) validate(p string) error {
	// Every field here is something only a completed run can know. The guard
	// originally covered three of six, so a pre-launch intent could assert the
	// conditions it ran under, an effective configuration the process never
	// reported, and a clean isolation, before anything had launched. Those are
	// exactly the facts a proof compares when deciding whether two runs are
	// comparable and whether isolation held.
	for _, f := range []struct {
		name  string
		state AvailabilityState
	}{
		{"outcome", e.Envelope.Outcome.State},
		{"observed_at", e.Envelope.ObservedAt.State},
		{"outputs", e.Envelope.Outputs.State},
		{"conditions_observed", e.Envelope.ConditionsObserved.State},
		{"config_effective", e.Envelope.ConfigEffective.State},
		{"isolation", e.Envelope.Isolation.State},
	} {
		if f.state != Unknown {
			return invalid(p+".envelope."+f.name,
				"pre-launch intent cannot claim what only a completed run observes")
		}
	}
	return nil
}

func (e InvocationSeal) validate(p string) error {
	if e.StartRef.InvocationID != e.Envelope.InvocationID || e.StartRef.Project != e.Envelope.ExecutionSourceIdentity.Project {
		return invalid(p+".start_ref", "seal must link this invocation's immutable start")
	}
	return nil
}

// SourceIntake feeds exact-byte capture and admission, and the views'
// attribution/ordering.
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

// CriterionFix feeds independent criterion revisions and their evaluation and proof.
// Author is the actual predicate author, not inferred from the eventual admitter.
type CriterionFix struct {
	Claim       RecordRef           `json:"claim"`
	CriterionID ID                  `json:"criterion_id"`
	Revision    Revision            `json:"revision"`
	Expression  CriterionExpression `json:"expression"`
	Policy      EvaluationPolicy    `json:"policy"`
	Author      Actor               `json:"author"`
	SourceRefs  []ArtifactRef       `json:"source_refs"`
}
type ObservationDisposition struct {
	InvocationRef InvocationRef `json:"invocation_ref"`
	Disposition   string        `json:"disposition"`
	Reason        string        `json:"reason" semantic:"text"`
	// CodeChange is the recorded fact that lets a failing run of the
	// proof's own criterion revision be set aside as inapplicable: code under
	// the claim's scope changed since the run's commit (code_change.go).
	CodeChange *CodeChange `json:"code_change,omitempty"`
}

func (d ObservationDisposition) validate(p string) error {
	if d.CodeChange != nil && d.Disposition != "inapplicable" {
		return invalid(p+".code_change", "a code change only sets a run aside as inapplicable")
	}
	return oneOf(d.Disposition, p+".disposition", "supports", "contradicts", "inapplicable", "inconclusive")
}

type ResponsibleJudgment struct {
	Actor  Actor  `json:"actor"`
	Reason string `json:"reason" semantic:"text"`
}

func (j ResponsibleJudgment) validate(p string) error {
	if Blank(j.Actor.ID) {
		return invalid(p+".actor", "proof judgment requires a named responsible actor")
	}
	return nil
}

// ProofAdmit supplies history and proof the family and responsible judgment, not a writable
// PROVEN. Family completeness, timing and unresolved contradiction are gate checks.
// Verdict says which way the judgment goes: supports or refutes. Every
// proof states it.
type ProofAdmit struct {
	Claim        RecordRef                `json:"claim"`
	CriterionRef CriterionRef             `json:"criterion_ref"`
	Evidence     []ObservationDisposition `json:"evidence"`
	Judgment     ResponsibleJudgment      `json:"judgment"`
	Verdict      string                   `json:"verdict" semantic:"text"`
}

// Proof verdicts. Status stays projected from them, never written.
const (
	VerdictSupports = "supports"
	VerdictRefutes  = "refutes"
)

// Refutes reports whether the proof's judgment is that the criterion failed.
func (e ProofAdmit) Refutes() bool { return e.Verdict == VerdictRefutes }

func (e ProofAdmit) validate(p string) error {
	if err := oneOf(e.Verdict, p+".verdict", "supports", "refutes"); err != nil {
		return err
	}
	if e.Claim != e.CriterionRef.Claim {
		return invalid(p+".criterion_ref", "criterion must name the same exact assertion")
	}
	if len(e.Evidence) == 0 {
		return invalid(p+".evidence", "proof needs an evidence family")
	}
	seen := map[InvocationRef]bool{}
	for i, o := range e.Evidence {
		if seen[o.InvocationRef] {
			return invalid(fmt.Sprintf("%s.evidence[%d]", p, i), "duplicate observation")
		}
		seen[o.InvocationRef] = true
	}
	return nil
}

// TrustWithdraw supplies history and proof the scope and the condition for restoring support.
type TrustWithdraw struct {
	Instrument            RecordRef `json:"instrument"`
	Scope                 Scope     `json:"scope"`
	RevalidationCondition string    `json:"revalidation_condition" semantic:"text"`
}

// ReviewAdmit feeds admission and the views' visible pending/rejected dispositions.
// Packet refs identify exact intake bytes; they are not record references.
type ReviewAdmit struct {
	Packets []PacketRef `json:"packets"`
	Outcome string      `json:"outcome"`
	Actor   Actor       `json:"actor"`
	Reason  string      `json:"reason" semantic:"text"`
	// Invocations records, on a rejected or correction-requested review,
	// each invocation.start/seal its packets carried: extracted facts, never the
	// packet. A refused run stays a ledger-visible criterion family member, so
	// proof validity never depends on which machine's intake holds its bytes.
	Invocations []ReviewedInvocation `json:"invocations,omitempty"`
	// Authors binds each reviewed packet's command ID to the
	// Actor the packet was captured with, so who wrote an admitted act is in the
	// ledger, not only in local intake. It covers every reviewed packet; an
	// author nobody recorded is an unknown Actor with its reason.
	Authors map[ID]Actor `json:"authors"`
	// CapturedAt binds each reviewed packet's command ID to the time WhoSaidSo's
	// intake stamped when it captured the packet; no author-facing input sets
	// it. "Criterion frozen before the run" is decided against it, so the check
	// replays from the ledger. It covers every reviewed packet; a time nobody
	// recorded is unknown with its reason.
	CapturedAt map[ID]Availability[time.Time] `json:"captured_at"`
	// EventPackets names the packet of each bundle event before this review,
	// in bundle order: empty when no event precedes it. Admission orders
	// packets by dependency, so the ledger cannot otherwise attribute an event
	// to a packet.
	EventPackets []ID `json:"event_packets"`
}

// ReviewedInvocation is one invocation event of a packet that was not accepted.
// EnvelopeDigest is the digest of the canonical envelope encoding, so a later
// admitted start or seal for the same invocation can be compared byte for byte.
type ReviewedInvocation struct {
	Packet         ID                         `json:"packet"`
	Event          string                     `json:"event"`
	InvocationID   ID                         `json:"invocation_id"`
	CriterionRef   Availability[CriterionRef] `json:"criterion_ref"`
	EnvelopeDigest Digest                     `json:"envelope_digest"`
}

// EnvelopeDigest is a ReviewedInvocation's identity: the digest of the
// canonical envelope encoding. Admission records it and replay compares it.
func EnvelopeDigest(env InvocationEnvelope) (Digest, error) {
	data, err := Encode(env)
	if err != nil {
		return "", err
	}
	return HashBytes(data), nil
}

// SelfAdmissionState records identity equality, not permission to admit. It
// is projected from a review's recorded author and admitter, never stored.
// Unknown means at least one actor was unknown.
type SelfAdmissionState string

const (
	SelfAdmissionTrue    SelfAdmissionState = "true"
	SelfAdmissionFalse   SelfAdmissionState = "false"
	SelfAdmissionUnknown SelfAdmissionState = "unknown"
)

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
	if len(e.Authors) != len(e.Packets) {
		return invalid(p+".authors", "must cover exactly the reviewed packets")
	}
	// Each author Actor is checked by the schema walk like every other Actor.
	for id := range e.Authors {
		if !seen[id] {
			return invalid(p+".authors", "names an unreviewed packet")
		}
	}
	if len(e.CapturedAt) != len(e.Packets) {
		return invalid(p+".captured_at", "must cover exactly the reviewed packets")
	}
	for id := range e.CapturedAt {
		if !seen[id] {
			return invalid(p+".captured_at", "names an unreviewed packet")
		}
	}
	for i, id := range e.EventPackets {
		if e.Outcome != "accepted" || !seen[id] {
			return invalid(fmt.Sprintf("%s.event_packets[%d]", p, i), "must name a packet of an accepted review")
		}
	}
	if e.Outcome == "accepted" && len(e.Invocations) != 0 {
		return invalid(p+".invocations", "an accepted review admits its invocations as events, not as review facts")
	}
	for i, inv := range e.Invocations {
		at := fmt.Sprintf("%s.invocations[%d]", p, i)
		if !seen[inv.Packet] {
			return invalid(at+".packet", "names an unreviewed packet")
		}
		if err := oneOf(inv.Event, at+".event", "invocation.start", "invocation.seal"); err != nil {
			return err
		}
	}
	return oneOf(e.Outcome, p+".outcome", "accepted", "correction-requested", "rejected")
}

// ArtifactDispose feeds proof's manual loss accounting and the current support
// that history and the views show.
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
