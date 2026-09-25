package reduce

// Current-support premises, support queries, and ordered loss-event histories live here,
// including the ledger-order event inventories those histories are read from.
// Achievement projections and reverse-reference graph traversal do not.

import (
	"github.com/alex2481kobe/whosaidso/internal/model"
)

// SupportLossFact identifies the cause even when it reached this record indirectly.
type SupportLossFact struct {
	Origin Origin          `json:"origin"`
	Type   model.EventType `json:"type"`
}

// SupportContext contains read-time checks supplied by the caller. Evidence must
// cover the complete support being quoted. Replay cannot establish available bytes
// or decide whether a different real-world use falls inside authored scope.
type SupportContext struct {
	EvidenceAvailable Truth `json:"evidence_available"`
	ScopeApplicable   Truth `json:"scope_applicable"`
}

// SupportFacts keeps independent reasons separate so loss of trust never rewrites
// achievement or pretends that the underlying bytes disappeared.
type SupportFacts struct {
	EvidenceAvailable Truth             `json:"evidence_available"`
	ActiveTrust       Truth             `json:"active_trust"`
	ApplicableScope   Truth             `json:"applicable_scope"`
	CorrectionFree    Truth             `json:"correction_free"`
	Losses            []SupportLossFact `json:"losses"`
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

// eventLog lists admitted origins in ledger order. apply adds each event as it
// is admitted, and bundles apply in sequence with their events in index order,
// so appending IS ledger order: nothing is sorted, and a snapshot's log holds
// exactly its own prefix, including events already applied from a bundle that
// is still being applied. The typed lists are subsequences of all.
type eventLog struct {
	all               []Origin
	losses            []Origin // correction, trust.withdraw, supersede, artifact.dispose
	proofs            []Origin
	decisionDisposals []Origin
	supersessions     []Origin
	corrections       []Origin
	withdrawals       []Origin
	disposals         []Origin
}

func (l *eventLog) add(o Origin, e model.TypedEvent) {
	l.all = append(l.all, o)
	switch e.(type) {
	case *model.ProofAdmit:
		l.proofs = append(l.proofs, o)
	case *model.DecisionDispose:
		l.decisionDisposals = append(l.decisionDisposals, o)
	case *model.Supersede:
		l.supersessions = append(l.supersessions, o)
		l.losses = append(l.losses, o)
	case *model.Correction:
		l.corrections = append(l.corrections, o)
		l.losses = append(l.losses, o)
	case *model.TrustWithdraw:
		l.withdrawals = append(l.withdrawals, o)
		l.losses = append(l.losses, o)
	case *model.ArtifactDispose:
		l.disposals = append(l.disposals, o)
		l.losses = append(l.losses, o)
	}
}

// fork caps every list at its length, so the first append on either side
// reallocates instead of writing into a backing array the other still reads.
// Two Applys onto one snapshot therefore never see each other's events.
func (l eventLog) fork() eventLog {
	clip := func(o []Origin) []Origin { return o[:len(o):len(o)] }
	return eventLog{
		all:               clip(l.all),
		losses:            clip(l.losses),
		proofs:            clip(l.proofs),
		decisionDisposals: clip(l.decisionDisposals),
		supersessions:     clip(l.supersessions),
		corrections:       clip(l.corrections),
		withdrawals:       clip(l.withdrawals),
		disposals:         clip(l.disposals),
	}
}

func (s Snapshot) Corrections() []AdmittedCorrection { return deepCopySlice(s.corrections()) }

func (s Snapshot) corrections() []AdmittedCorrection {
	out := []AdmittedCorrection{}
	for _, o := range s.inner().log.corrections {
		if e, ok := s.inner().events[o].(*model.Correction); ok {
			out = append(out, AdmittedCorrection{Correction: *e, Origin: o})
		}
	}
	return out
}

func (s Snapshot) Supersessions() []Supersession { return deepCopySlice(s.supersessions()) }

func (s Snapshot) supersessions() []Supersession {
	out := []Supersession{}
	for _, o := range s.inner().log.supersessions {
		if e, ok := s.inner().events[o].(*model.Supersede); ok {
			out = append(out, Supersession{Supersede: *e, Origin: o})
		}
	}
	return out
}

func (s Snapshot) TrustWithdrawals() []TrustWithdrawal { return deepCopySlice(s.trustWithdrawals()) }

func (s Snapshot) trustWithdrawals() []TrustWithdrawal {
	out := []TrustWithdrawal{}
	for _, o := range s.inner().log.withdrawals {
		if e, ok := s.inner().events[o].(*model.TrustWithdraw); ok {
			out = append(out, TrustWithdrawal{Withdrawal: *e, Origin: o})
		}
	}
	return out
}

func (s Snapshot) ArtifactDisposals() []ArtifactDisposal { return deepCopySlice(s.artifactDisposals()) }

func (s Snapshot) artifactDisposals() []ArtifactDisposal {
	out := []ArtifactDisposal{}
	for _, o := range s.inner().log.disposals {
		if e, ok := s.inner().events[o].(*model.ArtifactDispose); ok {
			out = append(out, ArtifactDisposal{Disposal: *e, Origin: o})
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
		for _, o := range s.inner().log.proofs {
			// The proof in force is the latest on this revision: a refutation
			// establishes nothing, and a later supports proof re-establishes.
			if e := s.inner().events[o].(*model.ProofAdmit); rec.Kind == model.Claim && e.Claim == ref {
				established = !e.Refutes()
			}
		}
		for _, o := range s.inner().log.decisionDisposals {
			e := s.inner().events[o].(*model.DecisionDispose)
			established = established || (rec.Kind == model.Decision && e.Decision == ref)
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
