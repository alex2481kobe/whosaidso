package reduce

import (
	"reflect"
	"sort"

	"datum/internal/model"
)

// ClaimStatus records achievement at one assertion revision, not current support.
type ClaimStatus string

const (
	StatusUnmeasured ClaimStatus = "UNMEASURED"
	StatusMeasured   ClaimStatus = "MEASURED"
	StatusProven     ClaimStatus = "PROVEN"
)

// DecisionStatus keeps negative rulings distinguishable from unanswered questions.
type DecisionStatus string

const (
	StatusOpen    DecisionStatus = "OPEN"
	StatusDecided DecisionStatus = "DECIDED"
)

// These facts retain the complete admitted payload and its ledger location.
// Their payloads follow Record's read-only ownership convention.
type ProofAdmission struct {
	Admission model.ProofAdmit
	Origin    Origin
}
type DecisionDisposition struct {
	Disposition model.DecisionDispose
	Origin      Origin
}
type TrustWithdrawal struct {
	Withdrawal model.TrustWithdraw
	Origin     Origin
}
type Supersession struct {
	Supersede model.Supersede
	Origin    Origin
}
type AdmittedCorrection struct {
	Correction model.Correction
	Origin     Origin
}
type ArtifactDisposal struct {
	Disposal model.ArtifactDispose
	Origin   Origin
}

// SupportLossFact identifies the cause even when it reached this record indirectly.
type SupportLossFact struct {
	Origin Origin
	Type   model.EventType
}

// SupportContext contains read-time checks supplied by the caller. Evidence must
// cover the complete support being quoted. Replay cannot establish available bytes
// or decide whether a different real-world use falls inside authored scope.
type SupportContext struct {
	EvidenceAvailable Truth
	ScopeApplicable   Truth
}

// SupportFacts keeps independent reasons separate so loss of trust never rewrites
// achievement or pretends that the underlying bytes disappeared.
type SupportFacts struct {
	EvidenceAvailable Truth
	ActiveTrust       Truth
	ApplicableScope   Truth
	CorrectionFree    Truth
	Losses            []SupportLossFact
}

func truthAnd(values ...Truth) Truth {
	result := TruthTrue
	for _, value := range values {
		if value == TruthFalse {
			return TruthFalse
		}
		if value != TruthTrue {
			result = TruthUnknown
		}
	}
	return result
}

// Current requires every independent premise. UNKNOWN never grants support.
func (s SupportFacts) Current() Truth {
	return truthAnd(s.EvidenceAvailable, s.ActiveTrust, s.ApplicableScope, s.CorrectionFree)
}

type ClaimProjection struct {
	Claim        RecordKey
	Spec         *model.ClaimSpec
	Status       ClaimStatus
	Observations []Invocation
	Proofs       []ProofAdmission
	Support      SupportFacts
}

type DecisionProjection struct {
	Decision     RecordKey
	Spec         *model.DecisionSpec
	Status       DecisionStatus
	Dispositions []DecisionDisposition
	Support      SupportFacts
}

// InstrumentProjection deliberately has no Status. Versioned validation and
// withdrawals are facts, not a tenth meaning of status.
type InstrumentProjection struct {
	Instrument  RecordKey
	Spec        *model.InstrumentSpec
	Withdrawals []TrustWithdrawal
	Support     SupportFacts
}

func asRef(k RecordKey) model.RecordRef {
	return model.RecordRef{Project: k.Project, RecordID: k.ID, Revision: k.Revision}
}

func (s Snapshot) Claim(id Ident) (ClaimProjection, bool) {
	rec, ok := s.inner().currentRecord(id)
	if !ok {
		return ClaimProjection{}, false
	}
	return s.ClaimAt(asRef(rec.Key))
}

// ClaimAt prevents a later assertion from borrowing any earlier observation.
func (s Snapshot) ClaimAt(ref model.RecordRef) (ClaimProjection, bool) {
	p, ok := s.claimAt(ref)
	return deepCopy(p), ok
}

func (s Snapshot) claimAt(ref model.RecordRef) (ClaimProjection, bool) {
	rec, ok := s.inner().record(ref)
	if !ok || rec.Kind != model.Claim {
		return ClaimProjection{}, false
	}
	p := ClaimProjection{Claim: rec.Key, Spec: rec.Claim, Status: StatusUnmeasured}
	for _, inv := range s.inner().invocationsSorted() {
		instrument, local := s.inner().records[recordKey(inv.Start.InstrumentRef)]
		if completedObservation(inv, ref) && local && instrument.Kind == model.Instrument {
			p.Observations = append(p.Observations, inv)
			p.Status = StatusMeasured
		}
	}
	for _, o := range s.inner().eventOrder() {
		if e, ok := s.inner().events[o].(*model.ProofAdmit); ok && e.Claim == ref {
			p.Proofs = append(p.Proofs, ProofAdmission{Admission: *e, Origin: o})
			p.Status = StatusProven
		}
	}
	p.Support, _ = s.support(ref)
	return p, true
}

