package query

// The Source seam lives here: what one answer reads its ledger facts from.
// Views, rendering and request checks do not.

import (
	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

// Source is one selected ledger prefix: the snapshot reduced from it, and the
// raw bundles for the one view that prints them (history). Both describe the
// same prefix; a Source never answers from two.
type Source interface {
	Snapshot() reduce.Snapshot
	Bundles() ([]model.Bundle, error)
}

// replayed is a Source folded from the whole prefix, the full-replay path.
type replayed struct {
	snapshot reduce.Snapshot
	bundles  []model.Bundle
}

func (r replayed) Snapshot() reduce.Snapshot        { return r.snapshot }
func (r replayed) Bundles() ([]model.Bundle, error) { return r.bundles, nil }

func replay(project store.Project) (Source, error) {
	prefix, err := store.ReadPrefix(project)
	if err != nil {
		return nil, err
	}
	snapshot, err := reduce.Replay(prefix)
	if err != nil {
		return nil, err
	}
	return replayed{snapshot: snapshot, bundles: prefix}, nil
}
