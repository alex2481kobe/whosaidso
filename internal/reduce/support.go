package reduce

// Current-support premises, support queries, and ordered loss-event histories live here.
// Achievement projections and reverse-reference graph traversal do not.
// This file stays below 200 lines because support answers and their history form a complete responsibility.

import (
	"sort"

	"datum/internal/model"
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
