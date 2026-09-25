package store

// The validated snapshot loader lives here: selecting the ledger prefix,
// binding it byte for byte to a cached image, and folding only the new tail.
// The image's files are cache.go's; the fold and its codec are reduce's.
//
// The ledger is the only authority. An image is reused only when the prefix it
// was folded from matches the selected prefix in every file name and every
// file's content hash, not merely in its head: a bundle's predecessor names a
// command id, not a digest, so an earlier bundle can change while the head
// stays byte-identical. Every command therefore still reads and hashes every
// bundle; what it skips is decoding and replaying the ones the image already
// folded. Any mismatch or damage rebuilds from the ledger, and any failure on
// the fast path falls back to the full read, so an error is always the one a
// full replay reports and never a cached answer.

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
)

// State is one selected ledger prefix and the snapshot reduced from it. Its
// raw bundles are available on demand, re-read and checked against the
// hashes taken at selection, so they are the same prefix or an error.
type State struct {
	project  Project
	snapshot reduce.Snapshot
	files    []selectedFile
	bundles  []model.Bundle
	restored int
	// foldErr is set only on an admission's prefix that reads but does not
	// fold; Folded reports it, and no read ever answers from such a state.
	foldErr error
}

type selectedFile struct {
	ledgerFile
	hash [32]byte
}

// Snapshot is the reduced state of exactly this prefix.
func (s State) Snapshot() reduce.Snapshot { return s.snapshot }

// Folded is the snapshot, or the error folding this prefix gave. Only an
// admission's prefix can carry one: the store sequences a ledger it can read,
// and leaves semantic refusal to the caller.
func (s State) Folded() (reduce.Snapshot, error) { return s.snapshot, s.foldErr }

// Restored is how many of the prefix's bundles came from a cached image
// rather than being folded in this process: zero on a rebuild.
func (s State) Restored() int { return s.restored }

