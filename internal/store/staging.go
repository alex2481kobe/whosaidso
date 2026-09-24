package store

// Where an in-progress run writes its outputs lives here. What a run captures,
// when its staging is retired, and what admission accepts do not.

import (
	"os"
	"path/filepath"

	"whosaidso/internal/model"
)

// MakeRunStaging creates the fresh, private directory one invocation writes
// its outputs into while it runs, and returns its absolute path.
//
// Staging is per-machine working state, never a record, so it lives in the
// per-machine WhoSaidSo home beside intake (<Home>/staging/<project>/<id>), outside
// the project and its committed artifact store. Home is the one function that
// places intake, staging, the machine id and the registry, so they can never
// be relocated apart. A run's bytes reach
// committed storage only through capture and admission, and only once.
//
// The staging directories below the WhoSaidSo home are created here and must be
// real directories: a symlink there would turn a local capture into a write
// somewhere else. The invocation's own directory must not exist yet.
func MakeRunStaging(project Project, invocation model.ID) (string, error) {
	if !model.ValidID(invocation) {
		return "", storeFault("invalid-field", "invocation_id", "not a ULID")
	}
	encoded, err := encodeProjectID(project.ID)
	if err != nil {
		return "", err
	}
	home, err := Home()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		return "", storeFault("io", home, err.Error())
	}
	dir := home
	parts := []string{"staging", encoded, string(invocation)}
	for i, part := range parts {
		dir = filepath.Join(dir, part)
		err := os.Mkdir(dir, 0700)
		if err != nil && (!os.IsExist(err) || i == len(parts)-1) {
			return "", storeFault("io", dir, err.Error())
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return "", storeFault("io", dir, err.Error())
		}
		if !info.IsDir() {
			return "", storeFault("invalid-field", dir, "run staging directories cannot be symlinks")
		}
	}
	return dir, nil
}
