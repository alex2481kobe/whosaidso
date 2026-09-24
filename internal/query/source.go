package query

// The Source seam lives here: what one answer reads its ledger facts from.
// Views, rendering and request checks do not.

import (
	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
)

// Source is one selected ledger prefix: the snapshot reduced from it, and the
// raw bundles for the one view that prints them (history). Both describe the
// same prefix; a Source never answers from two. store.State is the Source
// every command uses: validated against the whole prefix, cached or rebuilt.
type Source interface {
	Snapshot() reduce.Snapshot
	Bundles() ([]model.Bundle, error)
}
