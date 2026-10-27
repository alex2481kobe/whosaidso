package main

// Text rendering of ledger comparisons lives here; internal/write owns the
// classes, identities, measurement states, and refusal policy.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/alex2481kobe/whosaidso/internal/write"
)

func ledgerComparisonText(a *write.LedgerComparison) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ledger comparison base %s -> head %s\n", a.Base.Commit, a.Head.Commit)
	if a.Base.CheckError != "" {
		fmt.Fprintf(&b, "base check error: %s\n", a.Base.CheckError)
	}
	if a.Head.CheckError != "" {
		fmt.Fprintf(&b, "head check error: %s\n", a.Head.CheckError)
	}
	names := make([]string, 0, len(a.Classes))
	for name := range a.Classes {
		names = append(names, name)
	}
	slices.Sort(names)
	count := func(c write.LedgerClass) string {
		if c.Count == nil {
			return c.State
		}
		return fmt.Sprint(*c.Count)
	}
	for _, name := range names {
		c := a.Classes[name]
		fmt.Fprintf(&b, "%s: %s -> %s\n", name, count(c.Base), count(c.Head))
		for _, id := range c.Added {
			fmt.Fprintf(&b, "  added: %s\n", id)
		}
		for _, id := range c.Resolved {
			fmt.Fprintf(&b, "  resolved: %s\n", id)
		}
	}
	ledgerPinLocationsText(&b, "base ", a.Base.GitPinLocations)
	ledgerPinLocationsText(&b, "head ", a.Head.GitPinLocations)
	return b.String()
}
