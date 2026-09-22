// This file holds `datum id [N]`, which prints N fresh record identifiers
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
package main

import (
	"crypto/rand"
	"fmt"
	"io"
	"strconv"
	"time"

	"datum/internal/model"
)

// idCLI implements `datum id [N]` and returns the process exit code: 0 on
// success, 2 for a count that is not one or more, 1 if minting fails.
func idCLI(args []string, stdout, stderr io.Writer) int {
	n := 1
	if len(args) > 0 {
		parsed, err := strconv.Atoi(args[0])
		if err != nil || parsed < 1 {
			fmt.Fprintf(stderr, "datum id: %q is not a count of one or more\n", args[0])
			return 2
		}
		n = parsed
	}
	for i := 0; i < n; i++ {
		id, err := model.NewID(time.Now(), rand.Reader)
		if err != nil {
			// A failure here is a short read from the entropy source. Minting
			// a weaker id instead would be the worst possible recovery.
			fmt.Fprintln(stderr, "datum id:", err)
			return 1
		}
		fmt.Fprintln(stdout, id)
		// Two ids minted inside one millisecond differ only in entropy, which
		// is correct but reads as unsorted. A millisecond apart keeps the
		// human-facing ordering that the time prefix exists for.
		if i+1 < n {
			time.Sleep(time.Millisecond)
		}
	}
	return 0
}
