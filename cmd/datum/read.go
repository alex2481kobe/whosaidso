package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"datum/internal/model"
	"datum/internal/query"
	"datum/internal/reduce"
	"datum/internal/store"
	"datum/internal/write"
)

const readUsage = `datum todo [--limit N] [--json]
datum continue RECORD_ID [--limit N] [--json]
datum show [RECORD_ID] [--kind task|claim|decision|instrument] [--stale] [--json]
datum history [RECORD_ID] [--self-admitted[=true|false|unknown]] [--json]

todo is everything owed, in flight first: in-flight tasks and their runs,
tasks awaiting acceptance, blocked and ready tasks, open decisions, pending
intake and attention. continue resumes any record: its detail, closure,
context and, for a task, progress, attempts, runs and what is owed, plus a
fresh look at git HEAD, dirty state and the time. show ID is one record;
bare show is a summary, every current record by kind and every run; --kind
keeps one kind; --stale adds, per observed claim, whether code under its
scope changed since its last run (it runs git and cannot see uncommitted
changes). history is admitted events in order, and without an ID every
per-packet review; --self-admitted selects reviews by self-admission.
Every answer carries its ledger watermark. The default text is the brief;
--json is the complete answer. --limit cuts only optional results.
`

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

func isReadCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "todo", "continue", "show", "history":
		return true
	}
	return false
}

// readCLI answers one of the four views. Discovery, flag parsing and output
// selection are its entire role. It never writes files.
func readCLI(ctx context.Context, args []string, cwd string, stdout, stderr io.Writer) error {
	view, rest := args[0], args[1:]
	flags := flag.NewFlagSet(view, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(stderr, readUsage) }
	jsonOutput := flags.Bool("json", false, "print the complete answer as JSON")
	request := query.ViewRequest{View: view}
	var selfAdmitted selfAdmissionFlag
	stale := false
	switch view {
	case "todo", "continue":
		flags.IntVar(&request.Limit, "limit", 0, "cap optional results; mandatory facts are never cut")
	case "show":
		flags.StringVar(&request.Kind, "kind", "", "bare show restricted to one kind: task, claim, decision or instrument")
		flags.BoolVar(&stale, "stale", false, "add whether each observed claim's scoped code changed since its last run (runs git)")
	case "history":
		flags.Var(&selfAdmitted, "self-admitted", "select per-packet reviews by true, false, or unknown (bare flag: true)")
	}
	if err := flags.Parse(rest); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return usageError("%v", err)
	}
	request.SelfAdmitted = model.SelfAdmissionState(selfAdmitted)
	switch {
	case flags.NArg() > 1 || flags.NArg() > 0 && view == "todo":
		return usageError("%s: unexpected positional arguments", view)
	case flags.NArg() == 0 && view == "continue":
		return usageError("continue needs a RECORD_ID")
	case flags.NArg() == 1:
		request.ID = model.ID(flags.Arg(0))
	}
	if err := query.CheckView(request); err != nil {
		return usageError("%s: %v", view, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	project, err := store.Discover(cwd)
	if err != nil {
		return err
	}
	if view == "continue" {
		observed := observe(ctx, project.Root)
		request.Observed = &observed
	}
	if stale {
		request.Stale = func(s reduce.Snapshot) []query.StaleClaim {
			out := []query.StaleClaim{}
			for _, c := range write.StaleClaims(ctx, project, s) {
				out = append(out, query.StaleClaim(c))
			}
			return out
		}
	}
	answer, err := query.ReadView(project, request)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if *jsonOutput {
		return query.RenderViewJSON(stdout, answer)
	}
	return query.RenderViewBrief(stdout, answer)
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
