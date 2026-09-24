package model

// Authored record creation, revision, disposition, and correction payloads live here.
// Task execution, evidence admission, and event encoding do not.
// This file stays below 200 lines because authored record history is a complete group.

// Provenance travels with each authored revision because Bundle retains packet
// digests, not packet bodies. Pure replay cannot recover authors from intake.
type Provenance struct {
	Author     Actor         `json:"author"`
	SourceRefs []ArtifactRef `json:"source_refs"`
}

// Creation/replacement payloads feed revision history in U05/U06 and authored
// context in U13. U08 checks that provenance matches the authored intake.
type TaskCreate struct {
	Provenance Provenance `json:"provenance"`
	ID         ID         `json:"id"`
	Spec       TaskSpec   `json:"spec"`
}
type TaskAmend struct {
	Provenance  Provenance `json:"provenance"`
	Target      RecordRef  `json:"target"`
	Replacement TaskSpec   `json:"replacement"`
}
type ClaimAssert struct {
	Provenance Provenance `json:"provenance"`
	ID         ID         `json:"id"`
	Spec       ClaimSpec  `json:"spec"`
}
type ClaimRevise struct {
	Provenance  Provenance `json:"provenance"`
	Target      RecordRef  `json:"target"`
	Replacement ClaimSpec  `json:"replacement"`
}
type DecisionOpen struct {
	Provenance Provenance   `json:"provenance"`
	ID         ID           `json:"id"`
	Spec       DecisionSpec `json:"spec"`
}
type DecisionRevise struct {
	Provenance  Provenance   `json:"provenance"`
	Target      RecordRef    `json:"target"`
	Replacement DecisionSpec `json:"replacement"`
}
type InstrumentDeclare struct {
	Provenance Provenance     `json:"provenance"`
	ID         ID             `json:"id"`
	Spec       InstrumentSpec `json:"spec"`
}
type InstrumentRevise struct {
	Provenance  Provenance     `json:"provenance"`
	Target      RecordRef      `json:"target"`
	Replacement InstrumentSpec `json:"replacement"`
}

// replacementRevision refuses a target no successor revision can follow. The
// target's revision is the one the writer replaces: a stale one is a conflict.
func replacementRevision(target RecordRef, p string) error {
	if target.Revision == ^Revision(0) {
		return invalid(p+".target.revision", "the target revision must permit a successor")
	}
	return nil
}
func (e TaskAmend) validate(p string) error        { return replacementRevision(e.Target, p) }
func (e ClaimRevise) validate(p string) error      { return replacementRevision(e.Target, p) }
func (e DecisionRevise) validate(p string) error   { return replacementRevision(e.Target, p) }
func (e InstrumentRevise) validate(p string) error { return replacementRevision(e.Target, p) }

// DecisionDispose supplies U06/U12 a scoped attributable ruling; U13 preserves
// the exact quote, including its original whitespace around nonblank words.
type DecisionDispose struct {
	Decision    RecordRef `json:"decision"`
	Disposition string    `json:"disposition"`
	Quote       string    `json:"quote" semantic:"text"`
	Scope       Scope     `json:"scope"`
	Authority   Authority `json:"authority"`
}

func (e DecisionDispose) validate(p string) error {
	return oneOf(e.Disposition, p+".disposition", "approved", "rejected", "withdrawn")
}

// Supersede preserves U06/U13 canonical history. U12 requires Authority when an
// owner ruling is affected; only stateful admission can identify that circumstance.
type Supersede struct {
	Prior       RecordRef  `json:"prior"`
	Replacement RecordRef  `json:"replacement"`
	Reason      string     `json:"reason" semantic:"text"`
	Authority   *Authority `json:"authority,omitempty"`
}

// SupportLink names the exact dependent revision and the evidence whose support
// is corrected. U06/U12 expand this through the reverse graph, with cycle detection.
type SupportLink struct {
	Dependent RecordRef   `json:"dependent"`
	Evidence  ArtifactRef `json:"evidence"`
}
type CorrectionTarget struct {
	Kind      string        `json:"kind"`
	Record    *RecordRef    `json:"record,omitempty"`
	Criterion *CriterionRef `json:"criterion,omitempty"`
	Support   *SupportLink  `json:"support,omitempty"`
}

func (r CorrectionTarget) validate(p string) error {
	n := 0
	if r.Record != nil {
		n++
	}
	if r.Criterion != nil {
		n++
	}
	if r.Support != nil {
		n++
	}
	if n != 1 {
		return invalid(p, "correction needs exactly one typed target")
	}
	switch r.Kind {
	case "record":
		if r.Record != nil {
			return nil
		}
	case "criterion":
		if r.Criterion != nil {
			return nil
		}
	case "support":
		if r.Support != nil {
			return nil
		}
	}
	return invalid(p, "target tag does not select its branch")
}

type Correction struct {
	Target            CorrectionTarget `json:"target"`
	AffectedRevisions []RecordRef      `json:"affected_revisions"`
	Reason            string           `json:"reason" semantic:"text"`
	CorrectiveRef     ArtifactRef      `json:"corrective_ref"`
}

func (e Correction) validate(p string) error {
	if len(e.AffectedRevisions) == 0 {
		return invalid(p+".affected_revisions", "exact affected revisions are required")
	}
	return nil
}
