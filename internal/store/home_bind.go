package store

// Changing a registry entry lives here: naming the checkout the viewer opens
// for a project. The entry routes no read or write; it is checked only so the
// viewer is never pointed at a checkout of another project or at no checkout.
// Reading an entry lives in home.go, listing them in home_list.go.

import (
	"context"
	"os"
	"path/filepath"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// BindResult is what one registration did.
type BindResult struct {
	Project  Project
	Previous string // the checkout registered before, "" when none
	Bundles  int    // bundles in the registered checkout's ledger
}

// Bind registers target as the checkout this machine's viewer opens for the
// project declared nearest cwd. target must hold whosaidso.toml itself,
// declaring the same project id, with a ledger that reads. Registrations
// serialize on the registry lock.
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
	bundles, err := readLedger(dest)
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
	previous, _, err := Binding(invoked.ID)
	if fault, ok := err.(*model.Fault); ok && fault.Code == codeBindingCorrupt {
		// An entry that names no checkout is replaced by the explicit one.
		previous, err = "", nil
	}
	if err != nil {
		return BindResult{}, err
	}
	result := BindResult{Project: dest, Previous: previous, Bundles: len(bundles)}
	if previous != root {
		if err := writeBinding(path, root); err != nil {
			return BindResult{}, err
		}
	}
	return result, nil
}

func bindRefusal(root string, err error) error {
	return storeFault("home-invalid", root, "cannot register "+root+": "+err.Error())
}

// writeBinding replaces the registry entry atomically and flushes the registry.
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
