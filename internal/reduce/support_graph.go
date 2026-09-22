package reduce

// Support-loss seeds and propagation through the admitted reference graph live here.
// Read-time support premises and achievement projections do not.

import (
	"reflect"

	"datum/internal/model"
)

// supportNode is a tagged key, so equal-looking ids in different namespaces
// cannot alias while traversing the four existing reverse-reference maps.
type supportNode struct {
	kind       string
	record     RecordKey
	criterion  CriterionKey
	invocation InvocationKey
	blocker    BlockerKey
}

func recordNode(ref model.RecordRef) supportNode {
	return supportNode{kind: "record", record: recordKey(ref)}
}

func criterionNode(ref model.CriterionRef) supportNode {
	return supportNode{kind: "criterion", criterion: criterionKey(ref)}
}

func invocationNode(key InvocationKey) supportNode {
	return supportNode{kind: "invocation", invocation: key}
}

// eventOwners maps a reference-bearing fact back to what it establishes. Loss
// events establish no replacement support, so they cannot taint their own remedy.
func eventOwners(project model.ProjectID, e model.TypedEvent) []supportNode {
	created := func(id model.ID) []supportNode {
		return []supportNode{recordNode(model.RecordRef{Project: project, RecordID: id, Revision: 1})}
	}
	revised := func(r model.RecordRef) []supportNode { r.Revision++; return []supportNode{recordNode(r)} }
	switch e := e.(type) {
	case *model.TaskCreate:
		return created(e.ID)
	case *model.ClaimAssert:
		return created(e.ID)
	case *model.DecisionOpen:
		return created(e.ID)
	case *model.InstrumentDeclare:
		return created(e.ID)
	case *model.TaskAmend:
		return revised(e.Target)
	case *model.ClaimRevise:
		return revised(e.Target)
	case *model.DecisionRevise:
		return revised(e.Target)
	case *model.InstrumentRevise:
		return revised(e.Target)
	case *model.TaskStart:
		return []supportNode{recordNode(e.Task)}
	case *model.TaskTakeover:
		return []supportNode{recordNode(e.Task)}
	case *model.AttemptTerminal:
		return []supportNode{recordNode(e.Task)}
	case *model.TaskClose:
		return []supportNode{recordNode(e.Task)}
	case *model.BlockerHold:
		return []supportNode{{kind: "blocker", blocker: BlockerKey{Project: e.Task.Project, Task: e.Task.RecordID, Blocker: e.BlockerID}}, recordNode(e.Task)}
	case *model.BlockerClear:
		return []supportNode{recordNode(e.Task)}
	case *model.CriterionFix:
		return []supportNode{criterionNode(model.CriterionRef{Claim: e.Claim, CriterionID: e.CriterionID, Revision: e.Revision})}
	case *model.InvocationStart:
		return []supportNode{invocationNode(InvocationKey{Project: project, InvocationID: e.Envelope.InvocationID})}
	case *model.InvocationSeal:
		return []supportNode{invocationNode(invocationKey(e.StartRef))}
	case *model.ProofAdmit:
		return []supportNode{recordNode(e.Claim)}
	case *model.DecisionDispose:
		return []supportNode{recordNode(e.Decision)}
	}
	return nil
}

func (s *state) dependents(node supportNode) []supportNode {
	var refs []Referrer
	switch node.kind {
	case "record":
		refs = s.reverseRecord[node.record]
	case "criterion":
		refs = s.reverseCriterion[node.criterion]
	case "invocation":
		refs = s.reverseInvocation[node.invocation]
	case "blocker":
		refs = s.reverseBlocker[node.blocker]
	}
	out := []supportNode{}
	for _, ref := range refs {
		// A replacement is new authored content. Its expected prior revision is a
		// concurrency check, not an inherited support premise.
		if ref.Path == "target" {
			continue
		}
		out = append(out, eventOwners(s.project, s.events[ref.Origin])...)
	}
	return out
}

func (s *state) reaches(seeds []supportNode, target supportNode) bool {
	seen := map[supportNode]bool{}
	queue := append([]supportNode(nil), seeds...)
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		if seen[node] {
			continue
		}
		if node == target {
			return true
		}
		seen[node] = true
		queue = append(queue, s.dependents(node)...)
	}
	return false
}

func sameArtifact(a, b model.ArtifactRef) bool {
	if a.Content != nil && b.Content != nil {
		return a.Content.SHA256 == b.Content.SHA256
	}
	return a.Git != nil && b.Git != nil && *a.Git == *b.Git
}

// artifactRefs walks typed payloads, not arbitrary JSON. Stopping at ArtifactRef
// includes every availability and visual branch without mistaking locators or
// selectors for identity. Scalar maps cannot contain artifact references.
func artifactRefs(value reflect.Value, out *[]model.ArtifactRef) {
	if value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if !value.IsNil() {
			artifactRefs(value.Elem(), out)
		}
		return
	}
	if value.Type() == reflect.TypeOf(model.ArtifactRef{}) {
		*out = append(*out, value.Interface().(model.ArtifactRef))
		return
	}
	switch value.Kind() {
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if value.Type().Field(i).IsExported() {
				artifactRefs(value.Field(i), out)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			artifactRefs(value.Index(i), out)
		}
	}
}

func (s *state) disposalSeeds(e *model.ArtifactDispose) []supportNode {
	seeds := []supportNode{}
	for _, loss := range e.SupportLoss {
		seeds = append(seeds, recordNode(loss.Target))
	}
	for _, o := range s.eventOrder() {
		event := s.events[o]
		owners := eventOwners(s.project, event)
		if len(owners) == 0 {
			continue
		}
		artifacts := []model.ArtifactRef{}
		artifactRefs(reflect.ValueOf(event), &artifacts)
		for _, artifact := range artifacts {
			if sameArtifact(artifact, e.Artifact) || (artifact.Content != nil && artifact.Content.SHA256 == e.Digest) {
				seeds = append(seeds, owners...)
				break
			}
		}
	}
	return seeds
}

// supportLosses traverses the complete admitted graph at query time. References
// introduced after a withdrawal still inherit its loss. Each cause appears once,
// even when multiple paths or a proof/criterion cycle reach the same target.
func (s *state) supportLosses(target supportNode) []SupportLossFact {
	out := []SupportLossFact{}
	for _, o := range s.eventOrder() {
		event := s.events[o]
		var seeds []supportNode
		switch e := event.(type) {
		case *model.Correction:
			switch e.Target.Kind {
			case "record":
				seeds = append(seeds, recordNode(*e.Target.Record))
			case "criterion":
				seeds = append(seeds, criterionNode(*e.Target.Criterion))
			case "support":
				seeds = append(seeds, recordNode(e.Target.Support.Dependent))
			}
			for _, ref := range e.AffectedRevisions {
				seeds = append(seeds, recordNode(ref))
			}
		case *model.TrustWithdraw:
			// Free-text scopes cannot establish disjointness. Withdrawal remains a
			// conservative stop for this exact version until explicitly revalidated.
			seeds = append(seeds, recordNode(e.Instrument))
		case *model.Supersede:
			seeds = append(seeds, recordNode(e.Prior))
		case *model.ArtifactDispose:
			seeds = s.disposalSeeds(e)
		default:
			continue
		}
		if s.reaches(seeds, target) {
			out = append(out, SupportLossFact{Origin: o, Type: event.EventType()})
		}
	}
	return out
}
