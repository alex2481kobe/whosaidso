package main

// This file holds `whosaidso id [N]`, which prints N fresh record identifiers
// (default one). It belongs here and nowhere else because it touches no
// project state: no discovery, no ledger, no intake. Commands that read or
// write the record do not belong in this file.
//
// Authoring an event by hand requires a ULID for the record it creates.
// Hand-computing Crockford base32 predictably yields invented ids that happen
// to parse. This uses the same model.NewID as the rest of the system: 48 bits
// of millisecond time and 80 bits of crypto/rand entropy. The time prefix
// makes ids roughly sortable for a human; it is NEVER the ordering the system
// uses. Causal order is ledger sequence, always.

import (
	"crypto/rand"
	"flag"
	"fmt"
	"strconv"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// idVerb prints N fresh ids, one per line (default one). A count that is not
// one or more is a usage error.
func idVerb(fs *flag.FlagSet) func(*call) error {
	return func(c *call) error {
		n := 1
		if len(c.args) > 1 || c.argv != nil {
			return usageError("whosaidso id takes at most one count")
		}
		if len(c.args) == 1 {
			parsed, err := strconv.Atoi(c.args[0])
			if err != nil || parsed < 1 {
				return usageError("whosaidso id: %q is not a count of one or more", c.args[0])
			}
			n = parsed
		}
		for i := 0; i < n; i++ {
			id, err := model.NewID(time.Now(), rand.Reader)
			if err != nil {
				// A failure here is a short read from the entropy source. Minting
				// a weaker id instead would be the worst possible recovery.
				return fmt.Errorf("whosaidso id: %w", err)
			}
			fmt.Fprintln(c.stdout, id)
			// Two ids minted inside one millisecond differ only in entropy, which
			// is correct but reads as unsorted. A millisecond apart keeps the
			// human-facing ordering that the time prefix exists for.
			if i+1 < n {
				time.Sleep(time.Millisecond)
			}
		}
		return nil
	}
}
