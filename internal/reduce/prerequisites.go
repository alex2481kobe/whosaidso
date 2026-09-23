package reduce

// The claim-proof and decision-approved prerequisite predicates live here.
// They read the exact-revision CLAIM and DECISION projections and the admitted
// support premises; they never evaluate another task, so a cyclic prerequisite
// chain cannot recurse. task-success and derived TASK status stay in task.go.

import (
	"fmt"

	"datum/internal/model"
)

// exactTarget resolves a prerequisite target to one admitted revision of the
// wanted kind. A target this ledger cannot read is UNKNOWN; a target of the
// wrong kind was compared and disagreed, so it is FALSE.
func (s *state) exactTarget(target model.RecordRef, want model.Kind) (Truth, string, bool) {
	if target.Project != s.project {
		return TruthUnknown, "cross-project dependency resolves at read time", false
	}
	rec, ok := s.records[recordKey(target)]
	if !ok {
		return TruthUnknown, fmt.Sprintf("no admitted revision %d of %s", target.Revision, target.RecordID), false
	}
	if rec.Kind != want {
		return TruthFalse, fmt.Sprintf("%s is a %s, not a %s", target.RecordID, rec.Kind, want), false
	}
	return "", "", true
}

// ledgerSupport answers "is this exact revision currently supported?" from the
// ledger alone. Every premise the ledger decides must be TRUE: the revision is
// current and established, not superseded, not corrected, trust not withdrawn,
// and no admitted disposal took its evidence. Whether artifact bytes are on this
// machine's disk is a read-time observation the fold never makes; letting it
// decide would make READY, and so admission of a start, differ between machines.
// An admitted disposal is the ledger's own record of evidence loss and is FALSE.
func (s *state) ledgerSupport(target model.RecordRef) (Truth, string) {
	facts, _ := Snapshot{st: s}.support(target)
	premises := []struct {
		name  string
		truth Truth
	}{
		{"applicable scope", facts.ApplicableScope},
		{"active trust", facts.ActiveTrust},
		{"correction-free", facts.CorrectionFree},
	}
	if facts.EvidenceAvailable == TruthFalse {
		return TruthFalse, "its evidence was disposed"
	}
	unknown := ""
	for _, p := range premises {
		if p.truth == TruthFalse {
			return TruthFalse, fmt.Sprintf("its %s premise is FALSE", p.name)
		}
		if p.truth != TruthTrue && unknown == "" {
			unknown = fmt.Sprintf("its %s premise is UNKNOWN", p.name)
		}
	}
	if unknown != "" {
		return TruthUnknown, unknown
	}
	return TruthTrue, ""
}

// claimProof is TRUE only when the exact claim revision is PROVEN and that
// proof is currently supported. Historical PROVEN alone never counts: a
// withdrawn instrument, a correction or a supersession leaves the achievement
// visible and the prerequisite FALSE.
func (s *state) claimProof(target model.RecordRef) (Truth, string) {
	if truth, detail, ok := s.exactTarget(target, model.Claim); !ok {
		return truth, detail
	}
	claim, _ := Snapshot{st: s}.claimAt(target)
	if claim.Status != StatusProven {
		return TruthFalse, fmt.Sprintf("claim %s revision %d is %s, not PROVEN", target.RecordID, target.Revision, claim.Status)
	}
	truth, detail := s.ledgerSupport(target)
	if truth != TruthTrue {
		return truth, fmt.Sprintf("claim %s revision %d is PROVEN but not currently supported: %s", target.RecordID, target.Revision, detail)
	}
	return TruthTrue, fmt.Sprintf("claim %s revision %d is PROVEN and currently supported", target.RecordID, target.Revision)
}

// decisionApproved is TRUE only when the disposition in force on the exact
// decision revision is approved and the decision is currently supported. The
// disposition in force is the latest one admitted on that revision. OPEN,
// rejected and withdrawn were all compared against "approved" and disagreed,
// so each is FALSE, never UNKNOWN.
func (s *state) decisionApproved(target model.RecordRef) (Truth, string) {
	if truth, detail, ok := s.exactTarget(target, model.Decision); !ok {
		return truth, detail
	}
	decision, _ := Snapshot{st: s}.decisionAt(target)
	if len(decision.Dispositions) == 0 {
		return TruthFalse, fmt.Sprintf("decision %s revision %d is OPEN", target.RecordID, target.Revision)
	}
	inForce := decision.Dispositions[len(decision.Dispositions)-1].Disposition.Disposition
	if inForce != "approved" {
		return TruthFalse, fmt.Sprintf("decision %s revision %d is %s", target.RecordID, target.Revision, inForce)
	}
	truth, detail := s.ledgerSupport(target)
	if truth != TruthTrue {
		return truth, fmt.Sprintf("decision %s revision %d is approved but not in force: %s", target.RecordID, target.Revision, detail)
	}
	return TruthTrue, fmt.Sprintf("decision %s revision %d is approved and in force", target.RecordID, target.Revision)
}
