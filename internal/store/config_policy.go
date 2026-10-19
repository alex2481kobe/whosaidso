package store

// A project's optional settings in whosaidso.toml, beside the two required
// keys (config.go): each has a default, so a config without them reads as
// before. They travel with the repository, so every checkout and machine
// applies the same ones.

import (
	"fmt"
	"time"
)

// policyKeys are the optional keys a config may set.
var policyKeys = map[string]bool{"stale_after": true, "self_admission": true}

// DefaultStaleAfter is how long a claim may go without admitted activity
// before reads flag it a takeover candidate, when the config sets none.
const DefaultStaleAfter = 24 * time.Hour

// Policy is a project's optional settings.
type Policy struct {
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
	return p, nil
}
