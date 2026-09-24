package model

// The code-change fact a proof records beside a set-aside run lives
// here: the run's recorded commit, the commit the proof's support ran at, and
// the paths under the claim's scope that differ between them. Its shape is
// checked here; that it matches the run, the scope and the supporting runs is
// the reducer's (reduce/code_change.go), and that git agrees is the gate's.

import (
	"fmt"
	"path"
)

// CodeChange says code under the claim's scope changed between the commit a
// run executed (From) and the commit the proof's support ran at (To), so the
// run measured different code. It is a recorded fact, never a request: the
// admission gate verifies it with git and replay checks it against the ledger.
type CodeChange struct {
	From         GitHead  `json:"from"`
	To           GitHead  `json:"to"`
	ChangedPaths []string `json:"changed_paths"`
}

func (c CodeChange) validate(p string) error {
	if c.From.ObjectFormat != c.To.ObjectFormat {
		return invalid(p+".to.object_format", "both commits must use one object format")
	}
	if c.From.Commit == c.To.Commit {
		return invalid(p+".to.commit", "a code change needs two different commits")
	}
	if len(c.ChangedPaths) == 0 {
		return invalid(p+".changed_paths", "a code change names at least one changed path")
	}
	seen := map[string]bool{}
	for i, changed := range c.ChangedPaths {
		at := fmt.Sprintf("%s.changed_paths[%d]", p, i)
		if err := relativePath(changed, at); err != nil {
			return err
		}
		// One spelling per path, so the recorded set compares with git's exactly.
		if path.Clean(changed) != changed || changed == "." || seen[changed] {
			return invalid(at, "expected a clean, distinct file path")
		}
		seen[changed] = true
	}
	return nil
}
