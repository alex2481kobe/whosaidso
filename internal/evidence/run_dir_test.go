package evidence

// Where observed and pinned bytes may come from: run-dir outputs read from the
// run's own directory or their admitted copy in the store, non-canonical paths matching nothing, and git pins
// resolved relative to the whosaidso root. General resolver and selector behaviour
// belongs in resolve_test.go and selectors_test.go, not here.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"whosaidso/internal/model"
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

// Observe reads the matched run-dir path, then the content store: seal
// admission proved the bytes were this run's output and materialized them, so
// the store answers after the run directory is gone. No other locator answers.
func TestRunDirOutputIsReadFromTheRunDirectoryOrItsAdmittedCopy(t *testing.T) {
	sum := string(model.HashBytes([]byte(resultArtifact)))

	control := t.TempDir()
	writeFile(t, control, RunDir(invocationA)+"/out/result.json", resultArtifact)
	if o := runDirObserve(t, control); o.Unavailable != "" {
		t.Fatalf("control: a run-dir output present in the run directory must read: %s", o.Unavailable)
	}

	t.Run("admitted bytes in the content store after the run directory is gone", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, DefaultArtifactDir+"/"+sum, resultArtifact)
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(RunDir(invocationA)))); !os.IsNotExist(err) {
			t.Fatalf("fixture: the run directory must not exist: %v", err)
		}
		if o := runDirObserve(t, root); o.Unavailable != "" {
			t.Fatalf("the admitted copy of this run's output must read: %s", o.Unavailable)
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
	dot := ".whosaidso/./artifacts/runs/" + string(invocationB) + "/out/result.json"
	dbl := ".whosaidso//artifacts/runs/" + string(invocationB) + "/out/result.json"
	for _, tc := range []struct {
		name, contract, output string
		match                  bool
	}{
		{"control-own-run-dir", "out/result.json", a + "/out/result.json", true},
		{"bare-form-names-nothing", "out/result.json", "out/result.json", false},
		{"control-other-run-clean", b + "/out/result.json", b + "/out/result.json", false},
		{"contract-dot-segment-to-other-run", dot, dot, false},
		{"contract-double-slash-to-other-run", dbl, dbl, false},
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

// The whosaidso root sits one directory below the repository top level. A pin to
// secret.json names whosaidso-root/secret.json, which does not exist, never the
// repository's top-level file.
func TestGitPinResolvesRelativeToTheWhoSaidSoRoot(t *testing.T) {
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
		t.Fatalf("control: a committed file inside the whosaidso root must resolve: %q %v", got.Bytes, err)
	}
	if _, err := r.Resolve(ctx, gitRef("sha1", head, "secret.json", "whole", "")); err == nil {
		t.Fatal("secret.json is outside the whosaidso root and must not resolve")
	}
	// The same lookup backs a git pin that corroborates a content pin.
	body := `{"secret":"outside the root"}`
	both := contentRef(body, "application/json", nil, "whole", "")
	both.Git = &model.GitPin{ObjectFormat: "sha1", Commit: head, Path: "secret.json"}
	writeFile(t, filepath.Join(repo, "project"), DefaultArtifactDir+"/"+string(model.HashBytes([]byte(body))), body)
	if _, err := r.Resolve(ctx, both); err == nil {
		t.Fatal("a corroborating git pin must also resolve inside the whosaidso root")
	}
}
