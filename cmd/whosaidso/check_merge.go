package main

// This file holds `whosaidso check merge [--ref REF]`: the merge gate. What
// it checks is decided in internal/write (check_merge.go); this verb gives it
// a temporary directory, prints the finding, and exits 0 when the ledger at
// REF is whole for anyone who clones it, 1 when it is not.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/alex2481kobe/whosaidso/internal/write"
)

func mergeCheckVerb(fs *flag.FlagSet) func(*call) error {
	jsonOutput := jsonFlag(fs)
	ref := fs.String("ref", "HEAD", "the `COMMIT` to check: a branch, tag or commit of this checkout's repository")
	return func(c *call) error {
		if err := noPositionals(c, "check merge"); err != nil {
			return err
		}
		project, err := c.project()
		if err != nil {
			return err
		}
		scratch, err := os.MkdirTemp("", "whosaidso-check-merge-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(scratch)
		a, err := write.CheckMerge(c.ctx, project, *ref, scratch)
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
			fmt.Fprint(c.stdout, mergeText(a))
		}
		if !a.Clean {
			return &exitError{code: 1}
		}
		return nil
	}
}

func mergeText(a *write.MergeAnswer) string {
	var b strings.Builder
	result := "clean"
	if !a.Clean {
		result = "PROBLEMS"
	}
	fmt.Fprintf(&b, "merge check of %s (commit %s) in a fresh full-history clone, no cache, no home: %d bundles\nresult: %s\n", a.Ref, a.Commit, a.Bundles, result)
	if a.ReplayError != "" {
		fmt.Fprintf(&b, "the ledger does not replay: %s\n", a.ReplayError)
	}
	if a.Artifacts != "" {
		fmt.Fprintf(&b, "content artifacts missing from the commit: %s\n", a.Artifacts)
	}
	list := func(title string, items []string) {
		if len(items) > 0 {
			fmt.Fprintf(&b, "%s: %d\n", title, len(items))
			for _, item := range items {
				fmt.Fprintf(&b, "  %s\n", item)
			}
		}
	}
	list("git pins", a.GitPins)
	aside := make([]string, len(a.SetAside))
	for i, s := range a.SetAside {
		aside[i] = fmt.Sprintf("sequence %d command %s: %s: %s", s.Sequence, s.CommandID, s.Code, s.Reason)
	}
	list("set-aside bundles (two branches clashed; whosaidso todo says what to do)", aside)
	list("bundles in this checkout's ledger but not in the commit (commit them, or merge the branch that has them)", a.NotCommitted)
	return b.String()
}
