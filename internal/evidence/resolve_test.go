package evidence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
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

// ---- git pins ------------------------------------------------------------

// A pin names bytes at a revision. Resolving it has to answer what those bytes
// WERE, whatever the working tree says now, or a record silently starts
// describing today's file instead of the one that was measured.
func TestGitPinResolvesTheCommitNotTheWorkingTree(t *testing.T) {
	root := newRepo(t)
	const first = `{"depth":0.30}`
	const second = `{"depth":9.99}`
	c1 := commitFile(t, root, "engine/pelvis.json", first)

	r := NewResolver(root)
	ctx := context.Background()

	got, err := r.Resolve(ctx, gitRef("sha1", c1, "engine/pelvis.json", "whole", ""))
	if err != nil {
		t.Fatalf("control: %v", err)
	}
	if string(got.Bytes) != first {
		t.Fatalf("control: got %q, want %q", got.Bytes, first)
	}
	if got.Origin != OriginGit || got.DeclaredPath != "engine/pelvis.json" {
		t.Fatalf("control: origin %q path %q", got.Origin, got.DeclaredPath)
	}

	// Dirty working tree: the pin must not follow it.
	writeFile(t, root, "engine/pelvis.json", second)
	got, err = r.Resolve(ctx, gitRef("sha1", c1, "engine/pelvis.json", "whole", ""))
	if err != nil {
		t.Fatalf("dirty tree: %v", err)
	}
	if string(got.Bytes) != first {
		t.Fatalf("dirty tree: resolver returned the working copy %q", got.Bytes)
	}

	// A later commit does not change what the earlier pin means either.
	c2 := commitFile(t, root, "engine/pelvis.json", second)
	if c1 == c2 {
		t.Fatal("expected two distinct commits")
	}
	got, err = r.Resolve(ctx, gitRef("sha1", c1, "engine/pelvis.json", "whole", ""))
	if err != nil {
		t.Fatalf("historical pin: %v", err)
	}
	if string(got.Bytes) != first {
		t.Fatalf("historical pin: got %q, want %q", got.Bytes, first)
	}

	// A rename does not orphan the pin: the authored path still resolves at the
	// commit that had it, and the resolver reports the path as authored.
	gitRun(t, root, "mv", "engine/pelvis.json", "engine/pelvis-cover.json")
	gitRun(t, root, "commit", "--quiet", "-m", "rename")
	got, err = r.Resolve(ctx, gitRef("sha1", c1, "engine/pelvis.json", "whole", ""))
	if err != nil {
		t.Fatalf("after rename: %v", err)
	}
	if got.DeclaredPath != "engine/pelvis.json" || string(got.Bytes) != first {
		t.Fatalf("after rename: path %q bytes %q", got.DeclaredPath, got.Bytes)
	}
}

func TestGitPinRefusals(t *testing.T) {
	root := newRepo(t)
	head := commitFile(t, root, "engine/pelvis.json", `{"depth":0.30}`)
	r := NewResolver(root)
	ctx := context.Background()

	// Control: this resolver resolves. A resolver that refused everything would
	// pass every case below while being useless.
	if _, err := r.Resolve(ctx, gitRef("sha1", head, "engine/pelvis.json", "whole", "")); err != nil {
		t.Fatalf("control: %v", err)
	}

	missing := strings.Repeat("a", 40)
	cases := []struct {
		name string
		ref  model.ArtifactRef
		code string
	}{
		{"object format the repository does not use", gitRef("sha256", strings.Repeat("b", 64), "engine/pelvis.json", "whole", ""), "invalid-field"},
		{"commit that is not in this repository", gitRef("sha1", missing, "engine/pelvis.json", "whole", ""), "unavailable"},
		{"path that is a directory", gitRef("sha1", head, "engine", "whole", ""), "invalid-field"},
		{"path that does not exist at that commit", gitRef("sha1", head, "engine/absent.json", "whole", ""), "unavailable"},
		{"path escaping the project root", gitRef("sha1", head, "../outside.json", "whole", ""), "invalid-field"},
		{"path that could be read as an option", gitRef("sha1", head, "-c", "whole", ""), "invalid-field"},
		{"abbreviated commit", gitRef("sha1", head[:12], "engine/pelvis.json", "whole", ""), "invalid-field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.Resolve(ctx, tc.ref)
			wantFault(t, err, tc.code)
		})
	}
}

