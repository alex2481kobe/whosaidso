package main

// This file holds the home verb: showing and changing where this machine keeps
// a project's live ledger, refusing to bind a home no read could serve. The
// registry itself, binding and its checks live in internal/store (home.go,
// home_bind.go).

import (
	"flag"
	"fmt"
	"path/filepath"

	"github.com/alex2481kobe/whosaidso/internal/query"
	"github.com/alex2481kobe/whosaidso/internal/store"
	"github.com/alex2481kobe/whosaidso/internal/write"
)

// homeAnswer is `whosaidso home` with no PATH: the binding, never a refusal.
type homeAnswer struct {
	ProjectID    string  `json:"project_id"`
	Bound        bool    `json:"bound"`
	HomeRoot     *string `json:"home_root"`
	Available    bool    `json:"available"`
	Unavailable  string  `json:"unavailable_reason,omitempty"`
	CheckoutRoot string  `json:"checkout_root"`
}

// bindAnswer is `whosaidso home PATH`: what the binding did.
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
			return usageError("whosaidso home takes at most one PATH")
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
			text += "home     unbound: reads and admission refuse; run whosaidso home PATH with the checkout that holds the live ledger\n"
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
	if err := servable(c, path); err != nil {
		return err
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
	case store.ContinuityUnreadable:
		text = fmt.Sprintf("bound %s to %s (%d bundles), replacing a registry entry that could not be read; no previous home was known, so continuity was not compared\n", a.ProjectID, a.HomeRoot, a.Bundles)
	case store.ContinuityContinued:
		text = fmt.Sprintf("moved %s from %s to %s; its %d bundles continue the old history\n", a.ProjectID, a.Previous, a.HomeRoot, a.Bundles)
	default:
		text = fmt.Sprintf("moved %s from %s to %s (%d bundles); continuity: not-compared, the old home is gone\n", a.ProjectID, a.Previous, a.HomeRoot, a.Bundles)
	}
	return printResult(c.stdout, jsonOutput, a, text)
}

// servable refuses to bind a home that every read would then refuse: it
// answers the read todo answers (the ledger decoded and folded by this
// binary, and the project's intake under the store's own owner-only
// permission rule) from PATH before the binding is written. It also refuses a
// home missing artifacts its admitted records cite, on a first binding and a
// move alike: a home that cannot resolve its own admitted evidence is not one.
// A PATH that is not this project's own root is left to store.Bind to refuse.
func servable(c *call, path string) error {
	invoked, err := c.checkout()
	if err != nil {
		return err
	}
	dest, err := store.Discover(path)
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
		return fmt.Errorf("whosaidso home: not bound: every read through %s would refuse: %w", path, err)
	}
	if err := write.HomeHoldsKeptArtifacts(c.ctx, dest, state); err != nil {
		return fmt.Errorf("whosaidso home: not bound: %w", err)
	}
	return nil
}
