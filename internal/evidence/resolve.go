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

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"datum/internal/model"
)

// DefaultArtifactDir is where admission materializes incoming blobs, keyed by
// digest. The resolver checks it after the authored locators so a reference
// stays resolvable when the lane worktree that produced it is gone.
const DefaultArtifactDir = "record/artifacts"

// DefaultMaxBytes bounds what the resolver will pull into memory. A pin naming
// something enormous is refused out loud rather than taking the process down.
const DefaultMaxBytes int64 = 64 << 20

// WholeUnit and WholePopulation are what a whole-artifact selector reports.
// A whole selector reads identity, not a measured quantity, so its reading is
// the artifact's raw digest and it says so. A criterion that declares any other
// unit against a whole selector is asking a question these bytes cannot answer.
const (
	WholeUnit       = "sha256"
	WholePopulation = "artifact"
)

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

// readContent finds a copy of the pinned bytes and verifies it. Locators are
// tried in authored order, then the deterministic artifact store. A copy that
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
		b, err := r.readFile(cd.declared)
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

func (r *Resolver) readFile(rel string) ([]byte, error) {
	if err := relativePath(rel, "path"); err != nil {
		return nil, err
	}
	full := filepath.Join(r.Root, filepath.FromSlash(rel))
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
	spec := pin.Commit + ":" + pin.Path
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

// ---- paths ---------------------------------------------------------------

// PathRole keeps the three path families apart. An evidence PNG under an output
// directory is where a result was written, not the component under test, and
// merging the two turns an output location into a false topic claim.
type PathRole string

const (
	SourcePath     PathRole = "source"
	ArtifactPath   PathRole = "artifact"
	ConstraintPath PathRole = "constraint"
)

// RoleRef is a reference plus the role the citing record gave it. The role is
// authored, never guessed from the path.
type RoleRef struct {
	Role PathRole
	Ref  model.ArtifactRef
}

// PathSet is the three families, each sorted and deduplicated, never merged.
type PathSet struct {
	Source     []string
	Artifact   []string
	Constraint []string
}

// CollectPaths gathers the paths references DECLARE, at the revision they pin.
// Historical paths are preserved exactly: a file that has since moved keeps the
// path its pin names, so a rename does not orphan the reference.
func CollectPaths(refs ...RoleRef) (PathSet, error) {
	var s PathSet
	for i, rr := range refs {
		into := map[PathRole]*[]string{SourcePath: &s.Source, ArtifactPath: &s.Artifact, ConstraintPath: &s.Constraint}[rr.Role]
		if into == nil {
			return PathSet{}, fault("invalid-field", fmt.Sprintf("refs[%d].role", i), "unknown path role: "+string(rr.Role))
		}
		*into = append(*into, declaredPaths(rr.Ref)...)
	}
	s.Source, s.Artifact, s.Constraint = tidy(s.Source), tidy(s.Artifact), tidy(s.Constraint)
	return s, nil
}

// Roles reports every role a path was cited under. A path in two families is a
// fact worth seeing, not something to silently collapse into one answer.
func (s PathSet) Roles(p string) []PathRole {
	var out []PathRole
	for _, f := range []struct {
		role  PathRole
		paths []string
	}{{SourcePath, s.Source}, {ArtifactPath, s.Artifact}, {ConstraintPath, s.Constraint}} {
		for _, have := range f.paths {
			if have == p {
				out = append(out, f.role)
				break
			}
		}
	}
	return out
}

func declaredPaths(ref model.ArtifactRef) []string {
	var out []string
	if ref.Git != nil && ref.Git.Path != "" {
		out = append(out, ref.Git.Path)
	}
	if ref.Content != nil {
		for _, l := range ref.Content.Locators {
			out = append(out, l.Path)
		}
	}
	return out
}

func tidy(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	sort.Strings(in)
	out := in[:0]
	for i, s := range in {
		if i == 0 || in[i-1] != s {
			out = append(out, s)
		}
	}
	return out
}

func (r *Resolver) checkPaths(ref model.ArtifactRef) error {
	if ref.Git != nil {
		if err := relativePath(ref.Git.Path, "artifact.git.path"); err != nil {
			return err
		}
	}
	if ref.Content != nil {
		for i, l := range ref.Content.Locators {
			if err := relativePath(l.Path, fmt.Sprintf("artifact.content.locators[%d].path", i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// relativePath refuses anything that could read outside the project root or be
// mistaken for a git option or revision expression.
func relativePath(s, at string) error {
	switch {
	case strings.TrimSpace(s) == "":
		return fault("invalid-field", at, "blank path")
	case strings.ContainsAny(s, "\\\x00:"):
		return fault("invalid-field", at, "path contains a separator, colon or NUL")
	case strings.HasPrefix(s, "/"), strings.HasPrefix(s, "-"), strings.HasPrefix(s, "./"):
		return fault("invalid-field", at, "expected a plain project-relative path")
	}
	for _, part := range strings.Split(s, "/") {
		if part == ".." {
			return fault("invalid-field", at, "path escapes the project root")
		}
	}
	return nil
}

// ---- selectors -----------------------------------------------------------

// ReadingKind says what the selector found. Absent is a first-class answer and
// carries the reason, because an absent reading is information we do not have
// rather than a value to fill in.
type ReadingKind string

const (
	ReadingScalar ReadingKind = "scalar"
	ReadingSet    ReadingKind = "set"
	ReadingAbsent ReadingKind = "absent"
)

// Reading is what a selector actually read out of pinned bytes, with the facts
// the artifact stated about it. Unit, Population and Denominator come from the
// artifact, never from the record: a unit retyped next to a number is a second
// copy that can drift from the measurement it labels.
// Metadata's zero State means omitted; Unknown means declared unavailable.
type Reading struct {
	Artifact    model.Digest
	Selector    model.Selector
	Kind        ReadingKind
	Scalar      model.Scalar
	Values      []model.Scalar
	Unit        model.Availability[string]
	Population  model.Availability[string]
	Denominator model.Availability[string]
	Reason      string
	// MemberMetadata parallels Values; absent keys were not declared by the member.
	MemberMetadata []map[string]model.Availability[string]
}

// Scalars returns the values to compare. An absent reading has none, and says so.
func (r Reading) Scalars() ([]model.Scalar, bool) {
	switch r.Kind {
	case ReadingScalar:
		return []model.Scalar{r.Scalar}, true
	case ReadingSet:
		return r.Values, true
	}
	return nil, false
}

// Size is the cardinality a denominator is measured against.
func (r Reading) Size() (int, bool) {
	v, ok := r.Scalars()
	return len(v), ok
}

// Select applies a selector to resolved bytes.
//
// Numbers keep their exact decimal text the whole way through: 0.30 stays
// "0.30" and 9007199254740993 stays itself. Passing a threshold through float64
// would make a comparison mean something slightly different from what the
// criterion says, which is the one thing a threshold cannot afford.
func Select(a ResolvedArtifact, sel model.Selector) (Reading, error) {
	out := Reading{Artifact: a.SHA256, Selector: sel}
	switch sel.Kind {
	case "whole":
		// The reading of a whole artifact is its identity. That is a real,
		// checkable value, and it is honestly labelled as a digest rather than
		// dressed up as a measurement of the thing inside.
		out.Kind = ReadingScalar
		out.Scalar = stringScalar(string(a.SHA256))
		out.Unit = known(WholeUnit)
		out.Population = known(WholePopulation)
		out.Denominator = known(WholePopulation)
		return out, nil
	case "json-pointer":
	default:
		return Reading{}, fault("invalid-field", "selector.kind", "unknown selector kind: "+sel.Kind)
	}

	root, err := decodeJSON(a.Bytes)
	if err != nil {
		return Reading{}, err
	}
	value, parent, ok, err := pointerValue(root, sel.Pointer)
	if err != nil {
		return Reading{}, err
	}
	if !ok {
		out.Kind = ReadingAbsent
		out.Reason = "the artifact has no value at pointer " + quote(sel.Pointer)
		return out, nil
	}

	chain := []map[string]any{}
	if obj, isObj := value.(map[string]any); isObj {
		chain = append(chain, obj)
	}
	if parent != nil {
		chain = append(chain, parent)
	}
	out.Unit, out.Population, out.Denominator = metaFrom(chain...)

	switch v := value.(type) {
	case map[string]any:
		inner, found := v["value"]
		if !found {
			if vals, haveList := v["values"]; haveList {
				inner = vals
				found = true
			}
		}
		if !found {
			out.Kind = ReadingAbsent
			out.Reason = "the selected object states no value or values member"
			return out, nil
		}
		return finish(out, inner)
	default:
		return finish(out, v)
	}
}

func finish(out Reading, v any) (Reading, error) {
	switch t := v.(type) {
	case []any:
		out.Kind = ReadingSet
		out.Values = make([]model.Scalar, 0, len(t))
		out.MemberMetadata = make([]map[string]model.Availability[string], len(t))
		for i, e := range t {
			if obj, ok := e.(map[string]any); ok {
				unit, population, denominator := metaFrom(obj)
				declared := map[string]model.Availability[string]{"unit": unit, "population": population, "denominator": denominator}
				for key := range declared {
					if _, present := obj[key]; !present {
						delete(declared, key)
					}
				}
				out.MemberMetadata[i] = declared
				inner, found := obj["value"]
				if !found {
					out.Kind, out.Values = ReadingAbsent, nil
					out.Reason = fmt.Sprintf("member %d of the selected set states no value", i)
					return out, nil
				}
				e = inner
			}
			s, ok := scalarOf(e)
			if !ok {
				out.Kind, out.Values = ReadingAbsent, nil
				out.Reason = fmt.Sprintf("member %d of the selected set is not a comparable value", i)
				return out, nil
			}
			out.Values = append(out.Values, s)
		}
		return out, nil
	default:
		s, ok := scalarOf(t)
		if !ok {
			out.Kind = ReadingAbsent
			out.Reason = "the selected value is not a comparable value"
			return out, nil
		}
		out.Kind, out.Scalar = ReadingScalar, s
		return out, nil
	}
}

func scalarOf(v any) (model.Scalar, bool) {
	switch t := v.(type) {
	case json.Number:
		n := t
		return model.Scalar{Type: "number", Number: &n}, true
	case string:
		s := t
		return model.Scalar{Type: "string", String: &s}, true
	case bool:
		b := t
		return model.Scalar{Type: "bool", Bool: &b}, true
	}
	return model.Scalar{}, false
}

func stringScalar(s string) model.Scalar { return model.Scalar{Type: "string", String: &s} }

func numberScalar(n json.Number) model.Scalar { return model.Scalar{Type: "number", Number: &n} }

func known(s string) model.Availability[string] {
	v := s
	return model.Availability[string]{State: model.Known, Value: &v}
}

func unknown(reason string) model.Availability[string] {
	return model.Availability[string]{State: model.Unknown, Reason: reason}
}

// metaFrom reads what the artifact says about its own numbers, looking at the
// object holding them and then at its parent. Nothing is defaulted: an artifact
// that does not state its unit leaves the unit unknown, and a criterion cannot
// be evaluated against a number whose unit nobody recorded.
func metaFrom(objs ...map[string]any) (unit, population, denominator model.Availability[string]) {
	pick := func(key, missing string) model.Availability[string] {
		for i, o := range objs {
			raw, ok := o[key]
			if !ok {
				continue
			}
			if obj, ok := raw.(map[string]any); ok && i > 0 {
				// A parent's sibling reading is not an inherited label.
				_, value := obj["value"]
				_, values := obj["values"]
				_, state := obj["state"]
				if !state && (value || values) {
					continue
				}
			}
			s, isText := raw.(string)
			if !isText || strings.TrimSpace(s) == "" {
				return unknown("the artifact states a blank " + key)
			}
			return known(s)
		}
		return model.Availability[string]{Reason: missing}
	}
	return pick("unit", "the artifact does not state the unit of this reading"),
		pick("population", "the artifact does not state which population this reading covers"),
		pick("denominator", "the artifact does not state the denominator of this reading")
}

func quote(s string) string { return strconv.Quote(s) }

// ---- strict JSON ---------------------------------------------------------

// decodeJSON keeps exact number text and refuses duplicate keys. A duplicate key
// would give one pointer two values, and whichever one won would be an accident
// of the parser rather than something the artifact says.
func decodeJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	v, err := parseValue(dec, "$")
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fault("invalid-json", "$", "trailing content after the top-level value")
	}
	return v, nil
}

func parseValue(dec *json.Decoder, at string) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, fault("invalid-json", at, "artifact is not valid JSON")
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return parseObject(dec, at)
		case '[':
			return parseArray(dec, at)
		}
		return nil, fault("invalid-json", at, "unexpected delimiter")
	default:
		return tok, nil
	}
}

func parseObject(dec *json.Decoder, at string) (any, error) {
	obj := map[string]any{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, fault("invalid-json", at, "artifact is not valid JSON")
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fault("invalid-json", at, "object key is not a string")
		}
		if _, dup := obj[key]; dup {
			return nil, fault("invalid-json", at+"/"+key, "duplicate key: one pointer would have two values")
		}
		v, err := parseValue(dec, at+"/"+key)
		if err != nil {
			return nil, err
		}
		obj[key] = v
	}
	if _, err := dec.Token(); err != nil {
		return nil, fault("invalid-json", at, "unterminated object")
	}
	return obj, nil
}

