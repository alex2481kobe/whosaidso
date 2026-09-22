// Command mintid prints fresh record identifiers.
//
// Authoring an event by hand requires a ULID for the record it creates, and
// nothing else in this repository will give you one. Hand-computing Crockford
// base32 is not a reasonable thing to ask of anyone, and the predictable
// result of asking is invented ids that happen to parse.
//
// It uses the same model.NewID as the rest of the system, so an id from here
// is the same kind of id the code mints: 48 bits of millisecond time and 80
// bits of crypto/rand entropy.
//
// The time prefix makes ids roughly sortable for a human reading a directory.
// It is NEVER the ordering the system uses. Causal order is ledger sequence,
// always.
//
// Usage:
//
//	go run ./tools/mintid        one id
//	go run ./tools/mintid 5      five ids
package main

import (
	"crypto/rand"
	"fmt"
	"os"
	"strconv"
	"time"

	"datum/internal/model"
)

func main() {
	n := 1
	if len(os.Args) > 1 {
		parsed, err := strconv.Atoi(os.Args[1])
		if err != nil || parsed < 1 {
			fmt.Fprintf(os.Stderr, "mintid: %q is not a count of one or more\n", os.Args[1])
			os.Exit(2)
		}
		n = parsed
	}
	for i := 0; i < n; i++ {
		id, err := model.NewID(time.Now(), rand.Reader)
		if err != nil {
			// A failure here is a short read from the entropy source. Minting
			// a weaker id instead would be the worst possible recovery.
			fmt.Fprintln(os.Stderr, "mintid:", err)
			os.Exit(1)
		}
		fmt.Println(id)
		// Two ids minted inside one millisecond differ only in entropy, which
		// is correct but reads as unsorted. A millisecond apart keeps the
		// human-facing ordering that the time prefix exists for.
		if i+1 < n {
			time.Sleep(time.Millisecond)
		}
	}
}
