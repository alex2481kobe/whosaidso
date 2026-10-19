package main

// This file holds the version verb: which build of whosaidso is running, so a
// report can name it. Nothing else about the build belongs here.

import (
	"flag"
	"fmt"
	"strings"

	"github.com/alex2481kobe/whosaidso/internal/buildinfo"
)

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
	message, satisfied := project.Policy.VersionRequirement(buildinfo.Version())
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
		_, err := fmt.Fprintln(c.stdout, "whosaidso", buildinfo.Version())
		return err
	}
}
