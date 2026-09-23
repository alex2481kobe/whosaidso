package main

// This file holds the four read verbs (todo, continue, show, history) and
// continue's fresh look at the workspace. What each view selects lives in
// internal/query; the verbs' help lives in the registry.

import (
	"context"
	"flag"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"datum/internal/model"
	"datum/internal/query"
	"datum/internal/reduce"
	"datum/internal/write"
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
				return usageError("datum %s: unexpected positional arguments", view)
			case len(c.args) == 0 && view == "continue":
				return usageError("datum continue needs a RECORD_ID")
			case len(c.args) == 1:
				request.ID = model.ID(c.args[0])
			}
			if err := query.CheckView(request); err != nil {
				return usageError("datum %s: %v", view, err)
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
		observed := observe(c.ctx, project.Root)
		request.Observed = &observed
	}
	if stale {
		request.Stale = func(s reduce.Snapshot) []query.StaleClaim {
			out := []query.StaleClaim{}
			for _, claim := range write.StaleClaims(c.ctx, project, s) {
				out = append(out, query.StaleClaim(claim))
			}
			return out
		}
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

// observe is continue's fresh look at the workspace. Anything git cannot
// report stays UNKNOWN with git's reason. --no-optional-locks keeps status
// from refreshing the index, so continue writes nothing, not even there.
func observe(ctx context.Context, root string) query.Observation {
	git := func(args ...string) (string, error) {
		out, err := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "-C", root}, args...)...).Output()
		return strings.TrimSpace(string(out)), err
	}
	at := time.Now().UTC()
	o := query.Observation{ObservedAt: model.Availability[time.Time]{State: model.Known, Value: &at}}
	head, err := git("rev-parse", "--verify", "HEAD")
	format, formatErr := git("rev-parse", "--show-object-format")
	if err == nil && formatErr == nil && head != "" && format != "" {
		o.Head = model.Availability[model.GitHead]{State: model.Known, Value: &model.GitHead{ObjectFormat: format, Commit: head}}
	} else {
		o.Head = model.Availability[model.GitHead]{State: model.Unknown, Reason: fmt.Sprintf("git could not report HEAD here: %v %v", err, formatErr)}
	}
	if status, err := git("status", "--porcelain"); err == nil {
		dirty := status != ""
		o.Dirty = model.Availability[bool]{State: model.Known, Value: &dirty}
	} else {
		o.Dirty = model.Availability[bool]{State: model.Unknown, Reason: fmt.Sprintf("git could not report working-tree status here: %v", err)}
	}
	return o
}
