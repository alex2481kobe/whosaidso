// Tests for README splicing and the per-file line counts that replaced
// tools/readme-tree.sh and tools/filesize.sh. Scanner and renderer behaviour
// beyond those two absorbed jobs does not belong here.

package main

import (
	"os"
	"path/filepath"
	"testing"
)

const readmeFixture = "intro\n" + markBegin + "\n```text\nold\n```\n" + markEnd + "\noutro\n"

func writeReadme(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readReadme(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSpliceReplacesOnlyTheMarkedRegion(t *testing.T) {
	got := splice(readmeFixture, "new\n")
	want := "intro\n" + markBegin + "\n```text\nnew\n```\n" + markEnd + "\noutro\n"
	if got != want {
		t.Fatalf("splice:\n%q\nwant\n%q", got, want)
	}
}

func TestCheckStaleExitsOneAndWritesNothing(t *testing.T) {
	dir := writeReadme(t, readmeFixture)
	if code := syncReadme(dir, "new\n", true); code != 1 {
		t.Fatalf("stale -check exit = %d, want 1", code)
	}
	if readReadme(t, dir) != readmeFixture {
		t.Fatal("-check wrote to README.md")
	}
}

func TestCheckCurrentExitsZero(t *testing.T) {
	dir := writeReadme(t, readmeFixture)
	if code := syncReadme(dir, "old\n", true); code != 0 {
		t.Fatalf("current -check exit = %d, want 0", code)
	}
}

func TestReadmeRewrites(t *testing.T) {
	dir := writeReadme(t, readmeFixture)
	if code := syncReadme(dir, "new\n", false); code != 0 {
		t.Fatalf("-readme exit = %d", code)
	}
	if readReadme(t, dir) != splice(readmeFixture, "new\n") {
		t.Fatal("-readme did not write the spliced README")
	}
}

func TestMissingMarkersRefuse(t *testing.T) {
	for _, body := range []string{"no markers\n", markBegin + "\nonly begin\n", "only end\n" + markEnd + "\n"} {
		dir := writeReadme(t, body)
		if code := syncReadme(dir, "new\n", false); code != 2 {
			t.Fatalf("%q: exit = %d, want 2", body, code)
		}
		if readReadme(t, dir) != body {
			t.Fatalf("%q: refused but still wrote", body)
		}
	}
}

func TestFileLinesListsProductionFilesOnly(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module m\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "a"), 0o755)
	os.WriteFile(filepath.Join(dir, "a", "x.go"), []byte("package a\n\nvar X = 1\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a", "x_test.go"), []byte("package a\n"), 0o644)
	pkgs, err := scan(dir, "m")
	if err != nil {
		t.Fatal(err)
	}
	fl := pkgs[0].FileLines
	if len(fl) != 1 || fl[0].Path != "a/x.go" || fl[0].Lines != 3 {
		t.Fatalf("file_lines = %+v, want [{a/x.go 3}]", fl)
	}
}