func (s Snapshot) Claims() []ClaimProjection {
	out := []ClaimProjection{}
	for _, r := range s.inner().recordsSorted() {
		if r.Kind == model.Claim && s.inner().current[Ident{Project: r.Key.Project, ID: r.Key.ID}] == r.Key.Revision {
			p, _ := s.claimAt(asRef(r.Key))
			out = append(out, p)
		}
	}
	return deepCopySlice(out)
}

func (s Snapshot) Decision(id Ident) (DecisionProjection, bool) {
	rec, ok := s.inner().currentRecord(id)
	if !ok {
		return DecisionProjection{}, false
	}
	return s.DecisionAt(asRef(rec.Key))
}

func (s Snapshot) DecisionAt(ref model.RecordRef) (DecisionProjection, bool) {
	p, ok := s.decisionAt(ref)
	return deepCopy(p), ok
}

func (s Snapshot) decisionAt(ref model.RecordRef) (DecisionProjection, bool) {
	rec, ok := s.inner().record(ref)
	if !ok || rec.Kind != model.Decision {
		return DecisionProjection{}, false
	}
	p := DecisionProjection{Decision: rec.Key, Spec: rec.Decision, Status: StatusOpen}
	for _, o := range s.inner().eventOrder() {
		if e, ok := s.inner().events[o].(*model.DecisionDispose); ok && e.Decision == ref {
			p.Dispositions = append(p.Dispositions, DecisionDisposition{Disposition: *e, Origin: o})
			p.Status = StatusDecided
		}
	}
	p.Support, _ = s.support(ref)
	return p, true
}

func (s Snapshot) Decisions() []DecisionProjection {
	out := []DecisionProjection{}
	for _, r := range s.inner().recordsSorted() {
		if r.Kind == model.Decision && s.inner().current[Ident{Project: r.Key.Project, ID: r.Key.ID}] == r.Key.Revision {
			p, _ := s.decisionAt(asRef(r.Key))
			out = append(out, p)
		}
	}
	return deepCopySlice(out)
}

func (s Snapshot) Instrument(id Ident) (InstrumentProjection, bool) {
	rec, ok := s.inner().currentRecord(id)
	if !ok {
		return InstrumentProjection{}, false
	}
	return s.InstrumentAt(asRef(rec.Key))
}

func (s Snapshot) InstrumentAt(ref model.RecordRef) (InstrumentProjection, bool) {
	p, ok := s.instrumentAt(ref)
	return deepCopy(p), ok
}

func (s Snapshot) instrumentAt(ref model.RecordRef) (InstrumentProjection, bool) {
	rec, ok := s.inner().record(ref)
	if !ok || rec.Kind != model.Instrument {
		return InstrumentProjection{}, false
	}
	p := InstrumentProjection{Instrument: rec.Key, Spec: rec.Instrument}
	for _, w := range s.trustWithdrawals() {
		if w.Withdrawal.Instrument == ref {
			p.Withdrawals = append(p.Withdrawals, w)
		}
	}
	p.Support, _ = s.support(ref)
	return p, true
}

func (s Snapshot) Instruments() []InstrumentProjection {
	out := []InstrumentProjection{}
	for _, r := range s.inner().recordsSorted() {
		if r.Kind == model.Instrument && s.inner().current[Ident{Project: r.Key.Project, ID: r.Key.ID}] == r.Key.Revision {
			p, _ := s.instrumentAt(asRef(r.Key))
			out = append(out, p)
		}
	}
	return deepCopySlice(out)
}

func (s *state) eventOrder() []Origin {
	out := make([]Origin, 0, len(s.events))
	for o := range s.events {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].before(out[j]) })
	return out
}

func (s Snapshot) Corrections() []AdmittedCorrection { return deepCopySlice(s.corrections()) }

func (s Snapshot) corrections() []AdmittedCorrection {
	out := []AdmittedCorrection{}
	for _, o := range s.inner().eventOrder() {
		if e, ok := s.inner().events[o].(*model.Correction); ok {
			out = append(out, AdmittedCorrection{Correction: *e, Origin: o})
		}
	}
	return out
}

func (s Snapshot) Supersessions() []Supersession { return deepCopySlice(s.supersessions()) }