// A git object id and a raw SHA-256 of the same file are different strings,
// because git hashes "blob <len>\0" first. In a sha256 repository both are 64
// lowercase hex characters, so the mistake is invisible by shape alone.
func TestGitObjectIDIsNotTheRawSHA256(t *testing.T) {
	root := newRepo(t, "--object-format=sha256")
	const body = `{"depth":0.30}`
	head := commitFile(t, root, "engine/pelvis.json", body)
	oid := gitRun(t, root, "rev-parse", head+":engine/pelvis.json")
	raw := string(model.HashBytes([]byte(body)))

	if len(oid) != 64 || len(raw) != 64 {
		t.Fatalf("expected two 64-character names, got %d and %d", len(oid), len(raw))
	}
	if oid == raw {
		t.Fatal("expected the git object id and the raw sha-256 to differ")
	}

	r := NewResolver(root)
	ctx := context.Background()

	// Control: the git pin corroborated by the RAW digest resolves and is marked
	// as checked against the second pin.
	ok := gitRef("sha256", head, "engine/pelvis.json", "whole", "")
	ok.Content = contentRef(body, "application/json", []string{"engine/pelvis.json"}, "whole", "").Content
	got, err := r.Resolve(ctx, ok)
	if err != nil {
		t.Fatalf("control: %v", err)
	}
	if !got.Corroborated {
		t.Fatal("control: expected the two pins to be recorded as checked against each other")
	}

	// The git object id offered as a content digest is a different measurement
	// of a different thing, and it is refused rather than accepted as agreement.
	bad := gitRef("sha256", head, "engine/pelvis.json", "whole", "")
	bad.Content = &model.ContentPin{SHA256: model.Digest(oid), Length: uint64(len(body)), MediaType: "application/json"}
	_, err = r.Resolve(ctx, bad)
	wantFault(t, err, "conflict")
}

func TestBothPinsAreCheckedAgainstEachOther(t *testing.T) {
	root := newRepo(t)
	const body = `{"depth":0.30}`
	head := commitFile(t, root, "engine/pelvis.json", body)
	r := NewResolver(root)
	ctx := context.Background()

	both := gitRef("sha1", head, "engine/pelvis.json", "whole", "")
	both.Content = &model.ContentPin{
		SHA256:    model.HashBytes([]byte(body)),
		Length:    uint64(len(body)),
		MediaType: "application/json",
		Locators:  []model.Locator{{Path: "engine/pelvis.json"}},
	}
	// Control: agreeing pins resolve.
	if _, err := r.Resolve(ctx, both); err != nil {
		t.Fatalf("control: %v", err)
	}

	t.Run("digest disagreement", func(t *testing.T) {
		bad := both
		pin := *both.Content
		pin.SHA256 = model.HashBytes([]byte("something else"))
		bad.Content = &pin
		_, err := r.Resolve(ctx, bad)
		wantFault(t, err, "conflict")
	})
	t.Run("length disagreement", func(t *testing.T) {
		bad := both
		pin := *both.Content
		pin.Length = pin.Length + 1
		bad.Content = &pin
		_, err := r.Resolve(ctx, bad)
		wantFault(t, err, "conflict")
	})
	t.Run("corroborating pin that cannot be read", func(t *testing.T) {
		// Content-first reference whose git side is unreadable: unchecked is not
		// agreement, so it refuses rather than trusting the side that did read.
		writeFile(t, root, "out/result.json", body)
		bad := contentRef(body, "application/json", []string{"out/result.json"}, "whole", "")
		bad.Git = &model.GitPin{ObjectFormat: "sha1", Commit: strings.Repeat("a", 40), Path: "engine/pelvis.json"}
		_, err := r.Resolve(ctx, bad)
		wantFault(t, err, "unavailable")
	})
	t.Run("git primary requires readable content corroboration", func(t *testing.T) {
		for _, locators := range [][]model.Locator{nil, {{Path: "missing.json"}}} {
			bad := both
			pin := *both.Content
			pin.Locators = locators
			bad.Content = &pin
			_, err := r.Resolve(ctx, bad)
			wantFault(t, err, "unavailable")
		}
	})
}

func TestLFSPointerIsNotThePayload(t *testing.T) {
	root := newRepo(t)
	const payload = `{"depth":0.30,"frames":12}`
	digest := model.HashBytes([]byte(payload))
	pointer := fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", digest, len(payload))

	head := commitFile(t, root, "engine/pelvis.json", payload)
	head = commitFile(t, root, "assets/render.bin", pointer)
	_ = head

	r := NewResolver(root)
	ctx := context.Background()
	head = gitRun(t, root, "rev-parse", "HEAD")

	// Control: an ordinary committed file still resolves to its own bytes.
	got, err := r.Resolve(ctx, gitRef("sha1", head, "engine/pelvis.json", "whole", ""))
	if err != nil || string(got.Bytes) != payload {
		t.Fatalf("control: %v %q", err, got.Bytes)
	}

	t.Run("pointer alone is not evidence", func(t *testing.T) {
		_, err := r.Resolve(ctx, gitRef("sha1", head, "assets/render.bin", "whole", ""))
		wantFault(t, err, "unavailable")
		if !strings.Contains(err.Error(), string(digest)) {
			t.Fatalf("the refusal should name the payload it needs: %v", err)
		}
	})

	t.Run("payload fetched through the content pin", func(t *testing.T) {
		writeFile(t, root, DefaultArtifactDir+"/"+string(digest), payload)
		ref := gitRef("sha1", head, "assets/render.bin", "whole", "")
		ref.Content = &model.ContentPin{SHA256: digest, Length: uint64(len(payload)), MediaType: "application/octet-stream"}
		got, err := r.Resolve(ctx, ref)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if string(got.Bytes) != payload {
			t.Fatalf("got %q, want the payload", got.Bytes)
		}
		if !got.LFSPointer || !got.Corroborated || got.Origin != OriginArtifactStore {
			t.Fatalf("expected a corroborated payload from the artifact store, got %+v", got.Origin)
		}
	})

	t.Run("pointer naming different bytes than the content pin", func(t *testing.T) {
		other := model.HashBytes([]byte("different payload"))
		writeFile(t, root, DefaultArtifactDir+"/"+string(other), "different payload")
		ref := gitRef("sha1", head, "assets/render.bin", "whole", "")
		ref.Content = &model.ContentPin{SHA256: other, Length: 17, MediaType: "application/octet-stream"}
		_, err := r.Resolve(ctx, ref)
		wantFault(t, err, "conflict")
	})
}

