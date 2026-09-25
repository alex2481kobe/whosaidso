// Package store owns runtime paths and durable storage, so recorded identities
// never depend on a checkout's location or Git's common directory.
package store

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// Project keeps absolute paths at the runtime boundary. Only ID is persisted in
// packets; moving a checkout must not change its identity or lose its intake.
//
// Root is the project's home: the ledger, definitions, admission, canonical
// artifacts and the cache live under it. Checkout is the checkout the command
// was invoked in: processes run there, sources are captured from it and its
// git HEAD and dirty state are observed there. Discover sets both to the
// nearest config's directory; Open sets Root to the registered home (home.go).
type Project struct {
	ID       model.ProjectID
	Root     string
	Ledger   string
	Checkout string
	// bound records that Root came from this machine's registry, so admission
	// re-checks that binding under the ledger lock (home.go).
	bound bool
}

// ExecRoot is where processes run and git is observed: the invoking checkout,
// or Root for a Project built without one, where the two are the same place.
func (p Project) ExecRoot() string {
	if p.Checkout == "" {
		return p.Root
	}
	return p.Checkout
}

// ArtifactDir is the project-relative, slash-separated artifact store: the
// sibling "artifacts" of the ledger inside the ledger's record folder
// (.whosaidso/events beside .whosaidso/artifacts). It is derived from the configured
// ledger, never named separately, so the two cannot drift apart, and it is
// computed lexically under Root, which the ledger is already confined to.
func (p Project) ArtifactDir() string {
	rel, err := filepath.Rel(p.Root, p.Ledger)
	if err != nil {
		rel = "."
	}
	return path.Join(filepath.ToSlash(filepath.Dir(rel)), "artifacts")
}

// Discover stops at the nearest config, including an invalid one: falling back
// to a parent's identity could silently capture work for the wrong project.
func Discover(cwd string) (Project, error) {
	root, err := filepath.Abs(cwd)
	if err != nil {
		return Project{}, storeFault("io", cwd, err.Error())
	}
	info, err := os.Stat(root)
	if err != nil {
		return Project{}, storeFault("io", root, err.Error())
	}
	if !info.IsDir() {
		return Project{}, storeFault("invalid-field", root, "cwd must be a directory")
	}
	for {
		path := filepath.Join(root, "whosaidso.toml")
		project, err := configAt(root)
		if err == nil || !os.IsNotExist(err) {
			return project, err
		}
		// A dangling config is still the nearest config, not permission to use
		// a different project's identity above it.
		if _, statErr := os.Lstat(path); statErr == nil || !os.IsNotExist(statErr) {
			return Project{}, storeFault("io", path, err.Error())
		}
		parent := filepath.Dir(root)
		if parent == root {
			return Project{}, storeFault("config-not-found", cwd, "no whosaidso.toml in this directory or its parents")
		}
		root = parent
	}
}

// configAt reads the config in root itself, never a parent's. A missing file
// is returned as the os error, so callers can tell absence from a bad config.
func configAt(root string) (Project, error) {
	path := filepath.Join(root, "whosaidso.toml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Project{}, err
	}
	if err != nil {
		return Project{}, storeFault("io", path, err.Error())
	}
	values, err := parseConfig(data, path)
	if err != nil {
		return Project{}, err
	}
	ledger, err := ledgerInRoot(root, values["ledger"], path)
	if err != nil {
		return Project{}, err
	}
	return Project{ID: model.ProjectID(values["id"]), Root: root, Ledger: ledger, Checkout: root}, nil
}

// ledgerInRoot enforces that the ledger is committed with the project,
// so it may not leave the whosaidso root. Containment is asked of the filesystem by
// path segments, with symlinks resolved on both sides (macOS /tmp is a link).
func ledgerInRoot(root, declared, config string) (string, error) {
	ledger := filepath.Join(root, declared)
	outside := storeFault("config-invalid-value", config,
		fmt.Sprintf("ledger %q resolves to %q, outside the whosaidso root %q", declared, ledger, root))
	if filepath.IsAbs(declared) || filepath.VolumeName(declared) != "" {
		return "", outside
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", storeFault("io", root, err.Error())
	}
	realLedger, err := resolveExisting(ledger)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(realRoot, realLedger)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		outside.Detail = fmt.Sprintf("ledger %q resolves to %q, outside the whosaidso root %q", declared, realLedger, realRoot)
		return "", outside
	}
	return ledger, nil
}

// resolveExisting resolves symlinks through the longest existing prefix, since
// a new project's ledger does not exist yet. Only a missing name is deferred: a
// dangling link still points somewhere, so it is refused, not guessed at.
func resolveExisting(path string) (string, error) {
	tail := ""
	for {
		real, err := filepath.EvalSymlinks(path)
		if err == nil {
			return filepath.Join(real, tail), nil
		}
		if _, lstatErr := os.Lstat(path); !os.IsNotExist(lstatErr) {
			return "", storeFault("io", path, err.Error())
		}
		tail = filepath.Join(filepath.Base(path), tail)
		path = filepath.Dir(path)
	}
}

