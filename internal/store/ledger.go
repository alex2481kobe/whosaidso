package store

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"datum/internal/model"
)

// lockName is the admission lock's file, inside the ledger directory so one
// project's writers contend with each other and with nobody else.
const lockName = ".lock"

// unsafeLock refuses a lock path that is not a regular file of its own, such as
// a symlink that would carry the lock, and its creation, out of the datum root.
func unsafeLock(path string) error {
	return storeFault("ledger-corrupt", path,
		"the admission lock must be a regular file in the ledger directory, never a symlink; remove it and retry")
}

// publicationSuffix marks a ledger publication in progress. Only this
// publisher creates such a file, and only recovery under the admission lock
// removes one.
const publicationSuffix = ".tmp"

// ReadPrefix returns the ledger's admitted bundles in sequence order.
//
// It takes no lock. A published bundle is immutable and is never removed, and
// publication is a single rename, so any directory listing a reader can observe
// is a valid ledger. Holding the admission lock here would add contention and
// buy nothing on an already immutable snapshot.
func ReadPrefix(project Project) ([]model.Bundle, error) {
	return readLedger(project)
}

// ledgerFile is what a filename CLAIMS. The body has to agree before any of it
// is believed.
type ledgerFile struct {
	sequence uint64
	command  model.ID
	name     string
}

// inventory enumerates the ledger exactly once and returns its published
// bundle files sorted by the sequence their names claim. It refuses every entry
// a reader cannot account for and every doubly published sequence; it reads no
// bundle, so gaps and bodies are the caller's to check, in sequence order.
func inventory(project Project) ([]ledgerFile, error) {
	if project.ID == "" {
		return nil, storeFault("invalid-field", "project.id", "a ledger read needs the declared project id to check what it reads")
	}
	dir := project.Ledger
	if dir == "" {
		return nil, storeFault("invalid-field", "project.ledger", "empty ledger path")
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		// A project that has admitted nothing has no ledger directory yet. That
		// is an empty ledger, not a broken one. It is also indistinguishable
		// from a deleted ledger, which is why the first admission recreates the
		// directory rather than trusting it to exist.
		return []ledgerFile{}, nil
	}
	if err != nil {
		return nil, storeFault("io", dir, err.Error())
	}
	files := make([]ledgerFile, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(dir, name)
		if strings.HasPrefix(name, ".") {
			// A bundle filename starts with a digit, so no admitted fact can
			// hide in a dotfile. The lock lives here, and so do the files an
			// editor or a file browser leaves behind.
			continue
		}
		if _, _, ok := parsePublicationTemp(name); ok {
			if !entry.Type().IsRegular() {
				// Recovery removes regular files only, so a directory wearing a
				// temporary's name would be ignored by everything, forever.
				return nil, storeFault("ledger-corrupt", path,
					"an interrupted publication must be a regular file")
			}
			// An interrupted publication. It is not a record and it is not read.
			// Removing it is recovery's job, under the admission lock.
			continue
		}
		sequence, command, ok := parseBundleName(name)
		if !ok {
			// Skipping an entry the reader cannot account for is how a ledger
			// quietly loses a record, so an unaccountable entry stops the read.
			return nil, storeFault("ledger-corrupt", path,
				"entry is neither a published bundle nor an interrupted publication")
		}
		if !entry.Type().IsRegular() {
			return nil, storeFault("ledger-corrupt", path,
				"a published bundle must be a regular file, never a symlink or a directory")
		}
		files = append(files, ledgerFile{sequence: sequence, command: command, name: name})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].sequence != files[j].sequence {
			return files[i].sequence < files[j].sequence
		}
		return files[i].name < files[j].name
	})
	for i := 1; i < len(files); i++ {
		if files[i].sequence == files[i-1].sequence {
			// The exact race the lock exists to prevent: two writers read the
			// same tail and both published, under different filenames, so
			// neither overwrote the other. Name both files and refuse.
			return nil, storeFault("ledger-fork", filepath.Join(dir, files[i].name),
				"sequence "+strconv.FormatUint(files[i].sequence, 10)+" is published twice, also as "+files[i-1].name)
		}
	}
	return files, nil
}

