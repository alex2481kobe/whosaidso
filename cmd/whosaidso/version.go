package main

// This file holds the version verb: which build of whosaidso is running, so a
// report can name it. Nothing else about the build belongs here.

import (
	"flag"
	"fmt"
	"runtime/debug"
	"strings"
)

// readBuildInfo lets tests supply the same metadata the version verb uses.
var readBuildInfo = debug.ReadBuildInfo

// checkVersion runs once before a project command can read input, capture or
// launch a process. In particular template --capture cannot bypass the gate
// through its authoring and pinning paths.
func (c *call) checkVersion(name string, fs *flag.FlagSet) error {
	writes := false
	switch name {
	case "capture", "admit", "handback", "run", "reconcile":
		writes = true
	case "template":
		writes = fs.Lookup("capture").Value.String() == "true"
		if !writes {
			return nil
		}
	case "todo", "continue", "show", "history":
	default:
		if !strings.HasPrefix(name, "check ") {
			return nil
		}
	}
	project, err := c.project()
	if err != nil {
		return err
	}
	message, satisfied := project.Policy.VersionRequirement(buildVersion())
	if writes && !satisfied {
		return usageError("%s", message)
	}
	if message != "" {
		_, err = fmt.Fprintln(c.stderr, "warning: "+message)
	}
	return err
}

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
	bi, ok := readBuildInfo()
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
