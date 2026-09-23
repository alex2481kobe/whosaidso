// Known-answer tests for the values a claim reads out of archtree: per-file
// line counts, a package's largest file, and which directories are scanned.
// They are the validation artifact an admitter judges, so each one pins a
// number worked out by hand from a fixture written here. Criterion evaluation
// of these readings lives in criterion_test.go; README splicing in
// readme_test.go.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeModule lays out a fixture module. Keys are slash paths under the root.
func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// knownModule's answers, by hand:
//
//	a/b.go       4 lines   ties with c.go for largest; first by name wins
//	a/c.go       4 lines
//	a/d.go       2 lines   the last line has no newline and still counts
//	a/a_test.go 12 lines   the biggest file, but a test: never "largest"
//	a/testdata/  skipped   Go files there are fixtures, not a package
//	.hidden/     skipped   dot-directories are not the module's code
var knownModule = map[string]string{
	"go.mod":          "module m\n",
	"a/b.go":          "// Package a is a fixture.\npackage a\n\nvar B = 1\n",
	"a/c.go":          "package a\n\nvar C = 1\nvar C2 = 2\n",
	"a/d.go":          "package a\nvar D = 1",
	"a/a_test.go":     "package a\n" + strings.Repeat("\n", 11),
	"a/testdata/t.go": "package t\n\nvar T = 1\n",
	".hidden/h.go":    "package h\n",
}

func measureKnown(t *testing.T) report {
	t.Helper()
	r, err := measure(writeModule(t, knownModule))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestKnownAnswerFileLines(t *testing.T) {
	r := measureKnown(t)
	want := []namedNumber{{"a/b.go", 4}, {"a/c.go", 4}, {"a/d.go", 2}}
	got := r.Readings.FileLines.Values
	if len(got) != len(want) {
		t.Fatalf("file_lines = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("file_lines[%d] = %+v, want %+v (all %+v)", i, got[i], want[i], got)
		}
	}
	if n := r.Readings.ByFile["a/d.go"].Value; n != 2 {
		t.Fatalf("by_file a/d.go = %d, want 2: a final line without a newline is still a line", n)
	}
	a := r.Readings.ByPackage["a"]
	if a.Lines.Value != 10 || a.TestLines.Value != 12 {
		t.Fatalf("package a lines/test_lines = %d/%d, want 10/12", a.Lines.Value, a.TestLines.Value)
	}
}

func TestKnownAnswerLargestFile(t *testing.T) {
	r := measureKnown(t)
	largest := r.Readings.ByPackage["a"].Largest
	if largest == nil {
		t.Fatal("package a has production files but no largest_file_lines reading")
	}
	// 4, not 12: a_test.go is bigger but is not a production file. And
	// a/b.go, not a/c.go: on a tie the first file by name is reported.
	if largest.Value != 4 || largest.Path != "a/b.go" {
		t.Fatalf("largest_file_lines = %d at %s, want 4 at a/b.go", largest.Value, largest.Path)
	}
	p := r.Packages[0]
	if p.LargestN != 4 || p.Largest != "b.go" {
		t.Fatalf("packages[0] largest = %d %s, want 4 b.go", p.LargestN, p.Largest)
	}
}

func TestKnownAnswerSkipsTestdataAndDotDirectories(t *testing.T) {
	r := measureKnown(t)
	var paths []string
	for _, p := range r.Packages {
		paths = append(paths, p.Path)
	}
	if len(paths) != 1 || paths[0] != "a" {
		t.Fatalf("packages = %v, want [a]: testdata/ and .hidden/ must be skipped", paths)
	}
	for path := range r.Readings.ByFile {
		if strings.Contains(path, "testdata") || strings.HasPrefix(path, ".") {
			t.Fatalf("by_file lists %s, which should never have been scanned", path)
		}
	}
}

// A package with only tests has no largest file. Reporting 0 would read as a
// real, very small file; the reading is absent instead, and its file set is
// empty rather than missing.
func TestTestOnlyPackageHasNoLargestFile(t *testing.T) {
	r, err := measure(writeModule(t, map[string]string{"go.mod": "module m\n", "z/z_test.go": "package z\n"}))
	if err != nil {
		t.Fatal(err)
	}
	z, ok := r.Readings.ByPackage["z"]
	if !ok {
		t.Fatal("a test-only package must still be reported")
	}
	if z.Largest != nil {
		t.Fatalf("test-only package reports a largest file: %+v", *z.Largest)
	}
	if z.FileLines.Values == nil || len(z.FileLines.Values) != 0 {
		t.Fatalf("test-only package file_lines = %#v, want an empty set", z.FileLines.Values)
	}
}

// A root named with a leading dot, as -root ../.. is, is still the module.
func TestRootIsScannedWhateverItIsCalled(t *testing.T) {
	parent := writeModule(t, map[string]string{".mod/go.mod": "module m\n", ".mod/a/x.go": "package a\n"})
	r, err := measure(filepath.Join(parent, ".mod"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Packages) != 1 || r.Packages[0].Path != "a" {
		t.Fatalf("packages = %+v, want [a]", r.Packages)
	}
}

// Purpose is the package doc comment the author supplied, never a file's own
// comment. p has a file comment attached in its first file, a package doc in
// a later one and another in doc.go: doc.go wins. q has only a file comment,
// so no purpose was supplied. m is a main package documented as a command.
func TestKnownAnswerPurposeIsPackageDocOnly(t *testing.T) {
	r, err := measure(writeModule(t, map[string]string{
		"go.mod":        "module m\n",
		"p/a.go":        "// a.go implements the id command.\npackage p\n",
		"p/b.go":        "// Package p is from b.\npackage p\n",
		"p/doc.go":      "// Package p is from doc.\npackage p\n",
		"q/a.go":        "// a.go implements the id command.\npackage q\n",
		"q/b.go":        "// Detached file comment.\n\npackage q\n",
		"cmd/x/main.go": "// Command x does a thing.\npackage main\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{
		"p":     {"from doc", "doc.go"},
		"q":     {purposeMissing, ""},
		"cmd/x": {"does a thing", "main.go"},
	}
	for _, p := range r.Packages {
		w, ok := want[p.Path]
		if !ok {
			t.Errorf("unexpected package %s", p.Path)
			continue
		}
		if p.Purpose != w[0] || p.PurposeFrom != w[1] {
			t.Errorf("%s: purpose %q from %q, want %q from %q", p.Path, p.Purpose, p.PurposeFrom, w[0], w[1])
		}
		delete(want, p.Path)
	}
	for path := range want {
		t.Errorf("package %s missing", path)
	}
}
