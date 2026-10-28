// A long-lived reader retains one immutable snapshot, never ledger authority.
// Every read still inventories and hashes the complete prefix; disk images and
// admission remain owned by snapshot.go and publish.go.
package store

import "sync"

// Reader avoids decoding the same snapshot for every request in a viewer.
// It retains at most one project prefix and is safe for concurrent use.
type Reader struct {
	mu    sync.Mutex
	state *State
}

func (r *Reader) Load(p Project) (State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if off, err := cacheOff(); err != nil {
		return State{}, err
	} else if off {
		return Replayed(p)
	}
	state, moved, err := load(p, r.state)
	if err != nil {
		return Replayed(p)
	}
	if moved {
		_ = saveState(state)
	}
	// Raw bundles can be large and are mutable through Bundles. Only the
	// immutable snapshot and selected byte hashes are shared between reads.
	cached := state
	cached.bundles = nil
	r.state = &cached
	return state, nil
}
