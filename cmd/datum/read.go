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
	"datum/internal/store"
)

const readUsage = `datum show [--json] [RECORD_ID]
datum history [--json] [RECORD_ID]
datum history [--json] --self-admitted[=true|false|unknown]
datum task todo [--json]
datum intake pending [--json]
datum instruments|state|now [--json]
datum todo [--json] [--limit N]
datum context [--json] [--limit N] [RECORD_ID]
datum continue [--json] [--limit N] TASK_ID
datum disposal-loss [--json] --digest SHA256 [--git FORMAT:COMMIT:PATH]

Show selects current admitted records. History selects admitted events in order.
TODO includes all tasks not CLOSED. Pending includes rejected and correction-requested packets.
History without an ID also lists per-packet reviews. --self-admitted selects only
matching reviews, including rejected packets, without needing local intake bytes.
The bare flag selects true; false excludes unknown. Legacy facts remain UNKNOWN.
This audit filter cannot be combined with a record ID.
Every answer carries its ledger watermark. Flags precede the optional record ID.
Output is generated on stdout; --json exports the same answer as text.
INSTRUMENTS shows validation first; UNKNOWN validation is listed under attention.
--limit cuts only optional results (READY tasks, context refs), never blockers,
mandatory constraints, prerequisites, corrections or supersessions.
continue observes git HEAD, dirty state and the time now, and writes nothing.
disposal-loss prints the support_loss targets an artifact.dispose of exactly that
identity must record at this watermark (give --git when the disposal names a git
pin), and the admitted events citing it. Reasons are yours to write; admission
recomputes the list and stays the authority.
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
	case "show", "history", "task", "intake", "read", "instruments", "state", "now", "todo", "context", "continue", "disposal-loss":
		return true
	}
	return false
}

// This adapter is intentionally shorter than the usual file range: discovery,
// flag parsing and output selection are its entire role. It never writes files.
func readCLI(ctx context.Context, args []string, cwd string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "read" && (len(args) == 1 || len(args) == 2 && (args[1] == "--help" || args[1] == "-h")) {
		_, err := io.WriteString(stdout, readUsage)
		return err
	}
	command, rest := args[0], args[1:]
	if command == "task" || command == "intake" {
		want := "todo"
		if command == "intake" {
			want = "pending"
		}
		if len(rest) == 0 || rest[0] != want {
			return fmt.Errorf("expected task todo or intake pending; see datum read --help")
		}
		command, rest = command+" "+rest[0], rest[1:]
	}
	presets := map[string]bool{"instruments": true, "state": true, "now": true, "todo": true, "context": true, "continue": true, "disposal-loss": true}
	if command != "show" && command != "history" && command != "task todo" && command != "intake pending" && !presets[command] {
		return fmt.Errorf("unknown read command %q; see datum read --help", command)
	}
	withID := command == "show" || command == "history" || command == "context" || command == "continue"
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(stderr, readUsage) }
	jsonOutput := flags.Bool("json", false, "export the answer as JSON")
	var selfAdmitted selfAdmissionFlag
	limit := 0
	if command == "todo" || command == "context" || command == "continue" {
		flags.IntVar(&limit, "limit", 0, "cap optional results; mandatory facts are never cut")
	}
	if command == "history" {
		flags.Var(&selfAdmitted, "self-admitted", "select per-packet reviews by true, false, or unknown (bare flag: true)")
	}
	var digest, git string
	if command == "disposal-loss" {
		flags.StringVar(&digest, "digest", "", "sha-256 of the artifact a disposal would name (required)")
		flags.StringVar(&git, "git", "", "the disposal's git pin as FORMAT:COMMIT:PATH, when it names one")
	}
	if err := flags.Parse(rest); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	request := query.Request{Command: command, SelfAdmitted: model.SelfAdmissionState(selfAdmitted), Limit: limit, Context: ctx}
	if command == "disposal-loss" {
		request.Disposal = &query.DisposalTarget{Digest: model.Digest(digest)}
		if git != "" {
			parts := strings.SplitN(git, ":", 3)
			if len(parts) != 3 {
				return fmt.Errorf("disposal-loss: --git must be FORMAT:COMMIT:PATH")
			}
			request.Disposal.Git = &model.GitPin{ObjectFormat: parts[0], Commit: parts[1], Path: parts[2]}
		}
	}
	if flags.NArg() > 1 || flags.NArg() > 0 && !withID {
		return fmt.Errorf("%s: unexpected positional arguments", command)
	}
	if flags.NArg() == 1 {
		request.ID = model.ID(flags.Arg(0))
		if !model.ValidID(request.ID) {
			return fmt.Errorf("%s: RECORD_ID must be a ULID", command)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	project, err := store.Discover(cwd)
	if err != nil {
		return err
	}
	if command == "continue" {
		observed := observe(ctx, project.Root)
		request.Observed = &observed
	}
	answer, err := query.Read(project, request)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if *jsonOutput {
		return query.RenderJSON(stdout, answer)
	}
	return query.RenderText(stdout, answer)
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