func (s Snapshot) supersessions() []Supersession {
	out := []Supersession{}
	for _, o := range s.inner().eventOrder() {
		if e, ok := s.inner().events[o].(*model.Supersede); ok {
			out = append(out, Supersession{Supersede: *e, Origin: o})
		}
	}
	return out
}

func (s Snapshot) TrustWithdrawals() []TrustWithdrawal { return deepCopySlice(s.trustWithdrawals()) }

func (s Snapshot) trustWithdrawals() []TrustWithdrawal {
	out := []TrustWithdrawal{}
	for _, o := range s.inner().eventOrder() {
		if e, ok := s.inner().events[o].(*model.TrustWithdraw); ok {
			out = append(out, TrustWithdrawal{Withdrawal: *e, Origin: o})
		}
	}
	return out
}

func (s Snapshot) ArtifactDisposals() []ArtifactDisposal { return deepCopySlice(s.artifactDisposals()) }

func (s Snapshot) artifactDisposals() []ArtifactDisposal {
	out := []ArtifactDisposal{}
	for _, o := range s.inner().eventOrder() {
		if e, ok := s.inner().events[o].(*model.ArtifactDispose); ok {
			out = append(out, ArtifactDisposal{Disposal: *e, Origin: o})
		}
	}
	return out
}

// completedObservation uses the immutable start's exact local assertion link.
// A failed run with captured output is still an observation. An unknown terminal
// state, a launch failure or absent output cannot establish a reading.
func completedObservation(inv Invocation, claim model.RecordRef) bool {
	start := inv.Start
	if inv.Key.Project != claim.Project || start.ExecutionSourceIdentity.Project != claim.Project || start.InstrumentRef.Project != claim.Project ||
		start.CriterionRef.State != model.Known || start.CriterionRef.Value == nil || start.CriterionRef.Value.Claim != claim || inv.Seal == nil {
		return false
	}
	if !matchesInvocationIntent(inv) {
		return false
	}
	seal := inv.Seal
	return seal.ObservedAt.State == model.Known && seal.Outcome.State == model.Known && seal.Outcome.Value != nil &&
		seal.Outcome.Value.Kind != "spawn-failed" && seal.OutputRefs.State == model.Known && seal.OutputRefs.Value != nil && len(*seal.OutputRefs.Value) > 0
}

func (s *state) requireKind(b model.Bundle, idx int, ref model.RecordRef, kind model.Kind, path string) error {
	rec, ok := s.records[recordKey(ref)]
	if !ok {
		return faultAt(CodeUnknownReference, b.Sequence, idx, path, "no local admitted revision")
	}
	if rec.Kind != kind {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, path, "target is not a "+string(kind))
	}
	return nil
}

// proofAdmit checks applicability recoverable from admitted facts. Evaluation of
// artifact bytes, family closure and semantic judgment remain admission gate work.
func (s *state) proofAdmit(b model.Bundle, idx int, e *model.ProofAdmit) error {
	if err := s.requireKind(b, idx, e.Claim, model.Claim, "claim"); err != nil {
		return err
	}
	supported := false
	criterion := s.criteria[criterionKey(e.CriterionRef)]
	for _, member := range e.Evidence {
		inv, ok := s.invocations[invocationKey(member.InvocationRef)]
		if !ok || inv.Key.Project != b.Project || inv.Seal == nil || inv.Start.CriterionRef.Value == nil || *inv.Start.CriterionRef.Value != e.CriterionRef {
			return faultAt(CodeInvalidTransition, b.Sequence, idx, "evidence", "proof requires local sealed observations of the exact criterion")
		}
		if !criterion.Origin.before(inv.Started) {
			return faultAt(CodeInvalidTransition, b.Sequence, idx, "criterion_ref", "criterion was not fixed before the invocation start")
		}
		if member.Disposition == "contradicts" {
			return faultAt(CodeInvalidTransition, b.Sequence, idx, "evidence", "contradicting evidence is unresolved")
		}
		if member.Disposition != "supports" {
			continue
		}
		instrument, ok := s.records[recordKey(inv.Start.InstrumentRef)]
		if !completedObservation(inv, e.Claim) || !ok || instrument.Kind != model.Instrument || instrument.Instrument.Validation.State != model.Known {
			return faultAt(CodeInvalidTransition, b.Sequence, idx, "evidence", "support requires a completed observation from a validated local instrument")
		}
		if len(s.supportLosses(invocationNode(inv.Key))) != 0 {
			return faultAt(CodeInvalidTransition, b.Sequence, idx, "evidence", "observation has unresolved support loss")
		}
		supported = true
	}
	if !supported {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, "evidence", "proof has no supporting local observation")
	}
	if len(s.supportLosses(recordNode(e.Claim))) != 0 {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, "claim", "claim has unresolved support loss")
	}
	return nil
}

