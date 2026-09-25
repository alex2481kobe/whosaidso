package evidence

// This file holds what pinned readings state, as one answer, for a criterion's
// three metadata fields (unit, population identity, denominator): the drafting
// side of metadataAgreement (observations.go), which checks the same fields
// against the same declarations at evaluation. Selecting bytes lives in
// selectors.go; comparing a criterion with a run lives in observations.go.

import "github.com/alex2481kobe/whosaidso/internal/model"

// StatedMetadata is each of "unit", "population" and "denominator" that the
// readings establish uniquely: at least one declaration is present, every
// present one is KNOWN, and all say the same. The declarations are the ones
// metadataAgreement compares: the result's reading-wide and member metadata,
// and the population reading's, whose unit is not asked (members identify the
// denominator, not the unit). An absent, UNKNOWN or disagreeing field is left
// out: it has no one answer to copy. A nil reading states nothing.
func StatedMetadata(result, population *Reading) map[string]string {
	declared := map[string][]model.Availability[string]{}
	add := func(r *Reading, unit bool) {
		if r == nil {
			return
		}
		sets := []map[string]model.Availability[string]{{"unit": r.Unit, "population": r.Population, "denominator": r.Denominator}}
		sets = append(sets, r.MemberMetadata...)
		for _, set := range sets {
			for key, s := range set {
				if s.State == "" || key == "unit" && !unit {
					continue // omitted, or not asked of this reading
				}
				declared[key] = append(declared[key], s)
			}
		}
	}
	add(result, true)
	add(population, false)
	out := map[string]string{}
	for key, all := range declared {
		value, unique := "", true
		for i, s := range all {
			if s.State != model.Known || s.Value == nil || i > 0 && *s.Value != value {
				unique = false
				break
			}
			value = *s.Value
		}
		if unique && len(all) > 0 {
			out[key] = value
		}
	}
	return out
}