func parseArray(dec *json.Decoder, at string) (any, error) {
	arr := []any{}
	for dec.More() {
		v, err := parseValue(dec, fmt.Sprintf("%s/%d", at, len(arr)))
		if err != nil {
			return nil, err
		}
		arr = append(arr, v)
	}
	if _, err := dec.Token(); err != nil {
		return nil, fault("invalid-json", at, "unterminated array")
	}
	return arr, nil
}

// pointerValue walks an RFC 6901 pointer. The empty pointer is the document
// root. Escapes are unescaped ~1 before ~0, because doing it the other way
// turns "~01" into "/" instead of the "~1" the author wrote.
func pointerValue(root any, ptr string) (value any, parent map[string]any, ok bool, err error) {
	if ptr == "" {
		return root, nil, true, nil
	}
	if !strings.HasPrefix(ptr, "/") {
		return nil, nil, false, fault("invalid-field", "selector.pointer", "JSON pointer must begin with a slash")
	}
	cur := root
	for _, raw := range strings.Split(ptr[1:], "/") {
		token, err := unescapeToken(raw)
		if err != nil {
			return nil, nil, false, err
		}
		switch node := cur.(type) {
		case map[string]any:
			next, found := node[token]
			if !found {
				return nil, nil, false, nil
			}
			parent, cur = node, next
		case []any:
			i, err := arrayIndex(token)
			if err != nil || i >= len(node) {
				return nil, nil, false, nil
			}
			parent, cur = nil, node[i]
		default:
			return nil, nil, false, nil
		}
	}
	return cur, parent, true, nil
}

