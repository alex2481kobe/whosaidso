package evidence

// Verified content reads, Git object access, and LFS pointer parsing live here.
// Pin-resolution orchestration, selectors, and invocation observations do not.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// readContent finds a copy of the pinned bytes and verifies it. Locators are
// tried in authored order, then the deterministic artifact store (or the
// Staged bytes standing in for its copy). A copy that
// does not hash to the pin is not the artifact, so it is skipped and reported
// rather than returned.
func (r *Resolver) readContent(c model.ContentPin) ([]byte, Origin, string, error) {
	type candidate struct {
		declared string
		origin   Origin
	}
	cands := make([]candidate, 0, len(c.Locators)+1)
	for _, l := range c.Locators {
		cands = append(cands, candidate{declared: l.Path, origin: OriginLocator})
	}
	cands = append(cands, candidate{declared: path.Join(r.artifactDir(), string(c.SHA256)), origin: OriginArtifactStore})

	var notes []string
	for _, cd := range cands {
		b, err := r.readCopy(cd.declared, cd.origin, c.SHA256)
		if err != nil {
			notes = append(notes, cd.declared+": "+err.Error())
			continue
		}
		if uint64(len(b)) != c.Length {
			notes = append(notes, fmt.Sprintf("%s: %d bytes, pin declares %d", cd.declared, len(b), c.Length))
			continue
		}
		if sum := model.HashBytes(b); sum != c.SHA256 {
			notes = append(notes, fmt.Sprintf("%s: hashes to %s", cd.declared, sum))
			continue
		}
		// Once identity matches, another copy cannot repair a false claim
		// about these bytes, so these refusals do not fall through to fallback.
		if ptr, ok := parseLFSPointer(b); ok {
			return nil, "", "", fault("unavailable", "artifact.content",
				"the content pin names an LFS pointer, not its payload. The payload needs a content pin naming oid "+ptr.oid)
		}
		if err := checkMediaType(b, c.MediaType); err != nil {
			return nil, "", "", err
		}
		return b, cd.origin, cd.declared, nil
	}
	return nil, "", "", fault("unavailable", "artifact.content",
		"no locator holds the pinned bytes ("+strings.Join(notes, " | ")+")")
}

// checkMediaType checks only a bounded set of byte-level format properties:
// PNG's signature, UTF-8 JSON syntax, and UTF-8 plain text without binary control
// characters. Octet-stream makes no narrower claim than arbitrary bytes.
// BLIND TO: PNG chunk/pixel integrity, JSON schema or meaning (including duplicate
// keys), and text's meaning or intended format. A signature is not a full PNG
// validation, and readable text does not identify a specific text-based format.
// Unsupported media types and parameters are unknown, so they are refused with
// a reason rather than inferred from a filename or a best-effort MIME guess.
func checkMediaType(b []byte, declared string) error {
	media, params, err := mime.ParseMediaType(declared)
	if err != nil {
		return fault("invalid-field", "artifact.content.media_type", "invalid media type: "+err.Error())
	}
	if len(params) != 0 {
		return fault("unavailable", "artifact.content.media_type", "media type parameters cannot be verified: "+declared)
	}
	var matches bool
	switch media {
	case "application/octet-stream":
		matches = true
	case "image/png":
		matches = bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n"))
	case "application/json":
		matches = utf8.Valid(b) && json.Valid(b)
	case "text/plain":
		matches = utf8.Valid(b)
		for _, c := range b {
			if (c < 0x20 && c != '\t' && c != '\n' && c != '\r' && c != '\f') || c == 0x7f {
				matches = false
				break
			}
		}
	default:
		return fault("unavailable", "artifact.content.media_type", "no byte-level check is available for media type "+declared)
	}
	if !matches {
		return fault("conflict", "artifact.content.media_type", "the pinned bytes do not satisfy the byte-level format check for "+declared)
	}
	return nil
}

func (r *Resolver) artifactDir() string {
	if r.ArtifactDir == "" {
		return DefaultArtifactDir
	}
	return r.ArtifactDir
}

func (r *Resolver) maxBytes() int64 {
	if r.MaxBytes <= 0 {
		return DefaultMaxBytes
	}
	return r.MaxBytes
}

// readCopy reads one candidate copy. The store's copy of a staged digest is
// the staged bytes, held to the same size limit as a file in the store.
func (r *Resolver) readCopy(declared string, origin Origin, digest model.Digest) ([]byte, error) {
	staged, ok := r.Staged[digest]
	if !ok || origin != OriginArtifactStore {
		return r.readFile(declared)
	}
	if int64(len(staged)) > r.maxBytes() {
		return nil, fmt.Errorf("%d bytes exceeds the resolver limit of %d", len(staged), r.maxBytes())
	}
	return staged, nil
}

