package write

import (
	"fmt"
	"sort"
	"time"

	"datum/internal/model"
	"datum/internal/reduce"
)

// gateKey follows typed references and attempt identities; bundled authority
// artifacts resolve separately.
type gateKey struct {
	Record  model.RecordRef
	Blocker model.ID
	Attempt model.ID
}

func gatePackets(project model.ProjectID, snapshot reduce.Snapshot, packets []model.Packet) ([]model.Packet, error) {
	packets = append([]model.Packet(nil), packets...)
	sort.Slice(packets, func(i, j int) bool { return packets[i].CommandID < packets[j].CommandID })
	events := make([][]model.TypedEvent, len(packets))
	providers := map[gateKey]int{}
	sources := snapshot.Sources()
	for i, packet := range packets {
		if packet.Project != project {
			return nil, admissionFault("project-mismatch", "packet.project", "packet belongs to another project")
		}
		for _, raw := range packet.Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				return nil, err
			}
			if err := gateOperation(event, packet.Author); err != nil {
				return nil, err
			}
			events[i] = append(events[i], event)
			if source, ok := event.(*model.SourceIntake); ok {
				sources = append(sources, reduce.Source{Key: reduce.SourceKey{Project: project, Source: source.SourceID}, Intake: *source})
			}
			if key, ok := gateProvides(project, event); ok {
				if _, duplicate := providers[key]; duplicate {
					return nil, admissionFault("conflict", "packets", "two proposals establish the same revision, blocker or attempt")
				}
				providers[key] = i
			}
		}
	}
	admitted := map[model.ID]bool{}
	for _, task := range snapshot.Tasks() {
		for _, attempt := range task.Attempts {
			admitted[attempt.Key.Attempt] = true
		}
	}
	dependencies := make([]map[int]bool, len(packets))
	for i, typed := range events {
		dependencies[i] = map[int]bool{}
		for _, event := range typed {
			if err := gateAuthorities(event, sources); err != nil {
				return nil, err
			}
			refs, err := model.SameProjectReferences(event, project)
			if err != nil {
				return nil, err
			}
			for _, ref := range refs {
				key, exists := gateReference(snapshot, ref)
				if exists {
					continue
				}
				provider, ok := providers[key]
				if !ok {
					return nil, admissionFault("unknown-reference", ref.Path, "referent is neither admitted nor proposed in this packet set")
				}
				if provider != i {
					dependencies[i][provider] = true
				}
			}
			// A receipt or takeover depends on the packet creating its attempt.
			path, attempt := gateAttemptNeed(event)
			if attempt == "" || admitted[attempt] {
				continue
			}
			provider, ok := providers[gateKey{Attempt: attempt}]
			if !ok {
				return nil, admissionFault("unknown-reference", path, fmt.Sprintf("attempt %s is neither admitted nor proposed in this packet set", attempt))
			}
			if provider != i {
				dependencies[i][provider] = true
			}
		}
	}
	ordered := make([]model.Packet, 0, len(packets))
	used := make([]bool, len(packets))
	for len(ordered) < len(packets) {
		chosen := -1
		for i := range packets {
			if used[i] {
				continue
			}
			ready := true
			for dependency := range dependencies[i] {
				if !used[dependency] {
					ready = false
				}
			}
			if ready {
				chosen = i
				break
			}
		}
		if chosen < 0 {
			return nil, admissionFault("dependency-cycle", "packets", "dependency cycle cannot be replayed without changing authored packet order")
		}
		used[chosen] = true
		ordered = append(ordered, packets[chosen])
	}
	return gateHandbacks(snapshot, ordered)
}

func gateOperation(event model.TypedEvent, author model.Actor) error {
	var provenance *model.Provenance
	switch e := event.(type) {
	case *model.SourceIntake:
		// A source speaker can differ from the person who captured the words.
	case *model.TaskCreate:
		provenance = &e.Provenance
	case *model.TaskAmend:
		provenance = &e.Provenance
	case *model.ClaimAssert:
		// Claims start UNMEASURED; observation/proof operations stay disabled.
		provenance = &e.Provenance
	case *model.InstrumentDeclare:
		// Known validation currently grants active trust during reduction.
		// A declaration cannot grant itself that authority.
		if e.Spec.Validation.State != model.Unknown {
			return admissionFault("unavailable-until-integrated", "spec.validation", "declaration cannot establish trusted validation")
		}
		provenance = &e.Provenance
	case *model.TaskStart, *model.TaskTakeover, *model.AttemptTerminal, *model.BlockerHold, *model.BlockerClear:
	default:
		// Packet authors cannot mint reviews, closures or DECISION authority.
		return admissionFault("unavailable-until-integrated", "event.type", string(event.EventType())+" is not enabled by the first admission gate")
	}
	if provenance != nil && provenance.Author != author {
		return admissionFault("attribution-mismatch", "provenance.author", "record author must match the immutable packet author")
	}
	return nil
}

