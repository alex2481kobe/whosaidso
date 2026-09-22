package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"datum/internal/model"
	"datum/internal/query"
	"datum/internal/store"
)

const readUsage = `datum show [--json] [RECORD_ID]
datum history [--json] [RECORD_ID]
datum task todo [--json]
datum intake pending [--json]

Show selects current admitted records. History selects admitted events in order.
TODO includes all tasks not CLOSED. Pending includes rejected and correction-requested packets.
Every answer carries its ledger watermark. Flags precede the optional record ID.
Output is generated on stdout; --json exports the same answer as text.
`

func isReadCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "show", "history", "task", "intake", "read":
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
	if command != "show" && command != "history" && command != "task todo" && command != "intake pending" {
		return fmt.Errorf("unknown read command %q; see datum read --help", command)
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(stderr, readUsage) }
	jsonOutput := flags.Bool("json", false, "export the answer as JSON")
	if err := flags.Parse(rest); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	request := query.Request{Command: command}
	if flags.NArg() > 1 || flags.NArg() > 0 && command != "show" && command != "history" {
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
