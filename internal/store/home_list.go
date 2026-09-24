package store

// Listing the whole registry lives here: every project this machine's home
// binds, each opened through its binding exactly as Open would, or kept with
// the reason it cannot be opened. Reading one binding and opening a project
// from a checkout live in home.go; changing a binding in home_bind.go.

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"whosaidso/internal/model"
)

// Registration is one registry entry: the project id its file name encodes,
// the home it names, and that home opened as a Project, or the reason it
// could not be. An entry that cannot be read is listed, never skipped, so a
// broken binding is visible rather than silently absent.
type Registration struct {
	ID      model.ProjectID
	Home    string  // "" when the entry names no readable home
	Project Project // set only when Reason is ""
	Reason  string  // why the home is unavailable; "" when it opened
}

// Registered lists every entry of the home's projects/ registry, sorted by
// project id. A home with no registry yet lists nothing. Entries whose name
// starts with "." are a binding still being written (home_bind.go), not a
// binding.
func Registered() ([]Registration, error) {
	home, err := Home()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, "projects")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []Registration{}, nil
	}
	if err != nil {
		return nil, storeFault("io", dir, err.Error())
	}
	out := []Registration{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		out = append(out, registration(entry.Name()))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// registration opens one entry by its file name: the name must be the
// lowercase hex of a project id (encodeProjectID), the file one binding, and
// the home it names a directory whose config still declares that id.
func registration(name string) Registration {
	raw, err := hex.DecodeString(name)
	if err != nil || hex.EncodeToString(raw) != name || len(raw) == 0 || !utf8.Valid(raw) {
		return Registration{ID: model.ProjectID(name), Reason: "registry entry " + name + " is not the lowercase hex of a project id"}
	}
	r := Registration{ID: model.ProjectID(raw)}
	root, bound, err := Binding(r.ID)
	switch {
	case err != nil:
		r.Reason = err.Error()
		return r
	case !bound:
		r.Reason = "the binding disappeared while it was listed"
		return r
	}
	r.Home = root
	if r.Project, err = homeAt(root, r.ID); err != nil {
		r.Project, r.Reason = Project{}, err.Error()
	}
	return r
}