// ---- content pins --------------------------------------------------------

func TestResolverChecksDeclaredMediaType(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body, media, code string
	}{
		{"JSON", `{"value":1}`, "application/json", ""},
		{"PNG", encoded.String(), "image/png", ""},
		{"plain text", "a readable café\n", "text/plain", ""},
		{"opaque bytes", "\x00\xff\x01", "application/octet-stream", ""},
		{"empty opaque bytes", "", "application/octet-stream", ""},
		{"case insensitive media", `{"value":1}`, "APPLICATION/JSON", ""},
		{"JSON claiming PNG", `{"value":1}`, "image/png", "conflict"},
		{"PNG claiming JSON", encoded.String(), "application/json", "conflict"},
		{"incomplete PNG signature", "\x89PNG\r\n\x1a", "image/png", "conflict"},
		{"malformed JSON", `{"value":`, "application/json", "conflict"},
		{"invalid UTF8 JSON", "{\"value\":\"\xff\"}", "application/json", "conflict"},
		{"binary claiming text", "text\x00", "text/plain", "conflict"},
		{"invalid UTF8 text", "\xff", "text/plain", "conflict"},
		{"unsupported type", "arbitrary bytes", "application/x-datum", "unavailable"},
		{"JSON suffix is not a schema check", `{"value":1}`, "application/example+json", "unavailable"},
		{"unverified parameters", `{"value":1}`, "application/json; profile=example", "unavailable"},
		{"malformed media declaration", `{"value":1}`, "application/json; broken", "invalid-field"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newRepo(t)
			head := commitFile(t, root, "artifact.bin", tc.body)
			digest := model.HashBytes([]byte(tc.body))
			pointer := fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", digest, len(tc.body))
			head = commitFile(t, root, "pointer.bin", pointer)
			for _, route := range []string{"content", "git", "lfs-git", "lfs-content"} {
				t.Run(route, func(t *testing.T) {
					ref := contentRef(tc.body, tc.media, []string{"artifact.bin"}, "whole", "")
					if route != "content" {
						ref.Git = &model.GitPin{ObjectFormat: "sha1", Commit: head, Path: "artifact.bin"}
					}
					if strings.HasPrefix(route, "lfs-") {
						ref.Git.Path = "pointer.bin"
					}
					if route == "git" || route == "lfs-git" {
						ref.Kind = "git"
					}
					got, err := NewResolver(root).Resolve(context.Background(), ref)
					if tc.code != "" {
						wantFault(t, err, tc.code)
						var f *model.Fault
						if !errors.As(err, &f) || f.Path != "artifact.content.media_type" || f.Detail == "" {
							t.Fatalf("expected an explained media type refusal: %v", err)
						}
						return
					}
					if err != nil || string(got.Bytes) != tc.body || got.MediaType != tc.media || got.Corroborated != (route != "content") || got.LFSPointer != strings.HasPrefix(route, "lfs-") {
						t.Fatalf("valid media must resolve with its provenance: %+v, %v", got, err)
					}
				})
			}
		})
	}
}

func TestContentPinRefusesLFSPointerFromLocatorOrStore(t *testing.T) {
	digest := model.HashBytes([]byte("payload"))
	pointer := fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize 7\n", digest)
	for _, store := range []bool{false, true} {
		t.Run(fmt.Sprintf("store=%t", store), func(t *testing.T) {
			root := t.TempDir()
			ref := contentRef(pointer, "application/octet-stream", []string{"pointer.bin"}, "whole", "")
			at := "pointer.bin"
			if store {
				at = DefaultArtifactDir + "/" + string(ref.Content.SHA256)
			}
			writeFile(t, root, at, pointer)
			_, err := NewResolver(root).Resolve(context.Background(), ref)
			wantFault(t, err, "unavailable")
			if !strings.Contains(err.Error(), "LFS pointer") || !strings.Contains(err.Error(), string(digest)) {
				t.Fatalf("refusal must explain which payload is needed: %v", err)
			}
		})
	}
	// Mentioning a pointer inside a document does not make the document a stub.
	root := t.TempDir()
	body := "LFS example:\n" + pointer
	writeFile(t, root, "example.txt", body)
	if _, err := NewResolver(root).Resolve(context.Background(), contentRef(body, "text/plain", []string{"example.txt"}, "whole", "")); err != nil {
		t.Fatalf("a document mentioning LFS must remain readable: %v", err)
	}
}

