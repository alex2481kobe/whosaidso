package evidence

// Resolver tests for content pins (media type, locators, artifact store) and path
// roles. Git pins, selectors and observations belong elsewhere.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"strings"
	"testing"

	"whosaidso/internal/model"
)

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
		{"unsupported type", "arbitrary bytes", "application/x-whosaidso", "unavailable"},
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
