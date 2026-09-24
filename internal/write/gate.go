package write

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
)

// gateKey follows typed references and attempt identities; bundled authority
// artifacts resolve separately.
type gateKey struct {
	Record     model.RecordRef
	Blocker    model.ID
	Attempt    model.ID
	Criterion  model.CriterionRef
	Invocation model.InvocationRef
	Sealed     model.InvocationRef
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
			if err := gateCloseAuthority(event, sources); err != nil {
				return nil, err
			}
			if err := gateSupersedeCanonical(snapshot, event); err != nil {
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
		// A proof is ordered after the seals it disposes of, when they are proposed.
		for _, event := range typed {
			for _, sealed := range gateSealNeeds(event) {
				if provider, ok := providers[gateKey{Sealed: sealed}]; ok && provider != i {
					dependencies[i][provider] = true
				}
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
	switch e := event.(type) {
	case *model.SourceIntake:
		// A source speaker can differ from the person who captured the words.
	case *model.TaskCreate, *model.TaskAmend, *model.ClaimAssert:
		// Claims start UNMEASURED. A record's author is its packet's author,
		// recorded by the review, never a second copy in the record.
	case *model.InstrumentDeclare:
		// Known validation is admitted when its pinned artifact resolves;
		// the admitter is the judgment and trust.withdraw revokes it.
		return gateValidation(e.Spec.Validation)
	case *model.TaskStart, *model.TaskTakeover, *model.AttemptTerminal, *model.BlockerHold, *model.BlockerClear:
	default:
		// Packet authors cannot mint reviews, closures or DECISION authority.
		return gateProofOperation(event, author)
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
		return gateHoldKey(model.BlockerRef{Task: e.Task, BlockerID: e.BlockerID}), true
	case *model.TaskStart:
		return gateKey{Attempt: e.AttemptID}, true
	case *model.TaskTakeover:
		return gateKey{Attempt: e.AttemptID}, true
	}
	return gateProofProvides(project, event)
}

func gateAttemptNeed(event model.TypedEvent) (string, model.ID) {
	switch e := event.(type) {
	case *model.AttemptTerminal:
		return "attempt_id", e.AttemptID
	case *model.TaskTakeover:
		return "prior_attempt_id", e.PriorAttemptID
	case *model.InvocationStart:
		return "envelope.attempt_id", e.Envelope.AttemptID
	}
	return "", ""
}

// gateHoldKey matches a proposed hold to a proposed clear the way the reducer's
// lookup does: by task and blocker id, whatever task revision either names.
func gateHoldKey(ref model.BlockerRef) gateKey {
	task := model.RecordRef{Project: ref.Task.Project, RecordID: ref.Task.RecordID}
	return gateKey{Record: task, Blocker: ref.BlockerID}
}

func gateReference(snapshot reduce.Snapshot, ref model.Reference) (gateKey, bool) {
	if ref.Record != nil {
		_, exists := snapshot.Record(*ref.Record)
		return gateKey{Record: *ref.Record}, exists
	}
	if ref.Blocker != nil {
		_, exists := snapshot.Hold(*ref.Blocker)
		return gateHoldKey(*ref.Blocker), exists
	}
	if ref.Criterion != nil {
		_, exists := snapshot.Criterion(*ref.Criterion)
		return gateKey{Criterion: *ref.Criterion}, exists
	}
	if ref.Invocation != nil {
		_, exists := snapshot.Invocation(reduce.InvocationKey{Project: ref.Invocation.Project, InvocationID: ref.Invocation.InvocationID})
		if !exists && strings.HasPrefix(ref.Path, "evidence[") {
			// A proof may name a run the ledger recorded as rejected;
			// the reducer's proof family checker decides its disposition.
			exists = snapshot.RejectedRecorded(*ref.Invocation)
		}
		return gateKey{Invocation: *ref.Invocation}, exists
	}
	return gateKey{}, false
}

// gateProposal replays the proposal onto the admitted prefix. Start rules
// (current revision, READY with BLOCKED winning, one owner per attempt id) are
// the reducer's, so admission and replay share one implementation.
func gateProposal(base reduce.Snapshot, project model.ProjectID, command model.ID, digest model.Digest, proposal model.Bundle, dry *dryRun) (reduce.Snapshot, error) {
	// Validation only: Transact assigns the real envelope to the proposal.
	validation := proposal
	validation.Version = model.WireVersion
	validation.Project = project
	validation.CommandID = command
	validation.RequestDigest = digest
	validation.Sequence = base.Watermark().Sequence + 1
	validation.Predecessor = base.Watermark().CommandID
	validation.RecordedAt = time.Unix(0, 0).UTC()
	if dry == nil {
		after, err := reduce.Apply(base, validation)
		return after, unwritten(err, validation.Sequence)
	}
	after, refusals, err := reduce.ApplyCollectingProofRefusals(base, validation)
	for _, refusal := range refusals {
		dry.note("replay", unwritten(refusal, validation.Sequence))
	}
	return after, unwritten(err, validation.Sequence)
}
