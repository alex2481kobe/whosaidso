package evidence

// Where observed and pinned bytes may come from: a run output is read by its
// digest from the artifact store and from nowhere else, a selector binds to an
// output by name, and git pins resolve relative to the whosaidso root. General
// resolver and selector behaviour belongs in resolve_test.go and
// selectors_test.go, not here.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// observeOutput observes one run whose single output is out/result.json.
func observeOutput(t *testing.T, root, artifactDir string) Observation {
	t.Helper()
	env := testEnvelope(t, invocationA)
	env.Outputs = knownOutputs(runOutput("out/result.json", resultArtifact, "application/json"))
	o, err := NewResolverAt(root, artifactDir).Observe(context.Background(), testCriterion(t), env)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// Observe reads an output's bytes from the store by digest: seal admission
// proved they were this run's own and published them there. A copy at any
// path, including one spelled like the output's name or an old run
// directory, is not consulted, and the store's name or place is not part of
// what the run recorded.
func TestRunOutputIsReadByDigestFromTheStoreOnly(t *testing.T) {
	sum := string(model.HashBytes([]byte(resultArtifact)))
	for _, store := range []string{DefaultArtifactDir, "custom/place/artifacts"} {
		t.Run("control store "+store, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, store+"/"+sum, resultArtifact)
			if o := observeOutput(t, root, store); o.Unavailable != "" {
				t.Fatalf("the store's copy of this run's output must read wherever the store is: %s", o.Unavailable)
			}
		})
	}
	t.Run("bytes only at paths, none in the store", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "out/result.json", resultArtifact)
		writeFile(t, root, DefaultArtifactDir+"/runs/"+string(invocationA)+"/out/result.json", resultArtifact)
		if o := observeOutput(t, root, DefaultArtifactDir); o.Unavailable == "" {
			t.Fatal("a file at a path is not the store's copy of this run's output; the observation must be unavailable")
		}
	})
}

// A selector binds to an output by name inside the observed run. A name in
// non-canonical form names nothing: refused, never normalized, so no second
// spelling reaches an output. Two outputs named by one selector are ambiguous.
func TestSelectorBindsToOneOutputByCanonicalName(t *testing.T) {
	selector := func(names ...string) model.ArtifactRef {
		return contentRef(criterionExample, "application/json", names, "json-pointer", "/results")
	}
	out := func(name string) model.RunOutput { return runOutput(name, resultArtifact, "application/json") }
	for _, tc := range []struct {
		name     string
		selector model.ArtifactRef
		outputs  []model.RunOutput
		want     string // matched name, or "" for no match
	}{
		{"control-exact-name", selector("out/result.json"), []model.RunOutput{out("out/result.json")}, "out/result.json"},
		{"control-among-others", selector("out/result.json"), []model.RunOutput{out("stdout"), out("out/result.json")}, "out/result.json"},
		{"absent-name", selector("out/result.json"), []model.RunOutput{out("stdout")}, ""},
		{"dot-segment", selector("out/./result.json"), []model.RunOutput{out("out/result.json")}, ""},
		{"double-slash", selector("out//result.json"), []model.RunOutput{out("out/result.json")}, ""},
		{"trailing-slash", selector("out/result.json/"), []model.RunOutput{out("out/result.json")}, ""},
		{"dot-dot", selector("../out/result.json"), []model.RunOutput{out("out/result.json")}, ""},
		{"leading-dot-slash", selector("./out/result.json"), []model.RunOutput{out("out/result.json")}, ""},
		{"two-names-two-outputs-ambiguous", selector("stdout", "out/result.json"), []model.RunOutput{out("stdout"), out("out/result.json")}, ""},
		{"two-names-one-output", selector("stdout", "out/result.json"), []model.RunOutput{out("out/result.json")}, "out/result.json"},
		// A decoded seal cannot carry these names; the matcher refuses them anyway.
		{"same-non-canonical-spelling-both-sides", selector("out/./result.json"), []model.RunOutput{out("out/./result.json")}, ""},
		{"same-dot-dot-both-sides", selector("../result.json"), []model.RunOutput{out("../result.json")}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, why := matchOutput(tc.outputs, tc.selector)
			if tc.want == "" && why == "" {
				t.Fatalf("matched %q; want no match", got.Name)
			}
			if tc.want != "" && (why != "" || got.Name != tc.want) {
				t.Fatalf("want %s, got %q (%s)", tc.want, got.Name, why)
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
