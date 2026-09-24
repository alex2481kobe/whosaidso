package write

// The one resolution of "which criterion, at which revisions" lives here,
// shared by run's defaults and the bound templates: whatever the author
// omitted (the claim, its revision, the criterion id, its revision) becomes
// the CURRENT admitted one, read from one loaded ledger prefix. An omission
// that has no single correct answer is refused with the candidates named,
// never guessed. Running the measurement and checking the criterion's
// freezing stay in run.go and the reducer.

import (
	"fmt"
	"sort"
	"strings"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
)

// CriterionChoice is what an author named; a zero field was omitted.
type CriterionChoice struct {
	Claim             model.ID
	ClaimRevision     model.Revision
	CriterionID       model.ID
	CriterionRevision model.Revision
}

// AdmittedCriteria lists every admitted criterion revision of the prefix, in
// ledger order. Criterion revisions need not be contiguous, so they are read
// from the admitted criterion.fix events rather than counted.
func AdmittedCriteria(state store.State) ([]model.CriterionRef, error) {
	bundles, err := state.Bundles()
	if err != nil {
		return nil, err
	}
	snapshot := state.Snapshot()
	var out []model.CriterionRef
	for _, b := range bundles {
		for _, raw := range b.Events {
			if raw.Type != "criterion.fix" {
				continue
			}
			event, err := model.DecodeEvent(raw)
			if err != nil {
				return nil, err
			}
			fix := event.(*model.CriterionFix)
			ref := model.CriterionRef{Claim: fix.Claim, CriterionID: fix.CriterionID, Revision: fix.Revision}
			if _, ok := snapshot.Criterion(ref); ok {
				out = append(out, ref)
			}
		}
	}
	return out, nil
}

// ResolveCriterion fills each omitted part of want with the current admitted
// one: the claim that alone carries the criterion id, the claim's current
// revision, the one criterion id fixed on that revision, and that criterion's
// highest admitted revision there. A named part is checked, never replaced.
func ResolveCriterion(state store.State, project model.ProjectID, want CriterionChoice) (model.CriterionRef, error) {
	var none model.CriterionRef
	criteria, err := AdmittedCriteria(state)
	if err != nil {
		return none, err
	}
	snapshot := state.Snapshot()
	claim := want.Claim
	if claim == "" {
		if want.CriterionID == "" {
			return none, fmt.Errorf("name a claim or a criterion")
		}
		claims := distinct(criteria, func(r model.CriterionRef) (model.ID, bool) {
			return r.Claim.RecordID, r.CriterionID == want.CriterionID
		})
		if len(claims) != 1 {
			return none, fmt.Errorf("criterion %s is fixed on %d claims (%s); name the claim", want.CriterionID, len(claims), joinIDs(claims))
		}
		claim = claims[0]
	}
	claimRef := model.RecordRef{Project: project, RecordID: claim, Revision: want.ClaimRevision}
	if claimRef.Revision == 0 {
		current, ok := snapshot.CurrentRevision(reduce.Ident{Project: project, ID: claim})
		if !ok {
			return none, fmt.Errorf("claim %s is not admitted", claim)
		}
		claimRef.Revision = current
	}
	if rec, ok := snapshot.Record(claimRef); !ok || rec.Kind != model.Claim {
		return none, fmt.Errorf("claim %s revision %d is not admitted", claim, claimRef.Revision)
	}
	id := want.CriterionID
	if id == "" {
		ids := distinct(criteria, func(r model.CriterionRef) (model.ID, bool) { return r.CriterionID, r.Claim == claimRef })
		if len(ids) != 1 {
			return none, fmt.Errorf("claim %s revision %d has %d admitted criteria (%s); name the criterion", claim, claimRef.Revision, len(ids), joinIDs(ids))
		}
		id = ids[0]
	}
	ref := model.CriterionRef{Claim: claimRef, CriterionID: id, Revision: want.CriterionRevision}
	if ref.Revision == 0 {
		for _, c := range criteria {
			if c.Claim == claimRef && c.CriterionID == id && c.Revision > ref.Revision {
				ref.Revision = c.Revision
			}
		}
		if ref.Revision == 0 {
			elsewhere := distinct(criteria, func(r model.CriterionRef) (model.ID, bool) {
				return model.ID(fmt.Sprint(r.Claim.Revision)), r.Claim.RecordID == claim && r.CriterionID == id
			})
			return none, fmt.Errorf("criterion %s has no admitted revision on claim %s revision %d (claim revisions it is fixed on: %s); fix it on this revision or name the claim revision",
				id, claim, claimRef.Revision, joinIDs(elsewhere))
		}
	}
	if _, ok := snapshot.Criterion(ref); !ok {
		return none, fmt.Errorf("criterion %s revision %d of claim %s revision %d is not admitted", id, ref.Revision, claim, claimRef.Revision)
	}
	return ref, nil
}

// NextCriterionRevision is the revision a new criterion.fix of id on claim
// takes: one past the highest admitted there, or 1 when none is.
func NextCriterionRevision(criteria []model.CriterionRef, claim model.RecordRef, id model.ID) model.Revision {
	var highest model.Revision
	for _, c := range criteria {
		if c.Claim == claim && c.CriterionID == id && c.Revision > highest {
			highest = c.Revision
		}
	}
	return highest + 1
}

func distinct(criteria []model.CriterionRef, pick func(model.CriterionRef) (model.ID, bool)) []model.ID {
	seen := map[model.ID]bool{}
	var out []model.ID
	for _, c := range criteria {
		if id, ok := pick(c); ok && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func joinIDs(ids []model.ID) string {
	if len(ids) == 0 {
		return "none"
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = string(id)
	}
	return strings.Join(parts, ", ")
}
