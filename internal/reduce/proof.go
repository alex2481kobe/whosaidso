package reduce

// Claim, decision, and instrument projections and their admitted payload types live here.
// Transition validation and current-support loss traversal do not.

import (
	"datum/internal/model"
)

// ClaimStatus records achievement at one assertion revision, not current support.
type ClaimStatus string

const (
	StatusUnmeasured ClaimStatus = "UNMEASURED"
	StatusMeasured   ClaimStatus = "MEASURED"
	StatusProven     ClaimStatus = "PROVEN"
	// StatusRefuted (R14.1) is a claim whose proof in force, the latest
	// admitted on this exact revision, has the verdict refutes. A later
	// supports proof, under a new criterion revision or after R14.2 set the
	// failing runs aside, proves it again.
	StatusRefuted ClaimStatus = "REFUTED"
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
	Admission model.ProofAdmit `json:"admission"`
	Origin    Origin           `json:"origin"`
}

type DecisionDisposition struct {
	Disposition model.DecisionDispose `json:"disposition"`
	Origin      Origin                `json:"origin"`
	Author      PacketAuthor          `json:"author"` // who wrote the packet, beside the authority and quote
}

type TrustWithdrawal struct {
	Withdrawal model.TrustWithdraw `json:"withdrawal"`
	Origin     Origin              `json:"origin"`
}

type Supersession struct {
	Supersede model.Supersede `json:"supersede"`
	Origin    Origin          `json:"origin"`
}

type AdmittedCorrection struct {
	Correction model.Correction `json:"correction"`
	Origin     Origin           `json:"origin"`
}

type ArtifactDisposal struct {
	Disposal model.ArtifactDispose `json:"disposal"`
	Origin   Origin                `json:"origin"`
}

type ClaimProjection struct {
	Claim        RecordKey        `json:"claim"`
	Spec         *model.ClaimSpec `json:"spec"`
	Status       ClaimStatus      `json:"status"`
	Observations []Invocation     `json:"observations"`
	Proofs       []ProofAdmission `json:"proofs"`
	Support      SupportFacts     `json:"support"`
}

type DecisionProjection struct {
	Decision     RecordKey             `json:"decision"`
	Spec         *model.DecisionSpec   `json:"spec"`
	Status       DecisionStatus        `json:"status"`
	Dispositions []DecisionDisposition `json:"dispositions"`
	Support      SupportFacts          `json:"support"`
}

// InstrumentProjection deliberately has no Status. Versioned validation and
// withdrawals are facts, not a tenth meaning of status.
type InstrumentProjection struct {
	Instrument  RecordKey             `json:"instrument"`
	Spec        *model.InstrumentSpec `json:"spec"`
	Withdrawals []TrustWithdrawal     `json:"withdrawals"`
	Support     SupportFacts          `json:"support"`
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
	for _, o := range s.inner().log.proofs {
		if e, ok := s.inner().events[o].(*model.ProofAdmit); ok && e.Claim == ref {
			p.Proofs = append(p.Proofs, ProofAdmission{Admission: *e, Origin: o})
			p.Status = StatusProven
			if e.Refutes() {
				p.Status = StatusRefuted
			}
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
	for _, o := range s.inner().log.decisionDisposals {
		if e, ok := s.inner().events[o].(*model.DecisionDispose); ok && e.Decision == ref {
			p.Dispositions = append(p.Dispositions, DecisionDisposition{Disposition: *e, Origin: o, Author: s.inner().eventAuthor(o)})
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