func TestContentPinLocatorsAndArtifactStore(t *testing.T) {
	root := t.TempDir()
	const body = `{"depth":0.30}`
	digest := model.HashBytes([]byte(body))
	r := NewResolver(root)
	ctx := context.Background()

	// Control: the authored locator holds the pinned bytes.
	writeFile(t, root, "out/result.json", body)
	got, err := r.Resolve(ctx, contentRef(body, "application/json", []string{"out/result.json"}, "whole", ""))
	if err != nil {
		t.Fatalf("control: %v", err)
	}
	if got.Origin != OriginLocator || got.DeclaredPath != "out/result.json" || string(got.Bytes) != body {
		t.Fatalf("control: %+v", got)
	}

	t.Run("a copy that does not hash to the pin is not the artifact", func(t *testing.T) {
		writeFile(t, root, "out/result.json", `{"depth":9.99}`)
		_, err := r.Resolve(ctx, contentRef(body, "application/json", []string{"out/result.json"}, "whole", ""))
		wantFault(t, err, "unavailable")

		// The materialized blob is still the artifact, so the same reference
		// resolves once the digest-named copy exists.
		writeFile(t, root, DefaultArtifactDir+"/"+string(digest), body)
		got, err := r.Resolve(ctx, contentRef(body, "application/json", []string{"out/result.json"}, "whole", ""))
		if err != nil {
			t.Fatalf("%v", err)
		}
		if got.Origin != OriginArtifactStore {
			t.Fatalf("expected the artifact store, got %q", got.Origin)
		}
	})

	t.Run("no copy anywhere is unavailable, never invented", func(t *testing.T) {
		empty := t.TempDir()
		lonely := NewResolver(empty)
		_, err := lonely.Resolve(ctx, contentRef(body, "application/json", []string{"out/result.json"}, "whole", ""))
		wantFault(t, err, "unavailable")
	})

	t.Run("locator outside the project root", func(t *testing.T) {
		_, err := r.Resolve(ctx, contentRef(body, "application/json", []string{"../secrets.json"}, "whole", ""))
		wantFault(t, err, "invalid-field")
	})
}

// ---- paths ---------------------------------------------------------------

// An evidence PNG lives where a result was written. That is an output location,
// not the component under test, and the two must not merge into one list.
func TestPathRolesStayDistinguishable(t *testing.T) {
	source := contentRef("a", "text/plain", []string{"engine/pelvis.go"}, "whole", "")
	frame := contentRef("b", "image/png", []string{"documentation/evidence/pelvis-frame.png"}, "whole", "")
	limits := contentRef("c", "text/markdown", []string{"documentation/constraints/limits.md"}, "whole", "")

	set, err := CollectPaths(
		RoleRef{Role: SourcePath, Ref: source},
		RoleRef{Role: ArtifactPath, Ref: frame},
		RoleRef{Role: ConstraintPath, Ref: limits},
	)
	if err != nil {
		t.Fatalf("control: %v", err)
	}
	if len(set.Source) != 1 || set.Source[0] != "engine/pelvis.go" {
		t.Fatalf("source paths: %v", set.Source)
	}
	if len(set.Artifact) != 1 || set.Artifact[0] != "documentation/evidence/pelvis-frame.png" {
		t.Fatalf("artifact paths: %v", set.Artifact)
	}
	if len(set.Constraint) != 1 {
		t.Fatalf("constraint paths: %v", set.Constraint)
	}
	for _, p := range set.Source {
		if strings.HasPrefix(p, "documentation/evidence/") {
			t.Fatalf("an output location reached the source list: %q", p)
		}
	}
	if roles := set.Roles("documentation/evidence/pelvis-frame.png"); len(roles) != 1 || roles[0] != ArtifactPath {
		t.Fatalf("expected the frame to be an artifact path only, got %v", roles)
	}

	t.Run("a path cited in two roles reports both", func(t *testing.T) {
		shared := contentRef("d", "text/plain", []string{"engine/pelvis.go"}, "whole", "")
		set, err := CollectPaths(
			RoleRef{Role: SourcePath, Ref: source},
			RoleRef{Role: ConstraintPath, Ref: shared},
		)
		if err != nil {
			t.Fatal(err)
		}
		if roles := set.Roles("engine/pelvis.go"); len(roles) != 2 {
			t.Fatalf("expected both roles, got %v", roles)
		}
	})

	t.Run("an unknown role is refused", func(t *testing.T) {
		_, err := CollectPaths(RoleRef{Role: "topic", Ref: source})
		wantFault(t, err, "invalid-field")
	})
}

