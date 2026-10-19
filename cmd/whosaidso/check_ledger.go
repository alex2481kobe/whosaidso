package main

// This file holds `whosaidso check ledger`: is the ledger at this checkout's
// HEAD whole for anyone who clones it? What it checks is decided in
// internal/write (check_ledger.go); this verb prints the finding and exits 0
// clean, 1 problems.

import (
	"encoding/json"
	"flag"
	"fmt"
	"strings"

	"github.com/alex2481kobe/whosaidso/internal/write"
)

func ledgerCheckVerb(fs *flag.FlagSet) func(*call) error {
	jsonOutput := jsonFlag(fs)
	return func(c *call) error {
		if err := noPositionals(c, "check ledger"); err != nil {
			return err
		}
		project, err := c.project()
		if err != nil {
			return err
		}
		a, err := write.CheckLedger(c.ctx, project)
		if err != nil {
			return err
		}
		if *jsonOutput {
			encoder := json.NewEncoder(c.stdout)
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(a); err != nil {
				return err
			}
		} else {
			fmt.Fprint(c.stdout, ledgerText(a))
		}
		if !a.Clean {
			return &exitError{code: 1}
		}
		return nil
	}
}

func ledgerText(a *write.LedgerCheck) string {
	var b strings.Builder
	result := "clean"
	if !a.Clean {
		result = "PROBLEMS"
	}
	fmt.Fprintf(&b, "ledger check at HEAD %s, no cache, no home: %d bundles\nresult: %s\n", a.Commit, a.Bundles, result)
	if a.ReplayError != "" {
		fmt.Fprintf(&b, "the ledger does not replay: %s\n", a.ReplayError)
	}
	if a.Artifacts != "" {
		fmt.Fprintf(&b, "content artifacts missing: %s\n", a.Artifacts)
	}
	list := func(title string, items []string) {
		if len(items) > 0 {
			fmt.Fprintf(&b, "%s: %d\n", title, len(items))
			for _, item := range items {
				fmt.Fprintf(&b, "  %s\n", item)
			}
		}
	}
	list("not committed at HEAD (git add and commit them)", a.NotCommitted)
	list("git pins", a.GitPins)
	aside := make([]string, len(a.SetAside))
	for i, s := range a.SetAside {
		aside[i] = fmt.Sprintf("sequence %d command %s: %s: %s", s.Sequence, s.CommandID, s.Code, s.Reason)
	}
	list("set-aside bundles not resolved (whosaidso todo says what to do)", aside)
	if n := len(a.Resolved); n > 0 {
		fmt.Fprintf(&b, "set-aside bundles resolved: %d\n", n)
	}
	return b.String()
}