// Bundles returns the prefix's bundles in sequence order.
func (s State) Bundles() ([]model.Bundle, error) {
	if s.bundles != nil {
		return s.bundles, nil
	}
	out := make([]model.Bundle, 0, len(s.files))
	for i := range s.files {
		data, err := os.ReadFile(filepath.Join(s.project.Ledger, s.files[i].name))
		if err != nil {
			return nil, storeFault("io", filepath.Join(s.project.Ledger, s.files[i].name), err.Error())
		}
		if sha256.Sum256(data) != s.files[i].hash {
			return nil, storeFault("ledger-corrupt", filepath.Join(s.project.Ledger, s.files[i].name),
				"the bundle changed after this read selected it")
		}
		b, err := decodeSelected(s.project, s.files, i, data)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

// Replayed reads and folds the whole prefix, and neither reads nor writes the
// cache. It is the reference Load must always agree with.
func Replayed(project Project) (State, error) {
	bundles, err := readLedger(project)
	if err != nil {
		return State{}, err
	}
	snapshot, err := reduce.Replay(bundles)
	if err != nil {
		return State{}, err
	}
	return State{project: project, snapshot: snapshot, bundles: bundles}, nil
}

// NoCacheEnv set to exactly "1" makes Load the full ledger read (Replayed):
// the snapshot cache is neither read nor written. It is the owner's check
// against a deliberately rewritten cache, which the image checksum does not
// detect (blind spot: an image rewritten with a recomputed checksum is
// believed on the default path). Any other non-empty value is refused.
const NoCacheEnv = "WHOSAIDSO_NO_CACHE"

// cacheOff reports whether NoCacheEnv turns the cache off, or refuses a value
// it does not define rather than guessing what was meant.
func cacheOff() (bool, error) {
	switch v := os.Getenv(NoCacheEnv); v {
	case "":
		return false, nil
	case "1":
		return true, nil
	default:
		return true, storeFault("invalid-field", NoCacheEnv, fmt.Sprintf(
			"%s must be 1 (read the whole ledger, never the cache) or unset, got %q", NoCacheEnv, v))
	}
}

// Load selects the ledger prefix and returns its snapshot, restoring the
// cached image and folding only the bundles after it when the image matches.
// When the snapshot had to move, it publishes a new image, best effort: a
// failed publication changes nothing but the next command's speed. With
// NoCacheEnv=1 it is Replayed and touches no image.
func Load(project Project) (State, error) {
	if off, err := cacheOff(); err != nil {
		return State{}, err
	} else if off {
		return Replayed(project)
	}
	state, moved, err := load(project)
	if err != nil {
		// Whatever went wrong, the full read decides: it returns the answer,
		// or the error, that the ledger alone gives.
		return Replayed(project)
	}
	if moved {
		_ = saveState(state)
	}
	return state, nil
}

func load(project Project) (State, bool, error) {
	files, err := inventory(project)
	if err != nil {
		return State{}, false, err
	}
	image, usable := readImage(project, cacheVersion())
	if usable && image.count > len(files) {
		usable = false // the ledger lost bundles the image folded: never resurrect them
	}
	selected := make([]selectedFile, len(files))
	data := make([][]byte, len(files))
	chain := chainStart()
	if usable && image.count == 0 && chain != image.chain {
		usable = false
	}
	for i, f := range files {
		if f.sequence != uint64(i)+1 {
			return State{}, false, errImage
		}
		raw, err := os.ReadFile(filepath.Join(project.Ledger, f.name))
		if err != nil {
			return State{}, false, err
		}
		selected[i] = selectedFile{ledgerFile: f, hash: sha256.Sum256(raw)}
		chain = chainLink(chain, f.name, selected[i].hash)
		if !usable || i >= image.count {
			data[i] = raw // decoded below; a prefix the image covers is not
		}
		if usable && i+1 == image.count && chain != image.chain {
			usable = false
		}
	}
	state := State{project: project, files: selected}
	if usable {
		tail, err := decodeRange(project, selected, data, image.count)
		if err != nil {
			return State{}, false, err
		}
		snapshot, err := reduce.RestoreSnapshot(image.state, tail)
		if err == nil && bound(snapshot, project, selected) {
			state.snapshot, state.restored = snapshot, image.count
			return state, len(tail) > 0, nil
		}
	}
	// No usable image: fold the whole prefix from the bytes just hashed.
	bundles, err := decodeRange(project, selected, data, 0)
	if err != nil {
		return State{}, false, err
	}
	if state.snapshot, err = reduce.Replay(bundles); err != nil {
		return State{}, false, err
	}
	state.bundles = bundles
	return state, true, nil
}

// bound checks that a restored snapshot answers for exactly the selected
// prefix: its project, its length and its head.
func bound(s reduce.Snapshot, project Project, files []selectedFile) bool {
	w := s.Watermark()
	if len(files) == 0 {
		return w.Sequence == 0 && s.Project() == ""
	}
	last := files[len(files)-1]
	return s.Project() == project.ID && w.Sequence == last.sequence && w.CommandID == last.command
}

// decodeRange decodes files[from:], re-reading any whose bytes were not kept
// and checking them against the selection's hashes.
func decodeRange(project Project, files []selectedFile, data [][]byte, from int) ([]model.Bundle, error) {
	out := make([]model.Bundle, 0, len(files)-from)
	for i := from; i < len(files); i++ {
		raw := data[i]
		if raw == nil {
			var err error
			if raw, err = os.ReadFile(filepath.Join(project.Ledger, files[i].name)); err != nil {
				return nil, err
			}
			if sha256.Sum256(raw) != files[i].hash {
				return nil, errImage
			}
		}
		b, err := decodeSelected(project, files, i, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

// decodeSelected makes readLedger's per-bundle checks against the selection:
// the body agrees with its filename and project, and names the preceding
// file's command as predecessor. The reducer then refuses a repeated command.
func decodeSelected(project Project, files []selectedFile, i int, raw []byte) (model.Bundle, error) {
	path := filepath.Join(project.Ledger, files[i].name)
	b, err := model.DecodeBundle(raw)
	if err != nil {
		return model.Bundle{}, storeFault("ledger-corrupt", path, err.Error())
	}
	if b.Sequence != files[i].sequence || b.CommandID != files[i].command || b.Project != project.ID {
		return model.Bundle{}, storeFault("ledger-corrupt", path, "the bundle body disagrees with its filename or project")
	}
	if i > 0 && b.Predecessor != files[i-1].command {
		return model.Bundle{}, storeFault("ledger-discontinuity", path, "predecessor does not name the preceding bundle")
	}
	return b, nil
}

// The prefix digest chains every file's name and content hash in sequence
// order, so equal digests at a length mean equal prefixes byte for byte.
func chainStart() [32]byte { return sha256.Sum256([]byte("whosaidso-ledger-prefix/1")) }

func chainLink(prev [32]byte, name string, hash [32]byte) [32]byte {
	buf := make([]byte, 0, len(prev)+len(name)+1+len(hash))
	buf = append(append(append(append(buf, prev[:]...), name...), 0), hash[:]...)
	return sha256.Sum256(buf)
}

// saveState publishes the state's snapshot as the cache image for its prefix.
func saveState(s State) error {
	version := cacheVersion()
	if version == "" || s.files == nil && s.bundles != nil {
		return errImage // a Replayed state carries no hashes to bind an image to
	}
	encoded, err := reduce.EncodeSnapshot(s.snapshot)
	if err != nil {
		return err
	}
	chain := chainStart()
	for _, f := range s.files {
		chain = chainLink(chain, f.name, f.hash)
	}
	return publishImage(s.project, cacheImage{version: version, project: s.project.ID, count: len(s.files), chain: chain, state: encoded})
}
