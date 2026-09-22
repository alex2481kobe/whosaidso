package evidence

// Shared test helpers for package evidence: git repositories, files, refs and
// fault assertions. Tests do not belong here; they live in the themed *_test.go files.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"datum/internal/model"
)

// ---- helpers -------------------------------------------------------------

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := ExecGit(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

func newRepo(t *testing.T, args ...string) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, append([]string{"init", "--quiet"}, args...)...)
	gitRun(t, dir, "config", "user.email", "lane-d@example.invalid")
	gitRun(t, dir, "config", "user.name", "lane D")
	return dir
}

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitFile(t *testing.T, root, rel, body string) string {
	t.Helper()
	writeFile(t, root, rel, body)
	gitRun(t, root, "add", rel)
	gitRun(t, root, "commit", "--quiet", "-m", "add "+rel)
	return gitRun(t, root, "rev-parse", "HEAD")
}

func gitRef(format, commit, path, selector, pointer string) model.ArtifactRef {
	return model.ArtifactRef{
		Kind:     "git",
		Git:      &model.GitPin{ObjectFormat: format, Commit: commit, Path: path},
		Selector: model.Selector{Kind: selector, Pointer: pointer},
	}
}

func contentRef(body, media string, locators []string, selector, pointer string) model.ArtifactRef {
	pin := &model.ContentPin{
		SHA256:    model.HashBytes([]byte(body)),
		Length:    uint64(len(body)),
		MediaType: media,
	}
	for _, l := range locators {
		pin.Locators = append(pin.Locators, model.Locator{Path: l})
	}
	return model.ArtifactRef{
		Kind:     "content",
		Content:  pin,
		Selector: model.Selector{Kind: selector, Pointer: pointer},
	}
}

func wantFault(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a refusal with code %q, got none", code)
	}
	var f *model.Fault
	if !errors.As(err, &f) {
		t.Fatalf("expected a typed fault, got %T: %v", err, err)
	}
	if f.Code != code {
		t.Fatalf("expected code %q, got %q (%v)", code, f.Code, err)
	}
}
