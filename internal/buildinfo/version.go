// Package buildinfo owns the running binary identity, not project policy.
package buildinfo

import "runtime/debug"

// Version supplies the same process identity to diagnostics and write policy.
var Version = version

// version is the module version Go recorded for this binary: the tag of
// a `go install ...@vX` build, or the version Go derives from a checkout. When
// Go recorded none, it is "dev" plus the commit the build came from, marked
// modified when the checkout had uncommitted changes, so a report still names
// the code it ran.
func version() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	version, modified := "dev", false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) >= 12 {
				version += " " + s.Value[:12]
			}
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if modified {
		version += " (modified)"
	}
	return version
}
