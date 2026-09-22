package evidence

// Where observed and pinned bytes may come from: run-dir outputs read only from
// the run's own directory, non-canonical paths matching nothing, and git pins
// resolved relative to the datum root. General resolver and selector behaviour
// belongs in resolve_test.go and selectors_test.go, not here.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"datum/internal/model"
)

// runDirObserve observes one run whose single output declares its run-dir
// path plus any extra locators. The bytes are placed by the caller.
func runDirObserve(t *testing.T, root string, extra ...string) Observation {
	t.Helper()
	c := testCriterion(t)
	env := testEnvelope(t, invocationA)
	locators := append([]string{RunDir(invocationA) + "/out/result.json"}, extra...)
	env.OutputRefs = model.Availability[[]model.ArtifactRef]{
		State: model.Known,
		Value: &[]model.ArtifactRef{contentRef(resultArtifact, "application/json", locators, "whole", "")},
	}
	o, err := NewResolver(root).Observe(context.Background(), c, env)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestRunDirOutputIsReadOnlyFromTheRunsOwnDirectory(t *testing.T) {
	sum := string(model.HashBytes([]byte(resultArtifact)))

	control := t.TempDir()
	writeFile(t, control, RunDir(invocationA)+"/out/result.json", resultArtifact)
	if o := runDirObserve(t, control); o.Unavailable != "" {
		t.Fatalf("control: a run-dir output present in the run directory must read: %s", o.Unavailable)
	}

	t.Run("bytes only in the content store", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, DefaultArtifactDir+"/"+sum, resultArtifact)
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(RunDir(invocationA)))); !os.IsNotExist(err) {
			t.Fatalf("fixture: the run directory must not exist: %v", err)
		}
		if o := runDirObserve(t, root); o.Unavailable == "" {
			t.Fatal("a digest in the store is not this run's output; the observation must be unavailable")
		}
	})

	t.Run("bytes only at another declared locator", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "out/result.json", resultArtifact)
		if o := runDirObserve(t, root, "out/result.json"); o.Unavailable == "" {
			t.Fatal("a copy outside the run directory is not this run's output; the observation must be unavailable")
		}
	})
}

func TestNonCanonicalPathsMatchNothing(t *testing.T) {
	a, b := RunDir(invocationA), RunDir(invocationB)
	ref := func(p string) model.ArtifactRef {
		return contentRef(criterionExample, "application/json", []string{p}, "whole", "")
	}
	other := b + "/out/result.json"
	for _, tc := range []struct {
		name, contract, output string
		match                  bool
	}{
		{"control-own-run-dir", "out/result.json", a + "/out/result.json", true},
		{"control-bare-form", "out/result.json", "out/result.json", true},
		{"contract-dot-segment-to-other-run", "record/./artifacts/runs/" + string(invocationB) + "/out/result.json", other, false},
		{"contract-double-slash-to-other-run", "record//artifacts/runs/" + string(invocationB) + "/out/result.json", other, false},
		{"contract-dot-segment-bare", "out/./result.json", "out/./result.json", false},
		{"contract-trailing-slash", "out/result.json/", "out/result.json/", false},
		{"output-double-slash-in-own-run-dir", "out/result.json", a + "//out/result.json", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, at, why := matchOutput([]model.ArtifactRef{ref(tc.output)}, a, ref(tc.contract))
			if tc.match && (why != "" || at != tc.output) {
				t.Fatalf("want a match at %s, got %v at %q (%s)", tc.output, declaredPaths(got), at, why)
			}
			if !tc.match && why == "" {
				t.Fatalf("matched %v at %q; a non-canonical path must name nothing", declaredPaths(got), at)
			}
		})
	}
}

// The datum root sits one directory below the repository top level. A pin to
// secret.json names datum-root/secret.json, which does not exist, never the
// repository's top-level file.
func TestGitPinResolvesRelativeToTheDatumRoot(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "secret.json", `{"secret":"outside the root"}`)
	writeFile(t, repo, "project/inside.json", `{"inside":"the root"}`)
	gitRun(t, repo, "add", "secret.json", "project/inside.json")
	gitRun(t, repo, "commit", "--quiet", "-m", "fixture")
	head := gitRun(t, repo, "rev-parse", "HEAD")
	r := NewResolver(filepath.Join(repo, "project"))
	ctx := context.Background()

	got, err := r.Resolve(ctx, gitRef("sha1", head, "inside.json", "whole", ""))
	if err != nil || string(got.Bytes) != `{"inside":"the root"}` {
		t.Fatalf("control: a committed file inside the datum root must resolve: %q %v", got.Bytes, err)
	}
	if _, err := r.Resolve(ctx, gitRef("sha1", head, "secret.json", "whole", "")); err == nil {
		t.Fatal("secret.json is outside the datum root and must not resolve")
	}
	// The same lookup backs a git pin that corroborates a content pin.
	body := `{"secret":"outside the root"}`
	both := contentRef(body, "application/json", nil, "whole", "")
	both.Git = &model.GitPin{ObjectFormat: "sha1", Commit: head, Path: "secret.json"}
	writeFile(t, filepath.Join(repo, "project"), DefaultArtifactDir+"/"+string(model.HashBytes([]byte(body))), body)
	if _, err := r.Resolve(ctx, both); err == nil {
		t.Fatal("a corroborating git pin must also resolve inside the datum root")
	}
}
