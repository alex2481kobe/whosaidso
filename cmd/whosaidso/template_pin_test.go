package main

// Tests for `whosaidso template --pin`: a content pin carries the digest, length
// and a media type of the real bytes on disk, a git pin the full commit and
// object format of the committed object (never the working copy), selectors
// are only what was given, and a pin never lands outside a reference field.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"whosaidso/internal/evidence"
	"whosaidso/internal/model"
)

// pinOf reads the pin a printed template holds at path.
func pinOf(t *testing.T, data map[string]any, path string) model.ArtifactRef {
	t.Helper()
	raw, err := json.Marshal(boundAt(data, path))
	if err != nil {
		t.Fatal(err)
	}
	var ref model.ArtifactRef
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatalf("%s is not an artifact reference: %s", path, raw)
	}
	return ref
}

func TestPinsMatchRealBytesAndGitObjects(t *testing.T) {
	f := boundWorld(t)
	homeGit(t, f.root, "init", "-q")
	homeGit(t, f.root, "add", "whosaidso.toml", "tools", "validation", "out")
	homeGit(t, f.root, "commit", "-q", "-m", "fixture")
	committed := homeGit(t, f.root, "show", "HEAD:tools/measure.sh")
	// The working copy moves on after the commit: a git pin must not see it.
	proofWrite(t, f.root, "tools/measure.sh", "echo edited after the commit\n")
	proofWrite(t, f.root, "notes/plain.txt", "plain words\n")
	if err := os.WriteFile(filepath.Join(f.root, "notes", "raw.bin"), []byte{0, 1, 2, 0xff}, 0600); err != nil {
		t.Fatal(err)
	}
	boundCapture(t, f.root, "blocker.hold", "--task", string(f.task), "--set", "reason=resume", "--set", `actor={"id":"agent"}`, "--set", "criterion=the fixture output exists")
	hold := string(openHold(t, boundSnapshot(t, f.root), f.task))

	for file, media := range map[string]string{"out/result.json": "application/json", "notes/plain.txt": "text/plain", "notes/raw.bin": "application/octet-stream"} {
		want, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(file)))
		if err != nil {
			t.Fatal(err)
		}
		ref := pinOf(t, boundPrint(t, f.root, "blocker.clear", "--hold", hold, "--pin", "resolving_witness="+file), "resolving_witness")
		if ref.Kind != "content" || ref.Git != nil || ref.Content.SHA256 != model.HashBytes(want) || ref.Content.Length != uint64(len(want)) ||
			ref.Content.MediaType != media || len(ref.Content.Locators) != 1 || ref.Content.Locators[0].Path != file || ref.Selector.Kind != "whole" {
			t.Errorf("content pin of %s does not match its bytes: %+v %+v", file, ref, ref.Content)
		}
	}
	pointed := pinOf(t, boundPrint(t, f.root, "blocker.clear", "--hold", hold, "--pin", "resolving_witness=out/result.json#/results"), "resolving_witness")
	if pointed.Selector != (model.Selector{Kind: "json-pointer", Pointer: "/results"}) {
		t.Errorf("the selector must be exactly the pointer given: %+v", pointed.Selector)
	}

	ref := pinOf(t, boundPrint(t, f.root, "blocker.clear", "--hold", hold, "--pin", "resolving_witness=tools/measure.sh@HEAD"), "resolving_witness")
	if ref.Kind != "git" || ref.Content != nil || ref.Git.Commit != homeGit(t, f.root, "rev-parse", "HEAD") ||
		ref.Git.ObjectFormat != homeGit(t, f.root, "rev-parse", "--show-object-format") || ref.Git.Path != "tools/measure.sh" {
		t.Fatalf("git pin must carry the full commit and object format: %+v", ref.Git)
	}
	resolved, err := evidence.NewResolverAt(f.root, ".whosaidso/artifacts").Resolve(context.Background(), ref)
	if err != nil || string(resolved.Bytes) != committed+"\n" {
		t.Fatalf("the git pin must name the committed bytes, not the working copy: %q, %v", resolved.Bytes, err)
	}
	// Control: the pinned clear admits through the real gate.
	boundCapture(t, f.root, "blocker.clear", "--hold", hold, "--pin", "resolving_witness=tools/measure.sh@HEAD")

	// A file outside the project is pinned by content alone: no locator (an
	// absolute or ../ path is never stored), its bytes a blob.
	if err := os.WriteFile(filepath.Join(filepath.Dir(f.root), "outside.txt"), []byte("outside\n"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := pinOf(t, boundPrint(t, f.root, "claim.assert", "--pin", "provenance.source_refs[0]=../outside.txt"), "provenance.source_refs[0]")
	if outside.Kind != "content" || outside.Content.SHA256 != model.HashBytes([]byte("outside\n")) || outside.Content.Locators == nil || len(outside.Content.Locators) != 0 {
		t.Errorf("a pin outside the project must be content-only with no locator: %+v %+v", outside, outside.Content)
	}
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"blocker.hold", "--task", string(f.task), "--pin", "criterion=out/result.json"}, 2},
		{[]string{"claim.assert", "--pin", "provenance.source_refs[0]=missing.json"}, 1},
		{[]string{"claim.assert", "--pin", "provenance.source_refs[0]=tools/measure.sh@no-such-rev"}, 1},
		{[]string{"claim.assert", "--pin", "provenance.source_refs[0]=out/result.json"}, 0},
	} {
		if _, errs, code := cliRun(t, f.root, nil, "agent", append([]string{"template"}, tc.args...)...); code != tc.code {
			t.Errorf("%v: exit %d, want %d: %s", tc.args, code, tc.code, errs)
		}
	}
	// A second source ref is appended after the first.
	two := boundPrint(t, f.root, "claim.assert", "--pin", "provenance.source_refs[0]=out/result.json", "--pin", "provenance.source_refs[1]=notes/plain.txt")
	if refs := boundAt(two, "provenance.source_refs").([]any); len(refs) != 2 || fmt.Sprint(boundAt(two, "provenance.source_refs[1].content.locators[0].path")) != "notes/plain.txt" {
		t.Fatalf("an index one past the end appends a pin: %v", refs)
	}
}