func unescapeToken(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '~' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) {
			return "", fault("invalid-field", "selector.pointer", "pointer ends in an incomplete escape")
		}
		switch s[i+1] {
		case '0':
			b.WriteByte('~')
		case '1':
			b.WriteByte('/')
		default:
			return "", fault("invalid-field", "selector.pointer", "invalid pointer escape ~"+string(s[i+1]))
		}
		i++
	}
	return b.String(), nil
}

func arrayIndex(s string) (int, error) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, errors.New("not a canonical index")
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, errors.New("not a canonical index")
		}
	}
	return strconv.Atoi(s)
}

// ---- observations --------------------------------------------------------

// Observation is one sealed invocation's contribution to a criterion family.
// Every measurable thing in it was read out of the artifact that invocation
// produced. There is no field here for a number an author reports separately,
// which is the point: there is nothing to transcribe and therefore nothing to
// drift.
type Observation struct {
	InvocationRef      model.InvocationRef
	CriterionRef       model.Availability[model.CriterionRef]
	Outcome            model.Availability[model.ProcessOutcome]
	ConfigEffective    model.Availability[map[string]model.Availability[model.Scalar]]
	ConditionsObserved model.Availability[map[string]model.Availability[model.Scalar]]
	Visual             model.Availability[model.VisualObservation]
	Result             Reading
	Population         Reading
	// Unavailable is why this observation has no reading at all. Nonblank means
	// the evaluation must refuse, never that the value was zero.
	Unavailable string
}

