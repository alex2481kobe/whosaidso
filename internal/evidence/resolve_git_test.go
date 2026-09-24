package evidence

// Resolver tests for git pins and LFS pointers. Content pins, paths, selectors
// and observations belong in their own resolve_*_test.go files.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"whosaidso/internal/model"
)

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
