package main

// This file holds the four read verbs (todo, continue, show, history) and
// continue's fresh look at the workspace. What each view selects lives in
// internal/query; the verbs' help lives in the registry.

import (
	"context"
	"flag"
	"fmt"
	"time"

	"whosaidso/internal/evidence"
	"whosaidso/internal/model"
	"whosaidso/internal/query"
	"whosaidso/internal/store"
)

// A bare audit flag means true, while explicit values retain all three states.
type selfAdmissionFlag string

func (f *selfAdmissionFlag) String() string { return string(*f) }
func (*selfAdmissionFlag) IsBoolFlag() bool { return true }
func (f *selfAdmissionFlag) Set(value string) error {
	switch model.SelfAdmissionState(value) {
	case model.SelfAdmissionTrue, model.SelfAdmissionFalse, model.SelfAdmissionUnknown:
		*f = selfAdmissionFlag(value)
		return nil
	default:
		return fmt.Errorf("self-admitted must be true, false, or unknown")
	}
}

// viewVerb is one of the four views: todo, continue, show or history. It
// declares only that view's flags. Discovery, the request and the rendering
// are its whole role; it never writes files.
func viewVerb(view string) func(*flag.FlagSet) func(*call) error {
	return func(fs *flag.FlagSet) func(*call) error {
		jsonOutput := jsonFlag(fs)
		var limit int
		var kind string
		var selfAdmitted selfAdmissionFlag
		stale := false
		switch view {
		case "todo", "continue":
			fs.IntVar(&limit, "limit", 0, "cap optional results at `N`; blockers, closure and attention are never cut")
		case "show":
			fs.StringVar(&kind, "kind", "", "bare show kept to one `KIND`: task, claim, decision or instrument")
			fs.BoolVar(&stale, "stale", false, "add, per observed claim, whether code under its scope changed since its last run (runs git)")
		case "history":
			fs.Var(&selfAdmitted, "self-admitted", "only the per-packet reviews whose self-admission is `true|false|unknown` (bare: true)")
		}
		return func(c *call) error {
			request := query.ViewRequest{View: view, Limit: limit, Kind: kind, SelfAdmitted: model.SelfAdmissionState(selfAdmitted)}
			switch {
			case c.argv != nil || len(c.args) > 1 || len(c.args) > 0 && view == "todo":
				return usageError("whosaidso %s: unexpected positional arguments", view)
			case len(c.args) == 0 && view == "continue":
				return usageError("whosaidso continue needs a RECORD_ID")
			case len(c.args) == 1:
				request.ID = model.ID(c.args[0])
			}
			if err := query.CheckView(request); err != nil {
				return usageError("whosaidso %s: %v", view, err)
			}
			return readView(c, request, stale, *jsonOutput)
		}
	}
}

func readView(c *call, request query.ViewRequest, stale, jsonOutput bool) error {
	if err := c.ctx.Err(); err != nil {
		return err
	}
	project, err := c.project()
	if err != nil {
		return err
	}
	if request.View == "continue" {
		observed := observe(c.ctx, project)
		request.Observed = &observed
	}
	if stale {
		request.Stale = staleGit(c.ctx, project)
	}
	answer, err := query.ReadView(project, request)
	if err != nil {
		return err
	}
	if err := c.ctx.Err(); err != nil {
		return err
	}
	if jsonOutput {
		return query.RenderViewJSON(c.stdout, answer)
	}
	return query.RenderViewBrief(c.stdout, answer)
}

// observe is continue's fresh look at the invoking checkout: its HEAD and
// whether its working tree differs from it, as observed, and the time.
// Anything git cannot report stays UNKNOWN with the reason. Git runs without
// optional locks, so continue writes nothing, not even the index.
func observe(ctx context.Context, project store.Project) query.Observation {
	root, at := project.ExecRoot(), time.Now().UTC()
	return query.Observation{Head: evidence.CheckoutHead(ctx, evidence.ExecGit, root), Dirty: evidence.CheckoutDirty(ctx, evidence.ExecGit, root),
		ObservedAt: model.Availability[time.Time]{State: model.Known, Value: &at}}
}

// staleGit is show --stale's git, in the invoking checkout: its HEAD, and the
// scoped diff between a run's commit and that HEAD.
func staleGit(ctx context.Context, project store.Project) *query.StaleGit {
	root := project.ExecRoot()
	return &query.StaleGit{Head: evidence.CheckoutHead(ctx, evidence.ExecGit, root),
		Changes: func(from, to model.GitHead, scope []string) ([]string, error) {
			return evidence.ScopeChanges(ctx, evidence.ExecGit, root, from, to, scope)
		}}
}
