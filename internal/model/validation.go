package model

// Shared text, enum, path, commit, and field-error checks live here.
// Recursive schema traversal and domain-specific validation do not.
// This small shared vocabulary stays below 200 lines rather than borrowing unrelated code.

import (
	"fmt"
	"path"
	"reflect"
	"strings"
	"unicode"
)

// Blank reports whether a required semantic string carries nothing a reader can
// see. strings.TrimSpace answers "is every rune Unicode White_Space", which is a
// different question: a zero width space, a byte order mark, a right to left
// mark and a word joiner are all category Cf, render as nothing, and are not
// White_Space. Lane E accepted an instrument whose blind_to was U+200B, which is
// an instrument declaring no blind spot at all.
//
// This is the one emptiness rule. Everything that needs one calls it.
// stripInvisible removes what Blank would ignore, so a reserved name cannot be
// smuggled past an exact comparison with padding a reader cannot see. Found by
// lane E: a knob called "status " was not the reserved name "status".
func stripInvisible(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) || r == '\uFEFF' {
			return -1
		}
		return r
	}, s))
}

func Blank(s string) bool {
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) || r == '\uFEFF' {
			continue
		}
		return false
	}
	return true
}

func invalid(p, detail string) error { return fault("invalid-field", p, detail) }
func oneOf(value, p string, values ...string) error {
	for _, v := range values {
		if value == v {
			return nil
		}
	}
	return invalid(p, "unknown enum member: "+value)
}
func relativePaths(paths []string, p string) error {
	for i, s := range paths {
		if err := relativePath(s, fmt.Sprintf("%s[%d]", p, i)); err != nil {
			return err
		}
	}
	return nil
}
func relativePath(s, p string) error {
	if Blank(s) || strings.ContainsAny(s, "\\\x00") || path.IsAbs(s) || strings.Contains(s, ":") {
		return invalid(p, "expected a nonblank project-relative path")
	}
	for _, part := range strings.Split(s, "/") {
		if part == ".." {
			return invalid(p, "path escapes the project root")
		}
	}
	return nil
}
func validateCommit(format, commit, p string) error {
	n := 40
	switch format {
	case "sha1":
	case "sha256":
		n = 64
	default:
		return invalid(p+".object_format", "unknown Git object format")
	}
	if len(commit) != n {
		return invalid(p+".commit", "commit must be a full object name")
	}
	for _, c := range commit {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return invalid(p+".commit", "commit must be lowercase hex")
		}
	}
	return nil
}

func lowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return len(s) > 0
}

// ValidateArtifactRef checks the tagged-locator shape and every pin's path
// syntax and selector, including corroboration, using the schema walk's rules.
func ValidateArtifactRef(a ArtifactRef, path string) error {
	switch a.Kind {
	case "git":
		if a.Git == nil {
			return fault("invalid-field", path+".git", `kind "git" requires a git pin`)
		}
	case "content":
		if a.Content == nil {
			return fault("invalid-field", path+".content", `kind "content" requires a content pin`)
		}
	default:
		return fault("invalid-field", path+".kind", `kind must be "git" or "content"`)
	}
	if a.Git != nil {
		want := 40
		if a.Git.ObjectFormat == "sha256" {
			want = 64
		} else if a.Git.ObjectFormat != "sha1" {
			return fault("invalid-field", path+".git.object_format", `object format must be "sha1" or "sha256"`)
		}
		if len(a.Git.Commit) != want || !lowerHex(a.Git.Commit) {
			// Length alone let "gggg...g" through - a true measurement of the
			// wrong property. Found by lane E.
			return fault("invalid-field", path+".git.commit",
				fmt.Sprintf("commit must be %d lowercase hex characters for %s", want, a.Git.ObjectFormat))
		}
		if err := relativePath(a.Git.Path, path+".git.path"); err != nil {
			return err
		}
	}
	if a.Content != nil {
		if !ValidDigest(a.Content.SHA256) {
			return fault("invalid-field", path+".content.sha256", "not lowercase sha-256 hex")
		}
		if a.Content.MediaType == "" {
			return fault("invalid-field", path+".content.media_type", "empty media type")
		}
		for i, locator := range a.Content.Locators {
			if err := relativePath(locator.Path, fmt.Sprintf("%s.content.locators[%d].path", path, i)); err != nil {
				return err
			}
		}
	}
	return validateValue(reflect.ValueOf(a.Selector), path+".selector")
}
