package main

// This file holds `whosaidso check claim ATTEMPT_ID`: whether the holder of
// one attempt may keep working, read from this checkout's ledger. The status
// is decided in internal/query (claim.go); this verb prints it and maps it to
// an exit status: 0 HELD, 1 LOST, TAKEN-OVER or ENDED, 3 UNKNOWN.

import (
	"encoding/json"
	"flag"
	"fmt"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/query"
)

func claimCheckVerb(fs *flag.FlagSet) func(*call) error {
	jsonOutput := jsonFlag(fs)
	return func(c *call) error {
		if c.argv != nil || len(c.args) != 1 {
			return usageError("whosaidso check claim needs one ATTEMPT_ID")
		}
		project, err := c.project()
		if err != nil {
			return err
		}
		a, err := query.ReadClaim(project, model.ID(c.args[0]))
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
			fmt.Fprintf(c.stdout, "claim %s at watermark sequence %d: %s\n  %s\n  next: %s\n", a.Attempt, a.Watermark.Sequence, a.Status, a.Reason, a.Next)
		}
		switch a.Status {
		case query.ClaimHeld:
			return nil
		case query.ClaimUnknown:
			return &exitError{code: 3}
		}
		return &exitError{code: 1}
	}
}
