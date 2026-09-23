// Package evidence turns a reference into the exact bytes it names, and a frozen
// criterion into a verdict over what those bytes actually say.
//
// Two rules shape everything here. Bytes are fetched by their pin and never from
// the current working tree, because the question a record asks is what was
// measured, not what happens to be on disk now. And every reading is derived
// from the artifact itself, never from a number an author typed into a record a
// second time, because the second copy can drift from the thing it describes
// while still looking like a true value.
//
// This package reads the filesystem and runs git. It holds no state about a
// ledger, admits nothing, and decides no status.
package evidence

// Resolver configuration, pinned-byte resolution, corroboration, and faults live here.
// Storage reads, path collection, selectors, and invocation observations do not.

import (
	"context"
	"fmt"
	"path/filepath"

	"datum/internal/model"
)

// DefaultArtifactDir is where admission materializes incoming blobs, keyed by
// digest. The resolver checks it after the authored locators so a reference
// stays resolvable when the lane worktree that produced it is gone.
const DefaultArtifactDir = "record/artifacts"

// DefaultMaxBytes bounds what the resolver will pull into memory. A pin naming
// something enormous is refused out loud rather than taking the process down.
const DefaultMaxBytes int64 = 64 << 20

// GitRunner is the process seam. Tests pass their own, production passes ExecGit.
type GitRunner func(ctx context.Context, dir string, args ...string) ([]byte, error)

// Resolver fetches pinned bytes. Root is the absolute datum root that every
// authored path is relative to. No authored record ever holds an absolute path.
type Resolver struct {
	Root        string
	ArtifactDir string
	Git         GitRunner
	MaxBytes    int64
}

// NewResolver builds the production resolver for one project root.
func NewResolver(root string) *Resolver {
	return &Resolver{Root: root, ArtifactDir: DefaultArtifactDir, Git: ExecGit, MaxBytes: DefaultMaxBytes}
}

// Origin records where the bytes came from, so a reader can tell a committed
// object from a working copy from a materialized blob.
type Origin string

const (
	OriginGit           Origin = "git-object"
	OriginLocator       Origin = "locator"
	OriginArtifactStore Origin = "artifact-store"
)

// ResolvedArtifact is verified bytes plus the provenance that verified them.
// DeclaredPath is the path as AUTHORED, kept even when the file has since moved:
// a pin names bytes at a revision, and rewriting its path to today's location
// would quietly answer a different question.
type ResolvedArtifact struct {
	Ref          model.ArtifactRef
	Bytes        []byte
	SHA256       model.Digest
	Length       uint64
	MediaType    string
	Origin       Origin
	DeclaredPath string
	Corroborated bool // both pins were present and were checked against each other
	LFSPointer   bool // git held a pointer, so the payload came from the content pin
}

// Resolve fetches the bytes a reference names and verifies them against it.
//
// When both pins are present neither is trusted: the bytes are fetched and the
// two pins are checked against each other. They are corroboration, not two
// independently authored truths.
func (r *Resolver) Resolve(ctx context.Context, ref model.ArtifactRef) (ResolvedArtifact, error) {
	if !filepath.IsAbs(r.Root) {
		// Authored paths are relative to a declared root. Resolving them against
		// whatever directory the process happens to be in would answer about
		// different bytes without anyone noticing.
		return ResolvedArtifact{}, fault("invalid-field", "resolver.root", "the resolver needs an absolute project root")
	}
	if err := model.ValidateArtifactRef(ref, "artifact"); err != nil {
		return ResolvedArtifact{}, err
	}
	if err := r.checkPaths(ref); err != nil {
		return ResolvedArtifact{}, err
	}
	if ref.Kind == "git" {
		return r.resolveGitFirst(ctx, ref)
	}
	return r.resolveContentFirst(ctx, ref)
}

