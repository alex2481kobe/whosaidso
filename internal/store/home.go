package store

// The per-machine WhoSaidSo home and the project registry live here: where the
// home is, how a project's binding is read, and how a command opens a project
// through its bound home. Changing a binding lives in home_bind.go; intake,
// staging and the machine id only ask Home where they belong.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// HomeEnv overrides the whole per-machine home: registry, intake, staging and
// machine id move together, so a rehearsal cannot touch one real store.
const HomeEnv = "WHOSAIDSO_HOME"

// Home is the per-machine WhoSaidSo home: $WHOSAIDSO_HOME when set, else ~/.whosaidso.
// Every per-machine path is derived from it and from nothing else.
func Home() (string, error) {
	if dir := os.Getenv(HomeEnv); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", storeFault("invalid-field", HomeEnv, fmt.Sprintf("%s must be an absolute path, got %q", HomeEnv, dir))
		}
		return filepath.Clean(dir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return "", storeFault("io", "home", "an absolute user home directory is required")
	}
	return filepath.Join(home, ".whosaidso"), nil
}

// bindingPath is the registry file naming one project's home.
func bindingPath(id model.ProjectID) (string, error) {
	encoded, err := encodeProjectID(id)
	if err != nil {
		return "", err
	}
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "projects", encoded), nil
}

// codeBindingCorrupt is a registry entry that names no home. Bind replaces
// it: only an explicit binding can say where the home is.
const codeBindingCorrupt = "binding-corrupt"

// Binding returns the canonical root this machine binds the project to, and
// false when it is unbound. A binding file that is not exactly one absolute,
// clean path and a newline is an error, never read as unbound.
func Binding(id model.ProjectID) (string, bool, error) {
	path, err := bindingPath(id)
	if err != nil {
		return "", false, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, storeFault("io", path, err.Error())
	}
	if !info.Mode().IsRegular() {
		return "", false, storeFault(codeBindingCorrupt, path, "a project binding must be a regular file, never a symlink; fix it with whosaidso home PATH")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false, storeFault("io", path, err.Error())
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 1<<16))
	if err != nil {
		return "", false, storeFault("io", path, err.Error())
	}
	root, ok := strings.CutSuffix(string(raw), "\n")
	if !ok || root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root || strings.ContainsAny(root, "\x00\n") {
		return "", false, storeFault(codeBindingCorrupt, path, fmt.Sprintf("expected one absolute clean path and a newline, found %q; fix it with whosaidso home PATH", raw))
	}
	return root, true, nil
}

// Open is the project a command reads and admits through: the nearest config
// above cwd names the project, and this machine's registry names its home.
//
// An unbound project refuses, so no clone silently becomes the home by running
// first. A bound home that is missing, or no longer declares this project,
// refuses too: WhoSaidSo never falls back to the invoking checkout's ledger and
// never creates an empty one. Checkout stays the invoking checkout.
func Open(cwd string) (Project, error) {
	invoked, err := Discover(cwd)
	if err != nil {
		return Project{}, err
	}
	root, bound, err := Binding(invoked.ID)
	if err != nil {
		return Project{}, err
	}
	if !bound {
		return Project{}, storeFault("home-unbound", invoked.Root, fmt.Sprintf(
			"project %s is not bound to a home on this machine; run whosaidso home PATH with the checkout that holds its live ledger", invoked.ID))
	}
	home, err := homeAt(root, invoked.ID)
	if err != nil {
		return Project{}, err
	}
	if real, err := filepath.EvalSymlinks(invoked.Root); err == nil && real == root {
		// The home is the invoking checkout: keep its own spelling, so nothing
		// changes for a project used from one place.
		home = invoked
	}
	home.Checkout, home.bound = invoked.Root, true
	return home, nil
}

// homeAt opens the bound root as a project, refusing when it is gone or when
// its config no longer declares id.
func homeAt(root string, id model.ProjectID) (Project, error) {
	unavailable := func(why string) error {
		return storeFault("home-unavailable", root, fmt.Sprintf(
			"home %s of project %s is unavailable: %s; WhoSaidSo does not fall back to another ledger or create one; restore it or bind the new place with whosaidso home PATH", root, id, why))
	}
	info, err := os.Stat(root)
	if err != nil {
		return Project{}, unavailable(err.Error())
	}
	if !info.IsDir() {
		return Project{}, unavailable("not a directory")
	}
	home, err := configAt(root)
	if os.IsNotExist(err) {
		return Project{}, unavailable("it has no whosaidso.toml")
	}
	if err != nil {
		return Project{}, err
	}
	if home.ID != id {
		return Project{}, unavailable(fmt.Sprintf("its whosaidso.toml declares project %s", home.ID))
	}
	return home, nil
}

// recheckHome is admission's check that a registered project is still bound
// where it was opened. It runs once before the ledger directory is touched,
// so a deleted home is never recreated, and again under the ledger lock,
// which is what serializes it with rebinding (Bind holds the old home's lock
// while it switches the binding).
func recheckHome(project Project) error {
	if !project.bound {
		return nil
	}
	root, bound, err := Binding(project.ID)
	if err != nil {
		return err
	}
	now := "unbound"
	var current Project
	if bound {
		if current, err = homeAt(root, project.ID); err != nil {
			return err
		}
		now = "bound to " + root
	}
	if real, err := filepath.EvalSymlinks(project.Root); !bound || err != nil || real != root {
		return storeFault("home-moved", project.Root, fmt.Sprintf(
			"project %s was opened with home %s and is now %s; nothing was published; run the command again", project.ID, project.Root, now))
	}
	// The same home can name another ledger since it was opened. Publishing
	// into the one opened would acknowledge a fact the configured ledger never
	// holds, so the ledger the config names now must be the one being written.
	opened, openedErr := filepath.Rel(project.Root, project.Ledger)
	named, namedErr := filepath.Rel(current.Root, current.Ledger)
	if openedErr != nil || namedErr != nil || opened != named {
		return storeFault("home-moved", project.Root, fmt.Sprintf(
			"project %s was opened with ledger %s and its whosaidso.toml now names %s; nothing was published; run the command again", project.ID, opened, named))
	}
	return nil
}
