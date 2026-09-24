package write

// Admission rules for the two owner acts that are not decisions: supersede and
// artifact.dispose (R10.1 revised: no owner key, no author check; the act
// carries a named authority whose carrier must resolve, and the packet author
// stays whoever wrote it). Also here: once an artifact is disposed, no new
// admission may treat it as available. Replay-order rules (superseded twice,
// cycles, a ruling needing authority) live in internal/reduce/supersede.go.
// WhoSaidSo never deletes bytes here: disposal is recorded, not performed.

import (
	"fmt"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
)

// gateOwnerActOperation: an authority, when present, names who ruled. Whether a
// supersession needs one depends on replayed state and is decided by the reducer.
func gateOwnerActOperation(event model.TypedEvent) error {
	var authority *model.Authority
	switch e := event.(type) {
	case *model.Supersede:
		authority = e.Authority
		if authority == nil {
			return nil
		}
	case *model.ArtifactDispose:
		authority = &e.Authority
	}
	if authority == nil || model.Blank(authority.Actor.ID) {
		return admissionFault("authority-unavailable", "authority.actor", "an owner act must name the authority that ruled")
	}
	return nil
}

// gateOwnerActArtifacts resolves only the authority's carrier and its selected
// ruling. A disposed artifact is never resolved or preserved: disposal is not
// preserved evidence, and its bytes may already be gone.
func gateOwnerActArtifacts(event model.TypedEvent) []model.ArtifactRef {
	var authority *model.Authority
	switch e := event.(type) {
	case *model.Supersede:
		authority = e.Authority
	case *model.ArtifactDispose:
		authority = &e.Authority
	}
	if authority == nil {
		return nil
	}
	selected := authority.SourceRef
	selected.Selector = authority.Selector
	return []model.ArtifactRef{authority.SourceRef, selected}
}

// gateSupersedeCanonical: supersede acts on an already-canonical record, one
// admitted before this admission set. A record proposed alongside it, or one
// whose packet was rejected, is not canonical.
func gateSupersedeCanonical(before reduce.Snapshot, event model.TypedEvent) error {
	e, ok := event.(*model.Supersede)
	if !ok {
		return nil
	}
	if _, exists := before.Record(e.Prior); !exists {
		return admissionFault("supersede-not-canonical", "prior", fmt.Sprintf("%s revision %d is not an admitted record revision", e.Prior.RecordID, e.Prior.Revision))
	}
	return nil
}

// gateDisposals runs after replay and before any bytes are resolved or kept.
// No admitted event may cite a disposed artifact, and a disposal must name
// every record revision whose support it removes.
func gateDisposals(after reduce.Snapshot, packets []model.Packet) error {
	for _, packet := range packets {
		for _, raw := range packet.Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				return err
			}
			for _, ref := range admissionArtifacts(event) {
				if disposed(after, ref) {
					return admissionFault("artifact-disposed", "artifact", "cites an artifact whose disposal is recorded; it is unverifiable, not available")
				}
			}
			e, ok := event.(*model.ArtifactDispose)
			if !ok {
				continue
			}
			named := map[model.RecordRef]bool{}
			for _, loss := range e.SupportLoss {
				named[loss.Target] = true
			}
			for _, lost := range after.DisposalLoss(*e) {
				if !named[lost] {
					return admissionFault("loss-unaccounted", "support_loss", fmt.Sprintf("%s revision %d loses support and is not recorded as unverifiable", lost.RecordID, lost.Revision))
				}
			}
		}
	}
	return nil
}

// disposed matches the reducer's disposal identity: the same content digest or
// the same git object.
func disposed(s reduce.Snapshot, ref model.ArtifactRef) bool {
	for _, d := range s.ArtifactDisposals() {
		if ref.Content != nil && ref.Content.SHA256 == d.Disposal.Digest {
			return true
		}
		if ref.Git != nil && d.Disposal.Artifact.Git != nil && *ref.Git == *d.Disposal.Artifact.Git {
			return true
		}
	}
	return false
}