// readLedger enumerates the ledger exactly once, selects the complete prefix
// and validates it.
//
// Enumeration order decides nothing. Filesystems return directory entries in
// whatever order suits their on-disk structure, so the sequence number in each
// filename is sorted numerically and then checked against the body. Two files
// claiming one sequence is a fork, which is refused by name rather than
// resolved by picking whichever the filesystem happened to hand back first.
func readLedger(project Project) ([]model.Bundle, error) {
	files, err := inventory(project)
	if err != nil {
		return nil, err
	}
	dir := project.Ledger
	bundles := make([]model.Bundle, 0, len(files))
	commands := make(map[model.ID]int, len(files))
	for i, file := range files {
		path := filepath.Join(dir, file.name)
		if file.sequence != uint64(i)+1 {
			// Nothing legitimate creates a hole. Returning the bundles before it
			// would answer "what is admitted" with a true count of the wrong
			// thing, so the gap is reported instead of truncated away.
			return nil, storeFault("ledger-discontinuity", path,
				"sequence "+strconv.FormatUint(uint64(i)+1, 10)+" is missing")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, storeFault("io", path, err.Error())
		}
		bundle, err := model.DecodeBundle(data)
		if err != nil {
			// A bundle that will not decode is a ledger error. Continuing past
			// it would replay a ledger that is missing one of its transactions
			// and report the result as complete.
			return nil, storeFault("ledger-corrupt", path, err.Error())
		}
		if bundle.Sequence != file.sequence || bundle.CommandID != file.command {
			return nil, storeFault("ledger-corrupt", path,
				"the filename and the bundle body claim different identities")
		}
		if bundle.Project != project.ID {
			return nil, storeFault("ledger-corrupt", path,
				"bundle declares project "+strconv.Quote(string(bundle.Project))+", which is not this ledger's project")
		}
		if i > 0 && bundle.Predecessor != bundles[i-1].CommandID {
			// Sequence alone only counts files. The predecessor is what makes
			// the chain say which bundle each one was actually appended to.
			return nil, storeFault("ledger-discontinuity", path,
				"predecessor does not name the preceding bundle")
		}
		if prior, exists := commands[bundle.CommandID]; exists {
			requests := "matching request digests"
			if bundles[prior].RequestDigest != bundle.RequestDigest {
				requests = "different request digests"
			}
			return nil, storeFault("ledger-corrupt", path,
				"command id "+string(bundle.CommandID)+" is published at sequences "+
					strconv.FormatUint(bundles[prior].Sequence, 10)+" and "+strconv.FormatUint(bundle.Sequence, 10)+
					" with "+requests+"; admission identity is ambiguous")
		}
		commands[bundle.CommandID] = i
		bundles = append(bundles, bundle)
	}
	return bundles, nil
}

// parseBundleName splits a published ledger filename into the identity it
// claims: zero-padded sequence, transaction id, ".json".
func parseBundleName(name string) (uint64, model.ID, bool) {
	rest, ok := strings.CutSuffix(name, ".json")
	if !ok {
		return 0, "", false
	}
	digits, id, ok := strings.Cut(rest, "-")
	if !ok || len(digits) != 8 {
		return 0, "", false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, "", false
		}
	}
	sequence, err := strconv.ParseUint(digits, 10, 64)
	if err != nil || sequence == 0 {
		return 0, "", false
	}
	if !model.ValidID(model.ID(id)) {
		return 0, "", false
	}
	return sequence, model.ID(id), true
}

// parsePublicationTemp recognizes this publisher's own interrupted work, and
// only that. A temporary with any other name belongs to somebody else.
func parsePublicationTemp(name string) (uint64, model.ID, bool) {
	rest, ok := strings.CutSuffix(name, publicationSuffix)
	if !ok {
		return 0, "", false
	}
	return parseBundleName(rest)
}
