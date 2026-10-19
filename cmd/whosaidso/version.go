package main

// This file holds the version verb: which build of whosaidso is running, so a
// report can name it. Nothing else about the build belongs here.

import (
	"flag"
	"fmt"

	"github.com/alex2481kobe/whosaidso/internal/buildinfo"
)

func versionVerb(fs *flag.FlagSet) func(*call) error {
	return func(c *call) error {
		if err := noPositionals(c, "version"); err != nil {
			return err
		}
		_, err := fmt.Fprintln(c.stdout, "whosaidso", buildinfo.Version())
		return err
	}
}
