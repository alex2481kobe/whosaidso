package query

// The disposal-loss read: for one artifact identity, the support_loss targets
// an artifact.dispose must record and the admitted events whose evidence it
// leaves unverifiable. The list comes from the same reducer computation the
// admission gate checks against, at this answer's watermark. Nothing here
// writes, captures, or fills in a disposal; the gate stays the authority.
// Other presets and per-record views live in presets.go and views.go.

import (
	"fmt"

	"datum/internal/model"
	"datum/internal/reduce"
)

// DisposalTarget is the artifact identity a disposal would name: its digest,
// and its git pin when the disposal's artifact is git-pinned.
type DisposalTarget struct {
	Digest model.Digest  `json:"digest"`
	Git    *model.GitPin `json:"git,omitempty"`
}

// DisposalLoss holds only for a disposal naming exactly Target. SupportLoss is
// every record revision the gate will require in support_loss, each with an
// authored reason; a later admission may add more, and the gate then refuses.
type DisposalLoss struct {
	Target      DisposalTarget            `json:"target"`
	SupportLoss []model.RecordRef         `json:"support_loss"`
	CitedBy     []reduce.ArtifactCitation `json:"cited_by"`
}

func checkDisposalRequest(r Request) error {
	if (r.Command == "disposal-loss") != (r.Disposal != nil) {
		return fmt.Errorf("an artifact identity belongs to disposal-loss, which requires one")
	}
	if r.Disposal == nil {
		return nil
	}
	if !model.ValidDigest(r.Disposal.Digest) {
		return fmt.Errorf("disposal-loss: digest must be 64 lowercase sha-256 hex characters")
	}
	if r.Disposal.Git != nil {
		pin := model.ArtifactRef{Kind: "git", Git: r.Disposal.Git, Selector: model.Selector{Kind: "whole"}}
		if err := model.ValidateArtifactRef(pin, "target"); err != nil {
			return fmt.Errorf("disposal-loss: %v", err)
		}
	}
	return nil
}

// disposalLoss asks the reducer the gate's own question. A content-pinned
// citation matches by digest; a git-pinned one only by the same git pin.
func disposalLoss(s reduce.Snapshot, target DisposalTarget) *DisposalLoss {
	e := model.ArtifactDispose{Digest: target.Digest}
	if target.Git != nil {
		e.Artifact = model.ArtifactRef{Kind: "git", Git: target.Git, Selector: model.Selector{Kind: "whole"}}
	}
	return &DisposalLoss{Target: target, SupportLoss: nonNil(s.DisposalLoss(e)), CitedBy: nonNil(s.DisposalCitations(e))}
}
