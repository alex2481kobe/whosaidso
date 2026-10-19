package main

// This file holds the home verb: showing and changing which checkout this
// machine's viewer (whosaidso ui) opens for a project when it runs outside
// any checkout, refusing a checkout no read could serve. Reads and writes
// never consult it. The registry itself lives in internal/store (home.go,
// home_bind.go).

import (
	"flag"
	"fmt"
	"path/filepath"

	"github.com/alex2481kobe/whosaidso/internal/query"
	"github.com/alex2481kobe/whosaidso/internal/store"
	"github.com/alex2481kobe/whosaidso/internal/write"
)

// homeAnswer is `whosaidso home` with no PATH: the registry entry, never a refusal.
type homeAnswer struct {
	ProjectID    string  `json:"project_id"`
	Registered   bool    `json:"registered"`
	ViewerRoot   *string `json:"viewer_root"`
	Available    bool    `json:"available"`
	Unavailable  string  `json:"unavailable_reason,omitempty"`
	CheckoutRoot string  `json:"checkout_root"`
	// IntakeDir is this machine's intake for the project: one directory per
	// packet, which is what moves to a reviewer on another machine.
	IntakeDir string `json:"intake_dir"`
}

// bindAnswer is `whosaidso home PATH`: what the registration did.
type bindAnswer struct {
	ProjectID  string `json:"project_id"`
	ViewerRoot string `json:"viewer_root"`
	Previous   string `json:"previous_root,omitempty"`
	Bundles    int    `json:"bundles"`
}

func homeVerb(fs *flag.FlagSet) func(*call) error {
	jsonOutput := jsonFlag(fs)
	return func(c *call) error {
		if len(c.args) > 1 || c.argv != nil {
			return usageError("whosaidso home takes at most one PATH")
		}
		if len(c.args) == 1 {
			return bindHome(c, c.args[0], *jsonOutput)
		}
		invoked, err := c.project()
		if err != nil {
			return err
		}
		intake, err := store.IntakeDir(invoked)
		if err != nil {
			return err
		}
		a := homeAnswer{ProjectID: string(invoked.ID), CheckoutRoot: invoked.Root, IntakeDir: intake}
		root, registered, err := store.Binding(invoked.ID)
		if err != nil {
			return err
		}
		if registered {
			a.Registered, a.ViewerRoot = true, &root
			if _, err := c.openProject(root); err != nil {
				a.Unavailable = err.Error()
			} else {
				a.Available = true
			}
		}
		text := fmt.Sprintf("project  %s\ncheckout %s (reads and writes here use this checkout's ledger)\nintake   %s (this machine's packets, one directory each)\n",
			a.ProjectID, a.CheckoutRoot, a.IntakeDir)
		switch {
		case !registered:
			text += "viewer   none registered: whosaidso ui opens a project only from inside its checkout; register one with whosaidso home PATH\n"
		case !a.Available:
			text += fmt.Sprintf("viewer   %s UNAVAILABLE: %s\n", root, a.Unavailable)
		default:
			text += fmt.Sprintf("viewer   %s (what whosaidso ui opens from outside a checkout)\n", root)
		}
		return printResult(c.stdout, *jsonOutput, a, text)
	}
}

func bindHome(c *call, path string, jsonOutput bool) error {
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.cwd, path)
	}
	if err := servable(c, path); err != nil {
		return err
	}
	r, err := store.Bind(c.ctx, c.cwd, path)
	if err != nil {
		return err
	}
	a := bindAnswer{ProjectID: string(r.Project.ID), ViewerRoot: r.Project.Root, Previous: r.Previous, Bundles: r.Bundles}
	text := fmt.Sprintf("registered %s at %s for the viewer (%d bundles)\n", a.ProjectID, a.ViewerRoot, a.Bundles)
	if r.Previous == r.Project.Root {
		text = fmt.Sprintf("%s is already registered at %s (%d bundles); nothing changed\n", a.ProjectID, a.ViewerRoot, a.Bundles)
	}
	return printResult(c.stdout, jsonOutput, a, text)
}

// servable refuses to register a checkout that every read would then refuse:
// it answers the read todo answers (the ledger decoded and folded by this
// binary, and the project's intake under the store's own owner-only
// permission rule) from PATH before the entry is written. It also refuses a
// checkout missing artifacts its admitted records cite. A PATH that is not
// this project's own root is left to store.Bind to refuse.
func servable(c *call, path string) error {
	invoked, err := c.project()
	if err != nil {
		return err
	}
	dest, err := c.openProject(path)
	if err != nil || dest.ID != invoked.ID {
		return nil
	}
	want, err1 := filepath.EvalSymlinks(path)
	got, err2 := filepath.EvalSymlinks(dest.Root)
	if err1 != nil || err2 != nil || want != got {
		return nil
	}
	// Replayed, not Load: the probe reads the whole ledger and writes no cache.
	state, err := store.Replayed(dest)
	if err == nil {
		_, err = query.ReadViewFrom(dest, query.ViewRequest{View: "todo"}, state)
	}
	if err != nil {
		return fmt.Errorf("whosaidso home: not registered: every read through %s would refuse: %w", path, err)
	}
	if err := write.HomeHoldsKeptArtifacts(c.ctx, dest, state); err != nil {
		return fmt.Errorf("whosaidso home: not registered: %w", err)
	}
	return nil
}
