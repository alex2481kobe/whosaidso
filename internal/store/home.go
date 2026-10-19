package store

// The per-machine WhoSaidSo home and the project registry live here: where the
// home is, and how a registry entry is read. The registry names, per project,
// the checkout the viewer opens when run outside any checkout; it routes no
// read or write, which always use the invoking checkout (Discover). Changing
// an entry lives in home_bind.go; intake, staging and the machine id only ask
// Home where they belong.

import (
	"context"
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

// GalleryFile is the project's gallery on this machine (internal/gallery):
// in the home beside its registry binding, so it is never in a ledger and
// never inside a repository, whatever that repository ignores.
func GalleryFile(id model.ProjectID) (string, error) {
	encoded, err := encodeProjectID(id)
	if err != nil {
		return "", err
	}
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "gallery", encoded+".json"), nil
}

// HoldDirLock waits for the exclusive lock of dir, the same lock admission
// and binding take on theirs (holdAdmissionLock), across processes, and
// returns its release. The gallery serializes its read-modify-rename with it.
func HoldDirLock(ctx context.Context, dir string) (release func() error, err error) {
	f, err := holdAdmissionLock(ctx, dir)
	if err != nil {
		return nil, err
	}
	return func() error { return lockRelease(f) }, nil
}

// codeBindingCorrupt is a registry entry that names no home. Bind replaces
// it: only an explicit binding can say where the home is.
const codeBindingCorrupt = "binding-corrupt"

// Binding returns the checkout this machine's registry names for the project,
// and false when it names none. A binding file that is not exactly one absolute,
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

// homeAt opens the registered root as a project, refusing when it is gone or
// when its config no longer declares id.
func homeAt(root string, id model.ProjectID) (Project, error) {
	unavailable := func(why string) error {
		return storeFault("home-unavailable", root, fmt.Sprintf(
			"checkout %s registered for project %s is unavailable: %s; register the checkout to open with whosaidso home PATH", root, id, why))
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
