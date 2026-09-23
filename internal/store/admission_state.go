package store

// What an admission needs from its selected prefix lives here: the head it
// seals against, a bundle already published under its id, and the state after
// its own bundle for the cache. Selecting and validating the prefix is
// snapshot.go's; the transaction itself is publish.go's.

import (
	"crypto/sha256"
	"os"
	"path/filepath"

	"datum/internal/model"
	"datum/internal/reduce"
)

type selectedHead struct {
	sequence uint64
	command  model.ID
}

// head is the last selected bundle's sequence and command, zero when empty.
func (s State) head() selectedHead {
	if s.files != nil {
		if len(s.files) == 0 {
			return selectedHead{}
		}
		last := s.files[len(s.files)-1]
		return selectedHead{last.sequence, last.command}
	}
	if len(s.bundles) == 0 {
		return selectedHead{}
	}
	last := s.bundles[len(s.bundles)-1]
	return selectedHead{last.Sequence, last.CommandID}
}

// published returns the bundle already admitted under a command id, if any,
// from the selected prefix.
func (s State) published(id model.ID) (model.Bundle, bool, error) {
	if s.files == nil {
		for _, b := range s.bundles {
			if b.CommandID == id {
				return b, true, nil
			}
		}
		return model.Bundle{}, false, nil
	}
	for i := range s.files {
		if s.files[i].command != id {
			continue
		}
		path := filepath.Join(s.project.Ledger, s.files[i].name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return model.Bundle{}, false, storeFault("io", path, err.Error())
		}
		if sha256.Sum256(raw) != s.files[i].hash {
			return model.Bundle{}, false, storeFault("ledger-corrupt", path, "the bundle changed after this admission selected it")
		}
		b, err := decodeSelected(s.project, s.files, i, raw)
		return b, err == nil, err
	}
	return model.Bundle{}, false, nil
}

// extended is this state after the bundle its admission just published, for
// the cache, or nil when it cannot be bound to that file. It folds the bytes
// on disk, exactly what a later replay reads, and runs under the admission
// lock, so no other writer can have moved the ledger in between.
func (s State) extended(b model.Bundle) *State {
	if s.files == nil || s.foldErr != nil {
		return nil
	}
	name, err := model.BundleName(b.Sequence, b.CommandID)
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(s.project.Ledger, name))
	if err != nil {
		return nil
	}
	files := append(append(make([]selectedFile, 0, len(s.files)+1), s.files...),
		selectedFile{ledgerFile: ledgerFile{sequence: b.Sequence, command: b.CommandID, name: name}, hash: sha256.Sum256(raw)})
	published, err := decodeSelected(s.project, files, len(files)-1, raw)
	if err != nil {
		return nil
	}
	next, err := reduce.Apply(s.snapshot, published)
	if err != nil {
		return nil
	}
	return &State{project: s.project, snapshot: next, files: files}
}
