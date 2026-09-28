package main

// This file holds the version verb: which build of whosaidso is running, so a
// report can name it. Nothing else about the build belongs here.

import (
	"flag"
	"fmt"
	"runtime/debug"
)

func versionVerb(fs *flag.FlagSet) func(*call) error {
	return func(c *call) error {
		if err := noPositionals(c, "version"); err != nil {
			return err
		}
		_, err := fmt.Fprintln(c.stdout, "whosaidso", buildVersion())
		return err
	}
}

// buildVersion is the module version Go recorded for this binary: the tag of
// a `go install ...@vX` build, or the version Go derives from a checkout. When
// Go recorded none, it is "dev" plus the commit the build came from, marked
// modified when the checkout had uncommitted changes, so a report still names
// the code it ran.
func buildVersion() string {
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