func gateProvides(project model.ProjectID, event model.TypedEvent) (gateKey, bool) {
	switch e := event.(type) {
	case *model.TaskCreate:
		return gateKey{Record: model.RecordRef{Project: project, RecordID: e.ID, Revision: 1}}, true
	case *model.ClaimAssert:
		return gateKey{Record: model.RecordRef{Project: project, RecordID: e.ID, Revision: 1}}, true
	case *model.InstrumentDeclare:
		return gateKey{Record: model.RecordRef{Project: project, RecordID: e.ID, Revision: 1}}, true
	case *model.TaskAmend:
		target := e.Target
		target.Revision++
		return gateKey{Record: target}, true
	case *model.BlockerHold:
		return gateKey{Record: e.Task, Blocker: e.BlockerID}, true
	case *model.TaskStart:
		return gateKey{Attempt: e.AttemptID}, true
	case *model.TaskTakeover:
		return gateKey{Attempt: e.AttemptID}, true
	}
	return gateKey{}, false
}

func gateAttemptNeed(event model.TypedEvent) (string, model.ID) {
	switch e := event.(type) {
	case *model.AttemptTerminal:
		return "attempt_id", e.AttemptID
	case *model.TaskTakeover:
		return "prior_attempt_id", e.PriorAttemptID
	}
	return "", ""
}

func gateReference(snapshot reduce.Snapshot, ref model.Reference) (gateKey, bool) {
	if ref.Record != nil {
		_, exists := snapshot.Record(*ref.Record)
		return gateKey{Record: *ref.Record}, exists
	}
	if ref.Blocker != nil {
		key := gateKey{Record: ref.Blocker.Task, Blocker: ref.Blocker.BlockerID}
		for _, blocker := range snapshot.Blockers(reduce.Ident{Project: key.Record.Project, ID: key.Record.RecordID}) {
			if blocker.Key.Blocker == key.Blocker && blocker.TaskRevision == key.Record.Revision {
				return key, true
			}
		}
		return key, false
	}
	return gateKey{}, false
}

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

func gateProposal(base reduce.Snapshot, project model.ProjectID, command model.ID, digest model.Digest, proposal model.Bundle) error {
	// Validation only: Transact assigns the real envelope to the proposal.
	validation := proposal
	validation.Version = model.WireVersion
	validation.Project = project
	validation.CommandID = command
	validation.RequestDigest = digest
	validation.Sequence = base.Watermark().Sequence + 1
	validation.Predecessor = base.Watermark().CommandID
	validation.RecordedAt = time.Unix(0, 0).UTC()
	for i, raw := range proposal.Events {
		event, err := model.DecodeEvent(raw)
		if err != nil {
			return err
		}
		start, ok := event.(*model.TaskStart)
		if !ok {
			continue
		}
		current := base
		if i > 0 {
			validation.Events = proposal.Events[:i]
			current, err = reduce.Apply(base, validation)
			if err != nil {
				return err
			}
		}
		who := reduce.Ident{Project: start.Task.Project, ID: start.Task.RecordID}
		if revision, exists := current.CurrentRevision(who); exists && revision != start.Task.Revision {
			return &reduce.Conflict{Target: start.Task, Expected: start.Task.Revision, Actual: revision, Sequence: validation.Sequence, EventIndex: i, Path: "task"}
		}
		if task, exists := current.Task(who); exists && task.Status != reduce.StatusReady {
			return admissionFault("invalid-transition", "task.start", fmt.Sprintf("task is %s and cannot start until it is READY", task.Status))
		}
		for _, task := range current.Tasks() {
			for _, attempt := range task.Attempts {
				if attempt.Key.Attempt == start.AttemptID {
					return admissionFault("conflict", "attempt_id", "attempt identity already belongs to a task")
				}
			}
		}
	}
	validation.Events = proposal.Events
	_, err := reduce.Apply(base, validation)
	return err
}
