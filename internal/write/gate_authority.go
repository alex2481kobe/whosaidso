package write

// This file holds the authority side of the admission gate: checking that each
// prerequisite's authority artifact is present among the admission sources, and
// deriving the artifact refs an admission must carry. Packet ordering, record
// providers and reference resolution stay in gate.go.

import (
	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
)

func gateAuthorities(event model.TypedEvent, sources []reduce.Source) error {
	for _, prerequisite := range gatePrerequisites(event) {
		if prerequisite.Authority == nil {
			continue
		}
		authority := *prerequisite.Authority
		if model.Blank(authority.Actor.ID) {
			return admissionFault("authority-unavailable", "authority.actor", "unknown attribution cannot grant a waiver")
		}
		if !containsAdmissionRef(authority.Scope.ContextRefs, prerequisite.Target) {
			return admissionFault("authority-scope", "authority.scope.context_refs", "waiver scope must name the exact prerequisite revision")
		}
		found := false
		for _, source := range sources {
			if source.Intake.Speaker == authority.Actor && sameAdmissionArtifact(source.Intake.SourceRef, authority.SourceRef) && containsAdmissionRef(source.Intake.Referents, prerequisite.Target) {
				found = true
				break
			}
		}
		if !found {
			return admissionFault("authority-unavailable", "authority.source_ref", "no admitted or bundled carrier matches the source, speaker and exact target")
		}
	}
	return nil
}

func containsAdmissionRef(refs []model.RecordRef, target model.RecordRef) bool {
	for _, ref := range refs {
		if ref == target {
			return true
		}
	}
	return false
}

func sameAdmissionArtifact(a, b model.ArtifactRef) bool {
	// Identity comes from every supplied pin; locators are retrieval hints.
	if a.Kind != b.Kind || (a.Git == nil) != (b.Git == nil) || (a.Content == nil) != (b.Content == nil) {
		return false
	}
	if a.Git != nil && *a.Git != *b.Git {
		return false
	}
	return a.Content == nil || a.Content.SHA256 == b.Content.SHA256 && a.Content.Length == b.Content.Length && a.Content.MediaType == b.Content.MediaType
}

func gatePrerequisites(event model.TypedEvent) []model.Prerequisite {
	switch e := event.(type) {
	case *model.TaskCreate:
		return e.Spec.Prerequisites
	case *model.TaskAmend:
		return e.Replacement.Prerequisites
	}
	return nil
}

func admissionArtifacts(event model.TypedEvent) []model.ArtifactRef {
	refs := []model.ArtifactRef{}
	var spec *model.TaskSpec
	switch e := event.(type) {
	case *model.SourceIntake:
		refs = append(refs, e.SourceRef)
	case *model.TaskCreate:
		refs = append(refs, e.Provenance.SourceRefs...)
		spec = &e.Spec
	case *model.TaskAmend:
		refs = append(refs, e.Provenance.SourceRefs...)
		spec = &e.Replacement
	case *model.ClaimAssert:
		refs = append(refs, e.Provenance.SourceRefs...)
		for _, external := range e.Spec.ExternalRefs {
			if external.SourceRef != nil {
				refs = append(refs, *external.SourceRef)
			}
		}
	case *model.BlockerClear:
		refs = append(refs, e.ResolvingWitness)
	case *model.InstrumentDeclare:
		refs = append(refs, e.Provenance.SourceRefs...)
		refs = append(refs, e.Spec.ImplementationRef)
		if e.Spec.Validation.Value != nil {
			refs = append(refs, e.Spec.Validation.Value.Ref)
		}
	case *model.TaskTakeover:
		refs = append(refs, e.StoppedConfirmationRef)
	case *model.AttemptTerminal:
		refs = append(refs, e.DeliveryRefs...)
	default:
		refs = append(refs, gateProofArtifacts(event)...)
	}
	if spec != nil {
		if spec.Progress != nil {
			refs = append(refs, spec.Progress.WitnessRefs...)
		}
		for _, prerequisite := range spec.Prerequisites {
			if prerequisite.Authority != nil {
				ref := prerequisite.Authority.SourceRef
				ref.Selector = prerequisite.Authority.Selector
				refs = append(refs, ref)
			}
		}
	}
	return refs
}