// parseConfig implements the deliberately small config language, not a TOML
// approximation that silently accepts tables, extra settings or last-key wins.
func parseConfig(data []byte, path string) (map[string]string, error) {
	if !utf8.Valid(data) {
		return nil, storeFault("config-syntax", path, "config must be UTF-8")
	}
	values := make(map[string]string, 2)
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.Trim(strings.TrimSuffix(raw, "\r"), " \t")
		at := fmt.Sprintf("%s:%d", path, i+1)
		for _, c := range strings.TrimSuffix(raw, "\r") {
			if c < 0x20 && c != '\t' || c == 0x7f {
				return nil, storeFault("config-syntax", at, "control character in a single-line assignment")
			}
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rest, err := configKey(line, at)
		if err != nil {
			return nil, err
		}
		if key != "id" && key != "ledger" {
			return nil, storeFault("config-unknown-key", at, "unknown key "+strconv.Quote(key))
		}
		if _, exists := values[key]; exists {
			return nil, storeFault("config-duplicate-key", at, "duplicate key "+key)
		}
		value, tail, err := configString(rest, at)
		if err != nil {
			return nil, err
		}
		tail = strings.Trim(tail, " \t")
		if tail != "" && !strings.HasPrefix(tail, "#") {
			return nil, storeFault("config-syntax", at, "only a comment may follow a string value")
		}
		if value == "" || strings.ContainsRune(value, 0) {
			return nil, storeFault("config-invalid-value", at, key+" must be nonempty and contain no NUL")
		}
		values[key] = value
	}
	for _, key := range []string{"id", "ledger"} {
		if _, exists := values[key]; !exists {
			return nil, storeFault("config-missing-key", path, "missing required key "+key)
		}
		if model.Blank(values[key]) {
			// Discover never applied the emptiness rule that model already
			// had, so an id of one space became a real project identity and
			// then a real inbox name.
			return nil, storeFault("config-invalid-value", path,
				"value of "+key+" renders as nothing")
		}
	}
	return values, nil
}

func configKey(line, path string) (string, string, error) {
	var key, rest string
	if line[0] == '\'' || line[0] == '"' {
		var err error
		key, rest, err = configString(line, path)
		if err != nil {
			return "", "", err
		}
	} else {
		i := 0
		for i < len(line) && (line[i] >= 'a' && line[i] <= 'z' || line[i] >= 'A' && line[i] <= 'Z' || line[i] >= '0' && line[i] <= '9' || line[i] == '_' || line[i] == '-') {
			i++
		}
		key, rest = line[:i], line[i:]
	}
	rest = strings.TrimLeft(rest, " \t")
	if key == "" || !strings.HasPrefix(rest, "=") {
		return "", "", storeFault("config-syntax", path, "expected a top-level key = string assignment; tables and dotted keys are unsupported")
	}
	return key, strings.TrimLeft(rest[1:], " \t"), nil
}

func configString(s, path string) (string, string, error) {
	bad := func(detail string) (string, string, error) {
		return "", "", storeFault("config-syntax", path, detail)
	}
	if len(s) == 0 || s[0] != '"' && s[0] != '\'' {
		return bad("expected a basic or literal single-line string")
	}
	quote := s[0]
	if strings.HasPrefix(s, strings.Repeat(string(quote), 3)) {
		return bad("multiline strings are unsupported")
	}
	var value strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		if c == quote {
			return value.String(), s[i+1:], nil
		}
		if c != '\\' || quote == '\'' {
			value.WriteByte(c)
			continue
		}
		i++
		if i == len(s) {
			return bad("unterminated escape")
		}
		switch s[i] {
		case '"', '\\':
			value.WriteByte(s[i])
		case 'b':
			value.WriteByte('\b')
		case 't':
			value.WriteByte('\t')
		case 'n':
			value.WriteByte('\n')
		case 'f':
			value.WriteByte('\f')
		case 'r':
			value.WriteByte('\r')
		case 'u', 'U':
			digits := 4
			if s[i] == 'U' {
				digits = 8
			}
			if len(s)-i-1 < digits {
				return bad("short Unicode escape")
			}
			hex := s[i+1 : i+1+digits]
			for _, c := range hex {
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
					return bad("invalid Unicode escape")
				}
			}
			n, err := strconv.ParseUint(hex, 16, 32)
			if err != nil || !utf8.ValidRune(rune(n)) {
				return bad("Unicode escape must name a Unicode scalar value")
			}
			value.WriteRune(rune(n))
			i += digits
		default:
			return bad("unsupported string escape")
		}
	}
	return bad("unterminated single-line string")
}

func storeFault(code, path, detail string) *model.Fault {
	return &model.Fault{Code: code, EventIndex: -1, Path: path, Detail: detail}
}
