package main

// This file holds the home verb: showing and changing where this machine keeps
// a project's live ledger. The registry itself, binding and its checks live in
// internal/store (home.go, home_bind.go).

import (
	"flag"
	"fmt"
	"path/filepath"

	"datum/internal/store"
)

// homeAnswer is `datum home` with no PATH: the binding, never a refusal.
type homeAnswer struct {
	ProjectID    string  `json:"project_id"`
	Bound        bool    `json:"bound"`
	HomeRoot     *string `json:"home_root"`
	Available    bool    `json:"available"`
	Unavailable  string  `json:"unavailable_reason,omitempty"`
	CheckoutRoot string  `json:"checkout_root"`
}

// bindAnswer is `datum home PATH`: what the binding did.
type bindAnswer struct {
	ProjectID  string `json:"project_id"`
	HomeRoot   string `json:"home_root"`
	Previous   string `json:"previous_root,omitempty"`
	Continuity string `json:"continuity"`
	Bundles    int    `json:"bundles"`
}

func homeVerb(fs *flag.FlagSet) func(*call) error {
	jsonOutput := jsonFlag(fs)
	return func(c *call) error {
		if len(c.args) > 1 || c.argv != nil {
			return usageError("datum home takes at most one PATH")
		}
		if len(c.args) == 1 {
			return bindHome(c, c.args[0], *jsonOutput)
		}
		invoked, err := c.checkout()
		if err != nil {
			return err
		}
		a := homeAnswer{ProjectID: string(invoked.ID), CheckoutRoot: invoked.Root}
		root, bound, err := store.Binding(invoked.ID)
		if err != nil {
			return err
		}
		if bound {
			a.Bound, a.HomeRoot = true, &root
			if _, err := store.Open(c.cwd); err != nil {
				a.Unavailable = err.Error()
			} else {
				a.Available = true
			}
		}
		text := fmt.Sprintf("project  %s\ncheckout %s\n", a.ProjectID, a.CheckoutRoot)
		switch {
		case !bound:
			text += "home     unbound: reads and admission refuse; run datum home PATH with the checkout that holds the live ledger\n"
		case !a.Available:
			text += fmt.Sprintf("home     %s UNAVAILABLE: %s\n", root, a.Unavailable)
		default:
			text += fmt.Sprintf("home     %s\nreads and admission use the home; run, capture and git observation use the checkout\n", root)
		}
		return printResult(c.stdout, *jsonOutput, a, text)
	}
}

func bindHome(c *call, path string, jsonOutput bool) error {
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.cwd, path)
	}
	r, err := store.Bind(c.ctx, c.cwd, path)
	if err != nil {
		return err
	}
	a := bindAnswer{ProjectID: string(r.Project.ID), HomeRoot: r.Project.Root, Previous: r.Previous, Continuity: r.Continuity, Bundles: r.Bundles}
	var text string
	switch r.Continuity {
	case store.ContinuitySame:
		text = fmt.Sprintf("%s is already bound to %s (%d bundles); nothing changed\n", a.ProjectID, a.HomeRoot, a.Bundles)
	case store.ContinuityNew:
		text = fmt.Sprintf("bound %s to %s (%d bundles)\n", a.ProjectID, a.HomeRoot, a.Bundles)
	case store.ContinuityContinued:
		text = fmt.Sprintf("moved %s from %s to %s; its %d bundles continue the old history\n", a.ProjectID, a.Previous, a.HomeRoot, a.Bundles)
	default:
		text = fmt.Sprintf("moved %s from %s to %s (%d bundles); continuity: not-compared, the old home is gone\n", a.ProjectID, a.Previous, a.HomeRoot, a.Bundles)
	}
	return printResult(c.stdout, jsonOutput, a, text)
}
