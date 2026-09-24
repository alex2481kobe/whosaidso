package store

// Binding a project to its home lives here: the first binding and the checked
// relocation, which are the same act. Where the home is and how commands open
// a project through it live in home.go.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Continuity says what relocation could establish about the new home.
const (
	ContinuityNew         = "new"          // the project was unbound
	ContinuitySame        = "same"         // already bound here; nothing changed
	ContinuityContinued   = "continued"    // the new history continues the old
	ContinuityNotCompared = "not-compared" // the old home is gone
)

// BindResult is what one binding did.
type BindResult struct {
	Project    Project
	Previous   string // the root bound before, "" when unbound
	Continuity string
	Bundles    int // bundles in the bound home's ledger
}

// Bind binds the project declared nearest cwd to target, which must hold
// whosaidso.toml itself. Binding again elsewhere is the relocation, with its
// checks: the destination declares the same project id, confines its ledger
// and reads as a complete history; while the old home still exists, the
// destination's history must continue it bundle for bundle. When the old home
// is gone, continuity is not compared and says so.
//
// Every binding change holds the registry lock, so two binds serialize, and a
// relocation also holds the old home's ledger lock, so no admission lands in
// the old home after its history was compared. Admission re-checks the binding
// under that same lock (recheckHome).
func Bind(ctx context.Context, cwd, target string) (BindResult, error) {
	invoked, err := Discover(cwd)
	if err != nil {
		return BindResult{}, err
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return BindResult{}, storeFault("io", target, err.Error())
	}
	root, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return BindResult{}, storeFault("home-invalid", abs, err.Error())
	}
	dest, err := homeAt(root, invoked.ID)
	if err != nil {
		return BindResult{}, bindRefusal(root, err)
	}
	path, err := bindingPath(invoked.ID)
	if err != nil {
		return BindResult{}, err
	}
	registry := filepath.Dir(path)
	if err := os.MkdirAll(registry, 0o700); err != nil {
		return BindResult{}, storeFault("io", registry, err.Error())
	}
	lock, err := holdAdmissionLock(ctx, registry)
	if err != nil {
		return BindResult{}, err
	}
	defer lockRelease(lock)

	previous, bound, err := Binding(invoked.ID)
	if err != nil {
		return BindResult{}, err
	}
	result := BindResult{Project: dest, Previous: previous, Continuity: ContinuityNew}
	switch {
	case bound && previous == root:
		result.Continuity = ContinuitySame
	case bound:
		if _, err := os.Lstat(previous); os.IsNotExist(err) {
			result.Continuity = ContinuityNotCompared
			break
		}
		old, err := homeAt(previous, invoked.ID)
		if err != nil {
			return BindResult{}, storeFault("home-continuity", previous, fmt.Sprintf(
				"the old home %s still exists but cannot be read (%v), so continuity cannot be compared; restore it, or move it away if it is abandoned, then bind again", previous, err))
		}
		// Hold the old home's ledger lock through the switch: an admission
		// either publishes before the comparison sees it, or re-checks the
		// binding after the switch and refuses.
		if err := ensureLedgerDir(old.Ledger, systemPublishIO()); err != nil {
			return BindResult{}, err
		}
		oldLock, err := holdAdmissionLock(ctx, old.Ledger)
		if err != nil {
			return BindResult{}, err
		}
		defer lockRelease(oldLock)
		if err := continues(old, dest); err != nil {
			return BindResult{}, err
		}
		result.Continuity = ContinuityContinued
	}
	bundles, err := readLedger(dest)
	if err != nil {
		return BindResult{}, bindRefusal(root, err)
	}
	result.Bundles = len(bundles)
	if result.Continuity != ContinuitySame {
		if err := writeBinding(path, root); err != nil {
			return BindResult{}, err
		}
	}
	return result, nil
}

func bindRefusal(root string, err error) error {
	return storeFault("home-invalid", root, fmt.Sprintf("cannot bind %s: %v", root, err))
}

// continues refuses unless every bundle of old is in dest, byte for byte, at
// the same name: dest's history is old's with only admissions after it.
func continues(old, dest Project) error {
	if _, err := readLedger(old); err != nil {
		return storeFault("home-continuity", old.Ledger, fmt.Sprintf("the old home's history cannot be read, so continuity cannot be compared: %v", err))
	}
	if _, err := readLedger(dest); err != nil {
		return bindRefusal(dest.Root, err)
	}
	oldFiles, err := inventory(old)
	if err != nil {
		return err
	}
	destFiles, err := inventory(dest)
	if err != nil {
		return err
	}
	broken := func(why string) error {
		return storeFault("home-continuity", dest.Ledger, fmt.Sprintf(
			"%s does not continue the history at %s: %s; the binding is unchanged", dest.Root, old.Root, why))
	}
	if len(destFiles) < len(oldFiles) {
		return broken(fmt.Sprintf("it has %d bundles and the old home has %d", len(destFiles), len(oldFiles)))
	}
	for i, file := range oldFiles {
		if destFiles[i].name != file.name {
			return broken(fmt.Sprintf("bundle %d is %s here and %s there", i+1, destFiles[i].name, file.name))
		}
		a, err := os.ReadFile(filepath.Join(old.Ledger, file.name))
		if err != nil {
			return storeFault("io", old.Ledger, err.Error())
		}
		b, err := os.ReadFile(filepath.Join(dest.Ledger, file.name))
		if err != nil {
			return storeFault("io", dest.Ledger, err.Error())
		}
		if !bytes.Equal(a, b) {
			return broken("bundle " + file.name + " differs")
		}
	}
	return nil
}

// writeBinding replaces the binding file atomically and flushes the registry.
func writeBinding(path, root string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".binding-*.tmp")
	if err != nil {
		return storeFault("io", dir, err.Error())
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.WriteString(root + "\n")
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		return storeFault("io", path, err.Error())
	}
	return syncLedgerPath(dir, systemPublishIO())
}