// ---- selectors -----------------------------------------------------------

const selectorArtifact = `{
  "results": {
    "unit": "mm",
    "population": "twelve-pose sweep",
    "denominator": "poses",
    "values": [{"pose": "p1", "value": 0.30}, {"pose": "p2", "value": 0.00}]
  },
  "a/b": 1,
  "m~n": 2,
  "big": 9007199254740993,
  "nothing": null
}`

func resolved(t *testing.T, body string) ResolvedArtifact {
	t.Helper()
	return ResolvedArtifact{
		Bytes:  []byte(body),
		SHA256: model.HashBytes([]byte(body)),
		Length: uint64(len(body)),
	}
}

func TestSelectorsReadExactJSONNumbers(t *testing.T) {
	a := resolved(t, selectorArtifact)

	// Control: the pointer finds the set and the facts the artifact states about it.
	got, err := Select(a, model.Selector{Kind: "json-pointer", Pointer: "/results"})
	if err != nil {
		t.Fatalf("control: %v", err)
	}
	if got.Kind != ReadingSet || len(got.Values) != 2 {
		t.Fatalf("control: %+v", got)
	}
	if got.Unit.State != model.Known || *got.Unit.Value != "mm" {
		t.Fatalf("control: unit %+v", got.Unit)
	}
	if got.Denominator.State != model.Known || *got.Denominator.Value != "poses" {
		t.Fatalf("control: denominator %+v", got.Denominator)
	}

	t.Run("decimal text survives", func(t *testing.T) {
		got, err := Select(a, model.Selector{Kind: "json-pointer", Pointer: "/results/values/0/value"})
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != ReadingScalar || string(*got.Scalar.Number) != "0.30" {
			// 0.30 through float64 comes back as 0.3, a different spelling of a
			// number the criterion may well be comparing exactly.
			t.Fatalf("got %+v, want the exact token 0.30", got.Scalar.Number)
		}
	})

	t.Run("integers beyond float64 survive", func(t *testing.T) {
		got, err := Select(a, model.Selector{Kind: "json-pointer", Pointer: "/big"})
		if err != nil {
			t.Fatal(err)
		}
		if string(*got.Scalar.Number) != "9007199254740993" {
			t.Fatalf("got %v, want 9007199254740993", got.Scalar.Number)
		}
	})

	t.Run("escaped pointer tokens", func(t *testing.T) {
		for pointer, want := range map[string]string{"/a~1b": "1", "/m~0n": "2"} {
			got, err := Select(a, model.Selector{Kind: "json-pointer", Pointer: pointer})
			if err != nil {
				t.Fatalf("%s: %v", pointer, err)
			}
			if got.Kind != ReadingScalar || string(*got.Scalar.Number) != want {
				t.Fatalf("%s: got %+v, want %s", pointer, got, want)
			}
		}
	})

	t.Run("the whole artifact reads as its own identity", func(t *testing.T) {
		got, err := Select(a, model.Selector{Kind: "whole"})
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != ReadingScalar || *got.Scalar.String != string(a.SHA256) {
			t.Fatalf("got %+v, want the digest", got)
		}
		if *got.Unit.Value != WholeUnit {
			t.Fatalf("a whole selector must say what its reading is: %+v", got.Unit)
		}
	})

	t.Run("absent pointers are absent, not zero", func(t *testing.T) {
		for _, pointer := range []string{"/nope", "/results/values/9/value", "/results/values/0/missing"} {
			got, err := Select(a, model.Selector{Kind: "json-pointer", Pointer: pointer})
			if err != nil {
				t.Fatalf("%s: %v", pointer, err)
			}
			if got.Kind != ReadingAbsent || strings.TrimSpace(got.Reason) == "" {
				t.Fatalf("%s: expected an absent reading with a reason, got %+v", pointer, got)
			}
		}
	})

	t.Run("null is not an observation", func(t *testing.T) {
		got, err := Select(a, model.Selector{Kind: "json-pointer", Pointer: "/nothing"})
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != ReadingAbsent {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		cases := []struct {
			name string
			body string
			sel  model.Selector
			code string
		}{
			{"duplicate key gives one pointer two values", `{"unit":"mm","unit":"cm"}`,
				model.Selector{Kind: "json-pointer", Pointer: "/unit"}, "invalid-json"},
			{"artifact is not JSON", "\x89PNG\r\n\x1a\n",
				model.Selector{Kind: "json-pointer", Pointer: ""}, "invalid-json"},
			{"trailing content", `{"a":1} {"a":2}`,
				model.Selector{Kind: "json-pointer", Pointer: "/a"}, "invalid-json"},
			{"pointer without a leading slash", `{"a":1}`,
				model.Selector{Kind: "json-pointer", Pointer: "a"}, "invalid-field"},
			{"invalid escape", `{"a":1}`,
				model.Selector{Kind: "json-pointer", Pointer: "/a~2"}, "invalid-field"},
			{"unknown selector kind", `{"a":1}`,
				model.Selector{Kind: "xpath"}, "invalid-field"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := Select(resolved(t, tc.body), tc.sel)
				wantFault(t, err, tc.code)
			})
		}
	})
}