func (r *Resolver) readFile(rel string) ([]byte, error) {
	if err := relativePath(rel, "path"); err != nil {
		return nil, err
	}
	full, err := r.containedPath(rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return nil, errors.New("not readable")
	}
	if info.IsDir() {
		return nil, errors.New("is a directory")
	}
	if info.Size() > r.maxBytes() {
		return nil, fmt.Errorf("%d bytes exceeds the resolver limit of %d", info.Size(), r.maxBytes())
	}
	return os.ReadFile(full)
}

// ---- git -----------------------------------------------------------------

// gitBlob reads one path at one commit out of the object database. It never
// consults the working tree or the index, so an edited, staged or deleted file
// changes nothing about what a pin resolves to.
func (r *Resolver) gitBlob(ctx context.Context, pin model.GitPin) ([]byte, error) {
	run := r.Git
	if run == nil {
		run = ExecGit
	}
	format, err := run(ctx, r.Root, "rev-parse", "--show-object-format")
	if err != nil {
		return nil, fault("unavailable", "artifact.git", "the repository could not be read: "+err.Error())
	}
	if got := strings.TrimSpace(string(format)); got != pin.ObjectFormat {
		// A 40-character name means one thing in a sha1 repository and is not a
		// name at all in a sha256 one. Guessing here would resolve a reference
		// to bytes nobody pinned.
		return nil, fault("invalid-field", "artifact.git.object_format",
			fmt.Sprintf("reference declares %s, the repository uses %s", pin.ObjectFormat, got))
	}
	kind, err := run(ctx, r.Root, "cat-file", "-t", pin.Commit)
	if err != nil {
		return nil, fault("unavailable", "artifact.git.commit", "commit not found in this repository: "+pin.Commit)
	}
	if got := strings.TrimSpace(string(kind)); got != "commit" {
		return nil, fault("invalid-field", "artifact.git.commit", "pinned object is a "+got+", not a commit")
	}
	// Every authored path is relative to the whosaidso root, which may sit below the
	// repository's top level. "<commit>:<path>" is read from the top level;
	// "<commit>:./<path>" is read from the directory git runs in, the root. With
	// ".." refused by relativePath, the lookup cannot leave the root's subtree.
	spec := pin.Commit + ":./" + pin.Path
	kind, err = run(ctx, r.Root, "cat-file", "-t", spec)
	if err != nil {
		return nil, fault("unavailable", "artifact.git.path",
			"path does not exist at the pinned commit: "+pin.Path)
	}
	if got := strings.TrimSpace(string(kind)); got != "blob" {
		return nil, fault("invalid-field", "artifact.git.path", "pinned path is a "+got+", not a file")
	}
	b, err := run(ctx, r.Root, "cat-file", "blob", spec)
	if err != nil {
		return nil, fault("io", "artifact.git", "blob could not be read: "+err.Error())
	}
	if int64(len(b)) > r.maxBytes() {
		return nil, fault("unavailable", "artifact.git",
			fmt.Sprintf("%d bytes exceeds the resolver limit of %d", len(b), r.maxBytes()))
	}
	return b, nil
}

// ExecGit runs git in dir and returns its stdout bytes unchanged.
func ExecGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var out, errs bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errs
	// Configuration must not change what the object database answers, and
	// nothing here may sit waiting on a credential prompt.
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
	)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errs.String()))
	}
	return out.Bytes(), nil
}

type lfsPointer struct {
	oid  string
	size uint64
}

// parseLFSPointer recognises the committed stand-in for a payload git does not
// store. It is small and rigid on purpose: a real artifact that merely mentions
// LFS must not be mistaken for a pointer.
func parseLFSPointer(b []byte) (lfsPointer, bool) {
	const version = "version https://git-lfs.github.com/spec/v1"
	first, _, _ := bytes.Cut(b, []byte("\n"))
	if len(b) > 1024 || !bytes.Equal(bytes.TrimSuffix(first, []byte("\r")), []byte(version)) {
		return lfsPointer{}, false
	}
	var p lfsPointer
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		switch key {
		case "oid":
			p.oid = strings.TrimPrefix(value, "sha256:")
		case "size":
			n, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return lfsPointer{}, false
			}
			p.size = n
		}
	}
	if !model.ValidDigest(model.Digest(p.oid)) {
		return lfsPointer{}, false
	}
	return p, true
}