// Observe reads the frozen criterion's selectors out of the artifact THIS
// invocation produced.
//
// The criterion is fixed before the run, so its selector pins an artifact that
// states where a result lives. The bytes read are always the run's own output,
// matched by the declared path: reading a criterion's pinned example and calling
// it this run's result would report a number no instrument produced here.
func (r *Resolver) Observe(ctx context.Context, c model.CriterionFix, env model.InvocationEnvelope) (Observation, error) {
	if err := model.ValidateSchema(c); err != nil {
		return Observation{}, err
	}
	o := Observation{
		InvocationRef:      model.InvocationRef{Project: env.ExecutionSourceIdentity.Project, InvocationID: env.InvocationID},
		CriterionRef:       env.CriterionRef,
		Outcome:            env.Outcome,
		ConfigEffective:    env.ConfigEffective,
		ConditionsObserved: env.ConditionsObserved,
		Visual:             env.Visual,
	}
	if env.OutputRefs.State != model.Known || env.OutputRefs.Value == nil {
		o.Unavailable = "the invocation reports no observed outputs: " + env.OutputRefs.Reason
		return o, nil
	}
	outs := *env.OutputRefs.Value

	result, why, err := r.readSelector(ctx, outs, c.Expression.ResultSelector)
	if err != nil {
		return Observation{}, err
	}
	if why != "" {
		o.Unavailable = "result selector: " + why
		return o, nil
	}
	o.Result = result

	population, why, err := r.readSelector(ctx, outs, c.Expression.Population.Selector)
	if err != nil {
		return Observation{}, err
	}
	if why != "" {
		o.Unavailable = "population selector: " + why
		return o, nil
	}
	o.Population = population
	return o, nil
}