func TestSelectPreservesMemberMetadataDeclarations(t *testing.T) {
	for _, field := range []string{"unit", "population", "denominator"} {
		for _, raw := range []string{`"different"`, `""`, `null`, `{"state":"unknown","reason":"not observed"}`} {
			t.Run(field+"/"+raw, func(t *testing.T) {
				body := `{"unit":"mm","population":"pose sweep","denominator":"poses","values":[0.0200,{"value":0.0100,"` + field + `":` + raw + `},{"value":9007199254740993}]}`
				got, err := Select(resolved(t, body), model.Selector{Kind: "json-pointer"})
				if err != nil {
					t.Fatal(err)
				}
				values, ok := got.Scalars()
				if !ok || len(values) != 3 || string(*values[1].Number) != "0.0100" || string(*values[2].Number) != "9007199254740993" {
					t.Fatalf("resolution must retain exact values despite metadata disagreement: %+v", got)
				}
				if *got.Unit.Value != "mm" || *got.Population.Value != "pose sweep" || *got.Denominator.Value != "poses" {
					t.Fatalf("member declarations overwrote set metadata: %+v", got)
				}
				if len(got.MemberMetadata) != 3 || len(got.MemberMetadata[0]) != 0 || len(got.MemberMetadata[2]) != 0 || len(got.MemberMetadata[1]) != 1 {
					t.Fatalf("member declarations must retain their positions and omitted fields: %+v", got.MemberMetadata)
				}
				declared, present := got.MemberMetadata[1][field]
				if !present {
					t.Fatal("explicit declaration was lost")
				}
				if raw == `"different"` {
					if declared.State != model.Known || declared.Value == nil || *declared.Value != "different" {
						t.Fatalf("declared value lost: %+v", declared)
					}
				} else if declared.State != model.Unknown || declared.Value != nil || !strings.Contains(declared.Reason, field) {
					t.Fatalf("explicit unavailability must remain present with a reason: %+v", declared)
				}
			})
		}
	}
}

// ---- observations --------------------------------------------------------

const resultArtifact = `{
  "results": {
    "unit": "mm",
    "population": "pose sweep",
    "denominator": "poses",
    "values": [{"pose": "p1", "value": 0.01}, {"pose": "p2", "value": 0.02}]
  }
}`

// observeFixture builds a resolver whose root holds one output artifact, plus
// the criterion and envelope that point at it.
func observeFixture(t *testing.T) (*Resolver, model.CriterionFix, model.InvocationEnvelope) {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "out/result.json", resultArtifact)

	c := testCriterion(t)
	env := testEnvelope(t, invocationA)
	env.OutputRefs = model.Availability[[]model.ArtifactRef]{
		State: model.Known,
		Value: &[]model.ArtifactRef{contentRef(resultArtifact, "application/json", []string{"out/result.json"}, "whole", "")},
	}
	return NewResolver(root), c, env
}

func TestObserveReadsTheRunsOwnArtifact(t *testing.T) {
	r, c, env := observeFixture(t)
	ctx := context.Background()

	// Control: the criterion's pointers read out of this run's output.
	o, err := r.Observe(ctx, c, env)
	if err != nil {
		t.Fatalf("control: %v", err)
	}
	if o.Unavailable != "" {
		t.Fatalf("control: %s", o.Unavailable)
	}
	if o.Result.Kind != ReadingSet || len(o.Result.Values) != 2 {
		t.Fatalf("control: %+v", o.Result)
	}
	if *o.Result.Unit.Value != "mm" {
		t.Fatalf("control: the unit must come from the artifact, got %+v", o.Result.Unit)
	}
	if size, ok := o.Population.Size(); !ok || size != 2 {
		t.Fatalf("control: population %d %v", size, ok)
	}

	t.Run("a run with no observed outputs has no reading", func(t *testing.T) {
		env := env
		env.OutputRefs = model.Availability[[]model.ArtifactRef]{State: model.Unknown, Reason: "the observer was killed"}
		o, err := r.Observe(ctx, c, env)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(o.Unavailable, "the observer was killed") {
			t.Fatalf("expected the recorded reason to survive, got %q", o.Unavailable)
		}
	})

	t.Run("outputs that do not include the selected artifact", func(t *testing.T) {
		env := env
		env.OutputRefs = model.Availability[[]model.ArtifactRef]{
			State: model.Known,
			Value: &[]model.ArtifactRef{contentRef("other", "text/plain", []string{"out/log.txt"}, "whole", "")},
		}
		o, err := r.Observe(ctx, c, env)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(o.Unavailable, "out/result.json") {
			t.Fatalf("expected the missing path to be named, got %q", o.Unavailable)
		}
	})

	t.Run("an output whose bytes are gone", func(t *testing.T) {
		empty := NewResolver(t.TempDir())
		o, err := empty.Observe(ctx, c, env)
		if err != nil {
			t.Fatal(err)
		}
		if o.Unavailable == "" {
			t.Fatal("expected missing bytes to be reported, not read as zero")
		}
	})

	t.Run("a malformed criterion is an error, not an unknown reading", func(t *testing.T) {
		bad := c
		bad.Expression.Unit = "   "
		_, err := r.Observe(ctx, bad, env)
		wantFault(t, err, "invalid-field")
	})
}

