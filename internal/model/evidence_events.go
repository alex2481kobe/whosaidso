package model

// Invocation capture, source intake, proof, review, and evidence withdrawal payloads live here.
// Authored record revisions, task ownership, and stateful admission decisions do not.

import (
	"fmt"
)

// InvocationRef addresses the immutable start/seal pair by its subordinate ID.
// U06/U12 require a seal when it is used as an observation. No event UUID or
// future admission coordinate is needed while both packets are still in intake.
type InvocationRef struct {
	Project      ProjectID `json:"project"`
	InvocationID ID        `json:"invocation_id"`
}

// InvocationStart/Seal feed U06 observation history and U10 capture. Seal carries
// the complete envelope and a start link; U06/U12 enforce immutable intent equality.
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
	// comparable and whether isolation held. Found by lane E.
	for _, f := range []struct {
		name  string
		state AvailabilityState
	}{
		{"outcome", e.Envelope.Outcome.State},
		{"observed_at", e.Envelope.ObservedAt.State},
		{"output_refs", e.Envelope.OutputRefs.State},
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
	SourceRefs  []ArtifactRef       `json:"source_refs"`
}
type ObservationDisposition struct {
	InvocationRef InvocationRef `json:"invocation_ref"`
	Disposition   string        `json:"disposition"`
	Reason        string        `json:"reason" semantic:"text"`
}

func (d ObservationDisposition) validate(p string) error {
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
	seen := map[InvocationRef]bool{}
	for i, o := range e.Evidence {
		if seen[o.InvocationRef] {
			return invalid(fmt.Sprintf("%s.evidence[%d]", p, i), "duplicate observation")
		}
		seen[o.InvocationRef] = true
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
	// SelfAdmission binds each packet command ID to the author/admitter comparison.
	// Omission is reserved for legacy reviews and projects as unknown, never false.
	// When present, every reviewed packet must have exactly one explicit state.
	SelfAdmission map[ID]SelfAdmissionState `json:"self_admission,omitempty"`
}

// SelfAdmissionState records identity equality, not permission to admit.
// Unknown means at least one actor was unknown, or a legacy review omitted it.
type SelfAdmissionState string

const (
	SelfAdmissionTrue    SelfAdmissionState = "true"
	SelfAdmissionFalse   SelfAdmissionState = "false"
	SelfAdmissionUnknown SelfAdmissionState = "unknown"
)

func (s SelfAdmissionState) validate(p string) error {
	return oneOf(string(s), p, "true", "false", "unknown")
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
	if e.SelfAdmission != nil {
		if len(e.SelfAdmission) != len(e.Packets) {
			return invalid(p+".self_admission", "must cover exactly the reviewed packets")
		}
		for id, state := range e.SelfAdmission {
			if !seen[id] {
				return invalid(p+".self_admission", "names an unreviewed packet")
			}
			if Blank(e.Actor.ID) && state != SelfAdmissionUnknown {
				return invalid(p+".self_admission", "an unknown admitter requires an unknown comparison")
			}
		}
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
