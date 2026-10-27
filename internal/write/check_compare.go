package write

// Comparing ledger measurements lives here. Measurement acquisition delegates
// to CheckLedger; the command owns path discovery and rendering.

import (
	"context"
	"fmt"
	"slices"

	"github.com/alex2481kobe/whosaidso/internal/store"
)

// LedgerClassChange reports identity churn even when the count does not rise.
type LedgerClassChange struct {
	Base     LedgerClass `json:"base"`
	Head     LedgerClass `json:"head"`
	Added    []string    `json:"added"`
	Resolved []string    `json:"resolved"`
}

type LedgerComparison struct {
	Base     *LedgerCheck                 `json:"base"`
	Head     *LedgerCheck                 `json:"head"`
	Classes  map[string]LedgerClassChange `json:"classes"`
	Allowed  bool                         `json:"allowed"`
	Refusals []string                     `json:"refusals,omitempty"`
}

// CheckLedgerAgainst runs the identical cache-free check in both checkouts.
// Existing problems may remain or fall; unknown measurements never pass.
func CheckLedgerAgainst(ctx context.Context, base, head store.Project) (*LedgerComparison, error) {
	if base.ID != head.ID {
		return nil, fmt.Errorf("refused: checkouts declare different projects: %s and %s", base.ID, head.ID)
	}
	b, err := CheckLedger(ctx, base)
	if err != nil {
		b = &LedgerCheck{Classes: unmeasuredLedgerClasses(), CheckError: err.Error()}
	}
	h, err := CheckLedger(ctx, head)
	if err != nil {
		h = &LedgerCheck{Classes: unmeasuredLedgerClasses(), CheckError: err.Error()}
	}
	return compareLedgerChecks(b, h), nil
}

func compareLedgerChecks(base, head *LedgerCheck) *LedgerComparison {
	result := &LedgerComparison{Base: base, Head: head, Classes: map[string]LedgerClassChange{}, Allowed: true}
	for _, name := range ledgerClassNames {
		b, h := base.Classes[name], head.Classes[name]
		change := LedgerClassChange{Base: b, Head: h, Added: []string{}, Resolved: []string{}}
		measured := true
		for _, side := range []struct {
			name  string
			class LedgerClass
		}{{"base", b}, {"head", h}} {
			if side.class.State != "measured" || side.class.Count == nil {
				measured = false
				result.Refusals = append(result.Refusals, fmt.Sprintf("refused: %s unmeasured at %s", name, side.name))
			}
		}
		if measured {
			change.Added = identityDifference(h.Identities, b.Identities)
			change.Resolved = identityDifference(b.Identities, h.Identities)
			if *h.Count > *b.Count {
				result.Refusals = append(result.Refusals, fmt.Sprintf("refused: %s increased: %d -> %d", name, *b.Count, *h.Count))
			}
		}
		result.Classes[name] = change
	}
	result.Allowed = len(result.Refusals) == 0
	return result
}

func identityDifference(from, other []string) []string {
	seen := make(map[string]bool, len(other))
	for _, id := range other {
		seen[id] = true
	}
	result := []string{}
	for _, id := range from {
		if !seen[id] {
			result = append(result, id)
			seen[id] = true
		}
	}
	slices.Sort(result)
	return result
}
