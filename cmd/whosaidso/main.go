// Command whosaidso is the whole command surface: the write side that captures
// and admits, and the read side that answers from what was admitted.
//
// The two are deliberately separate. Capture writes immutable intake and
// publishes nothing. Admission is the only command that writes a bundle.
// Reads never write at all, so deleting anything a read produced changes no
// canonical state.
package main

// This file holds the process entry, dispatch through the usage registry
// (registry.go), argument parsing and exit status. What each verb does lives
// in its own file; help text lives in help.go and guide.go.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := whosaidso(ctx, os.Args[1:], cwd, os.Stdin, os.Stdout, os.Stderr, os.Getenv)
	stop()
	os.Exit(code)
}

// exitError is a failure whose exit status is not 1: 2 for a usage error, 3
// for an UNKNOWN check, 4 for a partial success. An empty message prints
// nothing, for answers that were already printed in full.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

func usageError(format string, a ...any) error {
	return &exitError{code: 2, msg: fmt.Sprintf(format, a...)}
}

// exitCode is the process status for err: 0 for none, the exitError's own
// code, else 1 (refused or false).
func exitCode(err error) int {
	var e *exitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &e):
		return e.code
	}
	return 1
}

// call is one invocation: where it runs, its streams, and its arguments once
// the verb's flags are parsed out of them.
type call struct {
	ctx    context.Context
	cwd    string
	stdin  io.Reader
	stdout *countingWriter
	stderr io.Writer
	getenv func(string) string
	args   []string // positional arguments, wherever they stood among the flags
	argv   []string // everything after a literal --
	json   bool     // the verb's --json was given
}

// project is the project a command reads and admits through: its bound home
// (store.Open). It refuses an unbound project or a missing home, and when the
// home is not the invoking checkout it says on stderr which home was used.
func (c *call) project() (store.Project, error) {
	p, err := store.Open(c.cwd)
	if err == nil && p.Root != p.Checkout {
		fmt.Fprintf(c.stderr, "home %s (invoked in %s)\n", p.Root, p.Checkout)
	}
	return p, err
}

// checkout is the invoking checkout's project, for what needs only the
// declared project id and this checkout: capture routes intake by id and
// captures sources from here, so it needs no home.
func (c *call) checkout() (store.Project, error) { return store.Discover(c.cwd) }

type countingWriter struct {
	w io.Writer
	n int
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.n += n
	return n, err
}

// whosaidso runs one command and returns its exit status. Every message about a
// failure goes to stderr; stdout carries only answers. With --json, a failure
// that printed no answer still prints one JSON object naming the error.
func whosaidso(ctx context.Context, args []string, cwd string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	c := &call{ctx: ctx, cwd: cwd, stdin: stdin, stdout: &countingWriter{w: stdout}, stderr: stderr, getenv: getenv}
	err := dispatch(c, args)
	code := exitCode(err)
	if err != nil && err.Error() != "" {
		fmt.Fprintln(stderr, err)
	}
	if err != nil && c.json && c.stdout.n == 0 {
		name := map[int]string{1: "refused", 2: "usage", 3: "unknown", 4: "partial"}[code]
		encoded, _ := json.Marshal(map[string]any{"error": map[string]string{"code": name, "message": err.Error()}})
		fmt.Fprintf(stdout, "%s\n", encoded)
	}
	return code
}

// dispatch finds the verb in the registry, parses its own flags (before or
// after positional arguments), and runs it. `VERB --help` prints the verb's
// usage from the same registry entry, once, on stdout.
func dispatch(c *call, args []string) error {
	if len(args) == 0 {
		_, err := io.WriteString(c.stdout, bareSummary)
		return err
	}
	if args[0] == "--help" || args[0] == "-h" {
		args = append([]string{"help"}, args[1:]...)
	}
	if args[0] == "--version" {
		args = append([]string{"version"}, args[1:]...)
	}
	v, rest := lookup(args)
	if v == nil {
		if modes := groupModes(args[0]); len(modes) > 0 {
			if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
				_, err := io.WriteString(c.stdout, groupHelp(args[0]))
				return err
			}
			return usageError("whosaidso %s needs a mode: %s; run whosaidso help %s", args[0], strings.Join(modes, ", "), args[0])
		}
		return usageError("unknown command %q; run whosaidso help", args[0])
	}
	fs := flag.NewFlagSet("whosaidso "+v.name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	run := v.define(fs)
	if err := parseArgs(fs, rest, c); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err := io.WriteString(c.stdout, verbHelp(v))
			return err
		}
		return usageError("whosaidso %s: %v; run whosaidso help %s", v.name, err, v.name)
	}
	if f := fs.Lookup("json"); f != nil {
		c.json = f.Value.String() == "true"
	}
	return run(c)
}

// parseArgs accepts flags before or after positional arguments. Everything
// after a literal -- is kept whole in c.argv (run's command line).
func parseArgs(fs *flag.FlagSet, args []string, c *call) error {
	for i, a := range args {
		if a == "--" {
			c.argv, args = args[i+1:], args[:i]
			break
		}
	}
	for {
		if err := fs.Parse(args); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			return nil
		}
		c.args = append(c.args, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// jsonFlag is --json, the complete answer as one JSON object.
func jsonFlag(fs *flag.FlagSet) *bool {
	return fs.Bool("json", false, "print the complete answer as one JSON object")
}

// actorFlag is --actor. Not given, it falls back to WHOSAIDSO_ACTOR; given empty,
// or absent from both, the actor is recorded unknown, never guessed.
func actorFlag(fs *flag.FlagSet) func(*call) model.Actor {
	id := fs.String("actor", "", "the attributed actor `ID`; default $WHOSAIDSO_ACTOR")
	return func(c *call) model.Actor {
		value := *id
		if !isSet(fs, "actor") {
			value = c.getenv("WHOSAIDSO_ACTOR")
		}
		if model.Blank(value) {
			return model.Actor{UnknownReason: "no actor supplied by --actor or WHOSAIDSO_ACTOR"}
		}
		return model.Actor{ID: value}
	}
}

func isSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

// noPositionals refuses positional arguments for a verb that takes none.
func noPositionals(c *call, verb string) error {
	if len(c.args) > 0 || c.argv != nil {
		return usageError("whosaidso %s takes flags, not positional arguments", verb)
	}
	return nil
}
