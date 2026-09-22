package evidence

// Declared path roles, collection, and project-relative path checks live here.
// Fetching bytes and binding invocation outputs to selectors do not.
// This file stays below 200 lines because path handling is a complete responsibility.

import (
	"fmt"
	"sort"
	"strings"

	"datum/internal/model"
)

// ---- paths ---------------------------------------------------------------

// PathRole keeps the three path families apart. An evidence PNG under an output
// directory is where a result was written, not the component under test, and
// merging the two turns an output location into a false topic claim.
type PathRole string

const (
	SourcePath     PathRole = "source"
	ArtifactPath   PathRole = "artifact"
	ConstraintPath PathRole = "constraint"
)

// RoleRef is a reference plus the role the citing record gave it. The role is
// authored, never guessed from the path.
type RoleRef struct {
	Role PathRole
	Ref  model.ArtifactRef
}

// PathSet is the three families, each sorted and deduplicated, never merged.
type PathSet struct {
	Source     []string
	Artifact   []string
	Constraint []string
}

// CollectPaths gathers the paths references DECLARE, at the revision they pin.
// Historical paths are preserved exactly: a file that has since moved keeps the
// path its pin names, so a rename does not orphan the reference.
func CollectPaths(refs ...RoleRef) (PathSet, error) {
	var s PathSet
	for i, rr := range refs {
		into := map[PathRole]*[]string{SourcePath: &s.Source, ArtifactPath: &s.Artifact, ConstraintPath: &s.Constraint}[rr.Role]
		if into == nil {
			return PathSet{}, fault("invalid-field", fmt.Sprintf("refs[%d].role", i), "unknown path role: "+string(rr.Role))
		}
		*into = append(*into, declaredPaths(rr.Ref)...)
	}
	s.Source, s.Artifact, s.Constraint = tidy(s.Source), tidy(s.Artifact), tidy(s.Constraint)
	return s, nil
}

// Roles reports every role a path was cited under. A path in two families is a
// fact worth seeing, not something to silently collapse into one answer.
func (s PathSet) Roles(p string) []PathRole {
	var out []PathRole
	for _, f := range []struct {
		role  PathRole
		paths []string
	}{{SourcePath, s.Source}, {ArtifactPath, s.Artifact}, {ConstraintPath, s.Constraint}} {
		for _, have := range f.paths {
			if have == p {
				out = append(out, f.role)
				break
			}
		}
	}
	return out
}

func declaredPaths(ref model.ArtifactRef) []string {
	var out []string
	if ref.Git != nil && ref.Git.Path != "" {
		out = append(out, ref.Git.Path)
	}
	if ref.Content != nil {
		for _, l := range ref.Content.Locators {
			out = append(out, l.Path)
		}
	}
	return out
}

func tidy(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	sort.Strings(in)
	out := in[:0]
	for i, s := range in {
		if i == 0 || in[i-1] != s {
			out = append(out, s)
		}
	}
	return out
}

func (r *Resolver) checkPaths(ref model.ArtifactRef) error {
	if ref.Git != nil {
		if err := relativePath(ref.Git.Path, "artifact.git.path"); err != nil {
			return err
		}
	}
	if ref.Content != nil {
		for i, l := range ref.Content.Locators {
			if err := relativePath(l.Path, fmt.Sprintf("artifact.content.locators[%d].path", i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// relativePath refuses anything that could read outside the project root or be
// mistaken for a git option or revision expression.
func relativePath(s, at string) error {
	switch {
	case strings.TrimSpace(s) == "":
		return fault("invalid-field", at, "blank path")
	case strings.ContainsAny(s, "\\\x00:"):
		return fault("invalid-field", at, "path contains a separator, colon or NUL")
	case strings.HasPrefix(s, "/"), strings.HasPrefix(s, "-"), strings.HasPrefix(s, "./"):
		return fault("invalid-field", at, "expected a plain project-relative path")
	}
	for _, part := range strings.Split(s, "/") {
		if part == ".." {
			return fault("invalid-field", at, "path escapes the project root")
		}
	}
	return nil
}