// readSelector finds the output this selector names and reads it. A missing or
// unreadable artifact comes back as a reason, not an error, because the family
// still has to be evaluated with that observation counted and refused.
func (r *Resolver) readSelector(ctx context.Context, outs []model.ArtifactRef, want model.ArtifactRef) (Reading, string, error) {
	match, why := matchOutput(outs, want)
	if why != "" {
		return Reading{}, why, nil
	}
	// The pin comes from the run's own output. The pointer comes from the
	// frozen criterion. That is what keeps a late edit to the criterion from
	// silently re-aiming at different bytes.
	ref := match
	ref.Selector = want.Selector
	resolved, err := r.Resolve(ctx, ref)
	if err != nil {
		if code := faultCode(err); code == "unavailable" || code == "io" {
			return Reading{}, err.Error(), nil
		}
		return Reading{}, "", err
	}
	reading, err := Select(resolved, want.Selector)
	if err != nil {
		return Reading{}, err.Error(), nil
	}
	return reading, "", nil
}

// matchOutput pairs a criterion selector with an output by the path each
// declares. Ambiguity is refused rather than resolved by position, since which
// of two same-path outputs was meant is not something order can answer.
func matchOutput(outs []model.ArtifactRef, want model.ArtifactRef) (model.ArtifactRef, string) {
	wanted := map[string]bool{}
	for _, p := range declaredPaths(want) {
		wanted[p] = true
	}
	if len(wanted) == 0 {
		return model.ArtifactRef{}, "the criterion selector declares no path to match an output against"
	}
	var hits []model.ArtifactRef
	for _, out := range outs {
		for _, p := range declaredPaths(out) {
			if wanted[p] {
				hits = append(hits, out)
				break
			}
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], ""
	case 0:
		paths := make([]string, 0, len(wanted))
		for p := range wanted {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		return model.ArtifactRef{}, "no output of this invocation declares " + strings.Join(paths, " or ")
	default:
		return model.ArtifactRef{}, fmt.Sprintf("%d outputs declare the selected path", len(hits))
	}
}

func fault(code, at, detail string) *model.Fault {
	return &model.Fault{Code: code, EventIndex: -1, Path: at, Detail: detail}
}

func faultCode(err error) string {
	var f *model.Fault
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
}
