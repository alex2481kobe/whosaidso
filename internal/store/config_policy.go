package store

// A project's optional settings in whosaidso.toml, beside the two required
// keys (config.go): each has a default, so a config without them reads as
// before. They travel with the repository, so every checkout and machine
// applies the same ones.

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/buildinfo"
)

// policyKeys are the optional keys a config may set.
var policyKeys = map[string]bool{"stale_after": true, "self_admission": true, "team": true, "requires": true}

// DefaultStaleAfter is how long a claim may go without admitted activity
// before reads flag it a takeover candidate, when the config sets none.
const DefaultStaleAfter = 24 * time.Hour

// Policy is a project's optional settings.
type Policy struct {
	// requires is validated at config load, before any command may use it.
	requires string
	// Team names actors to watch for idle work queues; it never assigns work.
	Team []string
	// StaleAfter: a live claim with no admitted activity for longer is
	// flagged as a takeover candidate. Flagged only: releasing it needs the
	// owner's approval (an authority on the receipt).
	StaleAfter time.Duration
	// RefuseSelfAdmission (self_admission = 'refuse'): admission accepts a
	// packet only when its author and the admitter are two known actors.
	// The default, 'allow', records self-admission and refuses nothing.
	RefuseSelfAdmission bool
}

func policyOf(values map[string]string, path string) (Policy, error) {
	p := Policy{StaleAfter: DefaultStaleAfter}
	if raw, ok := values["requires"]; ok {
		if _, valid := parseVersion(raw); !valid {
			return Policy{}, storeFault("config-invalid-value", path, fmt.Sprintf("requires must be a semantic version such as 'v0.1.8', got %q", raw))
		}
		p.requires = raw
	}
	if raw, ok := values["stale_after"]; ok {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return Policy{}, storeFault("config-invalid-value", path, fmt.Sprintf("stale_after must be a positive duration such as '24h' or '90m', got %q", raw))
		}
		p.StaleAfter = d
	}
	switch raw := values["self_admission"]; raw {
	case "", "allow":
	case "refuse":
		p.RefuseSelfAdmission = true
	default:
		return Policy{}, storeFault("config-invalid-value", path, fmt.Sprintf("self_admission must be 'allow' or 'refuse', got %q", raw))
	}
	if raw, ok := values["team"]; ok {
		seen := map[string]bool{}
		for _, name := range strings.Split(raw, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				return Policy{}, storeFault("config-invalid-value", path, "team must contain nonempty comma-separated actor names")
			}
			if !seen[name] {
				p.Team = append(p.Team, name)
				seen[name] = true
			}
		}
	}
	return p, nil
}

// VersionRequirement returns a diagnostic and whether the running build meets
// requires. Reads may display the diagnostic even when writes must refuse.
// Development builds deliberately satisfy the policy, with a warning.
func (p Policy) VersionRequirement(running string) (message string, satisfied bool) {
	if p.requires == "" {
		return "", true
	}
	if running == "(devel)" || running == "dev" || strings.HasPrefix(running, "dev ") {
		return fmt.Sprintf("whosaidso %s: requires %s; development build assumed to satisfy requires", running, p.requires), true
	}
	current, valid := parseVersion(running)
	required, _ := parseVersion(p.requires)
	if valid && compareVersions(current, required) >= 0 {
		return "", true
	}
	return fmt.Sprintf("whosaidso %s: requires %s; install: go install github.com/alex2481kobe/whosaidso/cmd/whosaidso@%s", running, p.requires, p.requires), false
}

// requireVersion is the write policy shared by intake and locked admission.
func requireVersion(project Project) error {
	message, satisfied := project.Policy.VersionRequirement(buildinfo.Version())
	if !satisfied {
		return storeFault("version-required", "requires", message)
	}
	return nil
}

type semanticVersion struct {
	core []string
	pre  []string
}

var versionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

func parseVersion(raw string) (semanticVersion, bool) {
	m := versionPattern.FindStringSubmatch(raw)
	if m == nil {
		return semanticVersion{}, false
	}
	v := semanticVersion{core: m[1:4]}
	if m[4] != "" {
		v.pre = strings.Split(m[4], ".")
		for _, id := range v.pre {
			if numericVersionID(id) && len(id) > 1 && id[0] == '0' {
				return semanticVersion{}, false
			}
		}
	}
	return v, true
}

func numericVersionID(id string) bool {
	return strings.Trim(id, "0123456789") == ""
}

// Compare digit strings by length before spelling, avoiding machine integer
// limits. Build metadata has no precedence and is omitted by parseVersion.
func compareVersionNumbers(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}

func compareVersions(a, b semanticVersion) int {
	for i := range a.core {
		if c := compareVersionNumbers(a.core[i], b.core[i]); c != 0 {
			return c
		}
	}
	if len(a.pre) == 0 && len(b.pre) != 0 {
		return 1
	}
	if len(b.pre) == 0 && len(a.pre) != 0 {
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, y := a.pre[i], b.pre[i]
		xnum, ynum := numericVersionID(x), numericVersionID(y)
		var c int
		switch {
		case xnum && ynum:
			c = compareVersionNumbers(x, y)
		case xnum:
			c = -1
		case ynum:
			c = 1
		default:
			c = strings.Compare(x, y)
		}
		if c != 0 {
			return c
		}
	}
	if len(a.pre) < len(b.pre) {
		return -1
	}
	if len(a.pre) > len(b.pre) {
		return 1
	}
	return 0
}