// A reading is derived from the artifact. Retyping the number into the record
// creates a second copy that can drift, and this test states the shape: the
// observation has nowhere to put an authored value.
func TestObservationCarriesNoTranscribedNumber(t *testing.T) {
	r, c, env := observeFixture(t)
	o, err := r.Observe(context.Background(), c, env)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(o.Result)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]json.RawMessage
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if _, found := back["Artifact"]; !found {
		t.Fatal("a reading must carry the digest of the bytes it came from")
	}
	if o.Result.Artifact != model.HashBytes([]byte(resultArtifact)) {
		t.Fatalf("the reading must name the exact bytes it was read from, got %s", o.Result.Artifact)
	}
}

func TestResolverRefusesAnOversizedArtifact(t *testing.T) {
	root := t.TempDir()
	const body = `{"depth":0.30}`
	writeFile(t, root, "out/result.json", body)

	// Control: the default limit resolves it.
	r := NewResolver(root)
	if _, err := r.Resolve(context.Background(), contentRef(body, "application/json", []string{"out/result.json"}, "whole", "")); err != nil {
		t.Fatalf("control: %v", err)
	}

	r.MaxBytes = 4
	_, err := r.Resolve(context.Background(), contentRef(body, "application/json", []string{"out/result.json"}, "whole", ""))
	wantFault(t, err, "unavailable")
}

// The headline property, end to end: what the pinned bytes say decides the
// comparison. Change the bytes and the verdict changes. Change what the
// selector asks for and the inference is refused rather than answered from
// something else.
func TestPinnedBytesDecideTheVerdict(t *testing.T) {
	root := t.TempDir()
	r := NewResolver(root)
	c := testCriterion(t)
	ctx := context.Background()

	observe := func(t *testing.T, body string, sel model.Selector) Observation {
		t.Helper()
		writeFile(t, root, "out/result.json", body)
		env := testEnvelope(t, invocationA)
		ref := contentRef(body, "application/json", []string{"out/result.json"}, sel.Kind, sel.Pointer)
		env.OutputRefs = model.Availability[[]model.ArtifactRef]{State: model.Known, Value: &[]model.ArtifactRef{ref}}
		o, err := r.Observe(ctx, c, env)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}

	pointer := model.Selector{Kind: "json-pointer", Pointer: "/results"}

	// Control: the clean sweep satisfies the criterion.
	wantVerdict(t, evaluate(t, c, observe(t, resultArtifact, pointer)), True, "")

	const failing = `{
  "results": {
    "unit": "mm",
    "population": "pose sweep",
    "denominator": "poses",
    "values": [{"pose": "p1", "value": 0.2}, {"pose": "p2", "value": 0.0}]
  }
}`
	wantVerdict(t, evaluate(t, c, observe(t, failing, pointer)), False, "")

	t.Run("a whole-artifact selector answers a different question", func(t *testing.T) {
		whole := c
		expr := c.Expression
		expr.ResultSelector = contentRef(criterionExample, "application/json", []string{"out/result.json"}, "whole", "")
		whole.Expression = expr
		writeFile(t, root, "out/result.json", resultArtifact)
		env := testEnvelope(t, invocationA)
		ref := contentRef(resultArtifact, "application/json", []string{"out/result.json"}, "whole", "")
		env.OutputRefs = model.Availability[[]model.ArtifactRef]{State: model.Known, Value: &[]model.ArtifactRef{ref}}
		o, err := r.Observe(ctx, whole, env)
		if err != nil {
			t.Fatal(err)
		}
		// The bytes are the same bytes that passed above. Asked for as a whole
		// artifact they read as an identity, which is not millimetres.
		wantVerdict(t, evaluate(t, whole, o), Unknown, "unit mismatch")
	})

	t.Run("bytes that no longer match the pin", func(t *testing.T) {
		env := testEnvelope(t, invocationA)
		ref := contentRef(resultArtifact, "application/json", []string{"out/result.json"}, "json-pointer", "/results")
		env.OutputRefs = model.Availability[[]model.ArtifactRef]{State: model.Known, Value: &[]model.ArtifactRef{ref}}
		writeFile(t, root, "out/result.json", failing)
		o, err := r.Observe(ctx, c, env)
		if err != nil {
			t.Fatal(err)
		}
		if o.Unavailable == "" {
			t.Fatal("edited bytes must not be read as the pinned artifact")
		}
		wantVerdict(t, evaluate(t, c, o), Unknown, "no locator holds the pinned bytes")
	})
}