func sameSet[T comparable](a, b []T) bool {
	left, right := map[T]bool{}, map[T]bool{}
	for _, v := range a {
		left[v] = true
	}
	for _, v := range b {
		right[v] = true
	}
	return reflect.DeepEqual(left, right)
}

// Scope prose has no executable containment relation. Exact declared semantics
// are the supported case until admission supplies a richer authored relation.
func sameScope(a, b model.Scope) bool {
	return a.AppliesWhen == b.AppliesWhen && a.Limitations == b.Limitations && sameSet(a.SourcePaths, b.SourcePaths) && sameSet(a.ContextRefs, b.ContextRefs)
}

func (s *state) decisionDispose(b model.Bundle, idx int, e *model.DecisionDispose) error {
	if err := s.requireKind(b, idx, e.Decision, model.Decision, "decision"); err != nil {
		return err
	}
	rec := s.records[recordKey(e.Decision)]
	if model.Blank(e.Authority.Actor.ID) || !sameScope(rec.Decision.Scope, e.Scope) || !sameScope(e.Scope, e.Authority.Scope) {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, "authority", "disposition requires a named authority and the exact declared scope")
	}
	return nil
}

func (s *state) supersede(b model.Bundle, idx int, e *model.Supersede) error {
	prior := s.records[recordKey(e.Prior)]
	replacement, ok := s.records[recordKey(e.Replacement)]
	if e.Prior == e.Replacement || (ok && prior.Kind != replacement.Kind) {
		return faultAt(CodeInvalidTransition, b.Sequence, idx, "replacement", "supersession needs a different record revision of the same kind")
	}
	return nil
}

func matchesInvocationIntent(inv Invocation) bool {
	if inv.Seal == nil {
		return false
	}
	start, seal := inv.Start, *inv.Seal
	if start.InvocationID != seal.InvocationID || start.AttemptID != seal.AttemptID || start.InstrumentRef != seal.InstrumentRef ||
		!reflect.DeepEqual(start.CriterionRef, seal.CriterionRef) || !reflect.DeepEqual(start.ExecutionSourceIdentity, seal.ExecutionSourceIdentity) ||
		!reflect.DeepEqual(start.Argv, seal.Argv) || !reflect.DeepEqual(start.InputRefs, seal.InputRefs) ||
		!reflect.DeepEqual(start.ConfigRequested, seal.ConfigRequested) || !reflect.DeepEqual(start.ConditionsDeclared, seal.ConditionsDeclared) ||
		!start.StartedAt.Equal(seal.StartedAt) {
		return false
	}
	return true
}

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

// Support combines admitted loss with optional read-time facts. The supplied
// context can never turn an admitted disposal or invalidation back into support.
func (s Snapshot) Support(ref model.RecordRef, context ...SupportContext) (SupportFacts, bool) {
	f, ok := s.support(ref, context...)
	return deepCopy(f), ok
}

func (s Snapshot) support(ref model.RecordRef, context ...SupportContext) (SupportFacts, bool) {
	rec, ok := s.inner().record(ref)
	if !ok {
		return SupportFacts{}, false
	}
	facts := SupportFacts{EvidenceAvailable: TruthUnknown, ActiveTrust: TruthTrue, ApplicableScope: TruthTrue, CorrectionFree: TruthTrue}
	if len(context) > 0 {
		facts.EvidenceAvailable = truthAnd(context[0].EvidenceAvailable)
		facts.ApplicableScope = truthAnd(context[0].ScopeApplicable)
	}
	if s.inner().current[ident(ref)] != ref.Revision {
		facts.ApplicableScope = TruthFalse
	}
	switch rec.Kind {
	case model.Claim, model.Decision:
		established := false
		for _, event := range s.inner().events {
			switch e := event.(type) {
			case *model.ProofAdmit:
				established = established || (rec.Kind == model.Claim && e.Claim == ref)
			case *model.DecisionDispose:
				established = established || (rec.Kind == model.Decision && e.Decision == ref)
			}
		}
		if !established {
			facts.ApplicableScope = TruthFalse
		}
	case model.Instrument:
		if rec.Instrument.Validation.State != model.Known {
			facts.ActiveTrust = TruthUnknown
		}
	}
	facts.Losses = s.inner().supportLosses(recordNode(ref))
	for _, loss := range facts.Losses {
		switch loss.Type {
		case "correction":
			facts.CorrectionFree = TruthFalse
		case "trust.withdraw":
			facts.ActiveTrust = TruthFalse
		case "artifact.dispose":
			facts.EvidenceAvailable = TruthFalse
		case "supersede":
			facts.ApplicableScope = TruthFalse
		}
	}
	return facts, true
}