func (r *Resolver) resolveGitFirst(ctx context.Context, ref model.ArtifactRef) (ResolvedArtifact, error) {
	raw, err := r.gitBlob(ctx, *ref.Git)
	if err != nil {
		return ResolvedArtifact{}, err
	}
	out := ResolvedArtifact{
		Ref:          ref,
		Origin:       OriginGit,
		DeclaredPath: ref.Git.Path,
	}
	if ptr, ok := parseLFSPointer(raw); ok {
		// A pointer file describes the payload. It is not the payload, and
		// measuring it measures the pointer.
		if ref.Content == nil {
			return ResolvedArtifact{}, fault("unavailable", "artifact.git",
				"git holds an LFS pointer for this path. The payload needs a content pin naming oid "+ptr.oid)
		}
		if ptr.oid != string(ref.Content.SHA256) || ptr.size != ref.Content.Length {
			return ResolvedArtifact{}, fault("conflict", "artifact.content",
				fmt.Sprintf("the LFS pointer names %s at %d bytes, the content pin names %s at %d",
					ptr.oid, ptr.size, ref.Content.SHA256, ref.Content.Length))
		}
		payload, origin, at, err := r.readContent(*ref.Content)
		if err != nil {
			return ResolvedArtifact{}, err
		}
		out.Bytes, out.SHA256, out.Length = payload, ref.Content.SHA256, ref.Content.Length
		out.MediaType, out.Origin, out.DeclaredPath = ref.Content.MediaType, origin, at
		out.Corroborated, out.LFSPointer = true, true
		return out, nil
	}
	out.Bytes = raw
	out.SHA256 = model.HashBytes(raw)
	out.Length = uint64(len(raw))
	if ref.Content != nil {
		if err := agree(out.SHA256, out.Length, *ref.Content); err != nil {
			return ResolvedArtifact{}, err
		}
		// Agreement with the declaration does not establish that the second
		// pin is readable. Check its own locators/store before corroborating.
		if _, _, _, err := r.readContent(*ref.Content); err != nil {
			return ResolvedArtifact{}, err
		}
		out.MediaType = ref.Content.MediaType
		out.Corroborated = true
	}
	return out, nil
}

func (r *Resolver) resolveContentFirst(ctx context.Context, ref model.ArtifactRef) (ResolvedArtifact, error) {
	payload, origin, at, err := r.readContent(*ref.Content)
	if err != nil {
		return ResolvedArtifact{}, err
	}
	out := ResolvedArtifact{
		Ref:          ref,
		Bytes:        payload,
		SHA256:       ref.Content.SHA256,
		Length:       ref.Content.Length,
		MediaType:    ref.Content.MediaType,
		Origin:       origin,
		DeclaredPath: at,
	}
	if ref.Git != nil {
		raw, err := r.gitBlob(ctx, *ref.Git)
		if err != nil {
			// A corroborating pin that cannot be read leaves the two pins
			// unchecked, and unchecked is not agreement.
			return ResolvedArtifact{}, fault("unavailable", "artifact.git",
				"the corroborating git pin could not be read: "+err.Error())
		}
		if ptr, ok := parseLFSPointer(raw); ok {
			if ptr.oid != string(ref.Content.SHA256) || ptr.size != ref.Content.Length {
				return ResolvedArtifact{}, fault("conflict", "artifact.git",
					fmt.Sprintf("the LFS pointer names %s at %d bytes, the content pin names %s at %d",
						ptr.oid, ptr.size, ref.Content.SHA256, ref.Content.Length))
			}
			out.LFSPointer = true
		} else if err := agree(model.HashBytes(raw), uint64(len(raw)), *ref.Content); err != nil {
			return ResolvedArtifact{}, err
		}
		out.Corroborated = true
	}
	return out, nil
}

// agree compares real bytes with what a content pin declares. The git object id
// is deliberately not in this comparison: git hashes "blob <len>\0" + contents,
// so a git OID and a raw SHA-256 of the same file are different strings, and
// comparing them would refuse every honest reference.
func agree(sum model.Digest, length uint64, c model.ContentPin) error {
	if sum != c.SHA256 {
		return fault("conflict", "artifact.content.sha256",
			fmt.Sprintf("the pinned bytes hash to %s, the content pin declares %s", sum, c.SHA256))
	}
	if length != c.Length {
		return fault("conflict", "artifact.content.length",
			fmt.Sprintf("the pinned bytes are %d long, the content pin declares %d", length, c.Length))
	}
	return nil
}

func fault(code, at, detail string) *model.Fault {
	return &model.Fault{Code: code, EventIndex: -1, Path: at, Detail: detail}
}