func TestResolverNeedsAnAbsoluteRoot(t *testing.T) {
	root := t.TempDir()
	const body = `{"depth":0.30}`
	writeFile(t, root, "out/result.json", body)
	ref := contentRef(body, "application/json", []string{"out/result.json"}, "whole", "")

	// Control: the absolute root resolves.
	if _, err := NewResolver(root).Resolve(context.Background(), ref); err != nil {
		t.Fatalf("control: %v", err)
	}
	_, err := NewResolver("relative/root").Resolve(context.Background(), ref)
	wantFault(t, err, "invalid-field")
}

func TestSelectMetadataOmissionAndUnavailabilityStayDistinct(t *testing.T) {
	for _, field := range []string{"unit", "population", "denominator"} {
		for _, tc := range []struct {
			name, declaration string
			parent            bool
			state             model.AvailabilityState
		}{
			{"omitted", "", false, ""},
			{"inherited", "", true, model.Known},
			{"known", `"child"`, true, model.Known},
			{"null blocks inheritance", `null`, true, model.Unknown},
			{"unknown blocks inheritance", `{"state":"unknown","reason":"not measured"}`, true, model.Unknown},
			{"blank blocks inheritance", `""`, true, model.Unknown},
		} {
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				declaration, parent := "", ""
				if tc.declaration != "" {
					declaration = `,"` + field + `":` + tc.declaration
				}
				if tc.parent {
					parent = `"` + field + `":"parent",`
				}
				body := `{` + parent + `"reading":{"value":1` + declaration + `}}`
				read, err := Select(resolved(t, body), model.Selector{Kind: "json-pointer", Pointer: "/reading"})
				if err != nil {
					t.Fatal(err)
				}
				got := map[string]model.Availability[string]{"unit": read.Unit, "population": read.Population, "denominator": read.Denominator}[field]
				if got.State != tc.state {
					t.Fatalf("metadata state: %+v, want %q", got, tc.state)
				}
				if tc.state == model.Known {
					want := "parent"
					if tc.declaration != "" {
						want = "child"
					}
					if got.Value == nil || *got.Value != want {
						t.Fatalf("metadata value: %+v, want %q", got, want)
					}
				} else if got.Value != nil || !strings.Contains(got.Reason, field) {
					t.Fatalf("unobserved metadata lost its reason: %+v", got)
				}
			})
		}
	}
}

func TestSelectSiblingReadingIsNotInheritedMetadata(t *testing.T) {
	for _, field := range []string{"unit", "population", "denominator"} {
		for _, member := range []string{"value", "values"} {
			for _, explicit := range []string{"", `,"` + field + `":null`, `,"` + field + `":{"state":"unknown","reason":"not measured"}`} {
				body := `{"` + field + `":{"` + member + `":[]},"reading":{"value":1` + explicit + `}}`
				read, err := Select(resolved(t, body), model.Selector{Kind: "json-pointer", Pointer: "/reading"})
				if err != nil {
					t.Fatal(err)
				}
				got := map[string]model.Availability[string]{"unit": read.Unit, "population": read.Population, "denominator": read.Denominator}[field]
				want := model.AvailabilityState("")
				if explicit != "" {
					want = model.Unknown
				}
				if got.State != want {
					t.Fatalf("%s: got %+v, want %q", body, got, want)
				}
			}
		}
	}
}

func TestLFSPointerVersionLine(t *testing.T) {
	const version = "version https://git-lfs.github.com/spec/v1"
	digest := model.HashBytes([]byte("payload"))
	for _, tc := range []struct {
		name, first, newline string
		pointer              bool
	}{
		{"actual LF", version, "\n", true},
		{"actual CRLF", version, "\r\n", true},
		{"example suffix", version + "-example", "\n", false},
		{"version ten", version + "0", "\n", false},
		{"trailing text", version + " example", "\n", false},
		{"embedded version", "notes: " + version, "\n", false},
		{"later version line", "notes\n" + version, "\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.first + tc.newline + "oid sha256:" + string(digest) + tc.newline + "size 7" + tc.newline
			for _, store := range []bool{false, true} {
				root := t.TempDir()
				path := "out/result.txt"
				if store {
					path = DefaultArtifactDir + "/" + string(model.HashBytes([]byte(body)))
				}
				writeFile(t, root, path, body)
				ref := contentRef(body, "text/plain", []string{"out/result.txt"}, "whole", "")
				got, err := NewResolver(root).Resolve(context.Background(), ref)
				if tc.pointer {
					if err == nil || !strings.Contains(err.Error(), "LFS pointer") {
						t.Fatalf("genuine pointer accepted (store=%v): %v", store, err)
					}
				} else if err != nil || string(got.Bytes) != body || got.LFSPointer {
					t.Fatalf("ordinary text refused or changed (store=%v): %+v, %v", store, got, err)
				}
			}
		})
	}
}
