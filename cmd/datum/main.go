// Command datum is the whole command surface: the write side that captures
// and admits, and the read side that answers from what was admitted.
//
// The two are deliberately separate. Capture writes immutable intake and
// publishes nothing. Admission is the only command that writes a bundle.
// Reads never write at all, so deleting anything a read produced changes no
// canonical state.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := datum(ctx, os.Args[1:], cwd, os.Stdin, os.Stdout, os.Stderr, os.Getenv)
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

// datum runs one command and returns its exit status. Every message about a
// failure goes to stderr; stdout carries only answers.
func datum(ctx context.Context, args []string, cwd string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) > 0 && args[0] == "id" {
		return idCLI(args[1:], stdout, stderr)
	}
	var err error
	if isReadCommand(args) {
		err = readCLI(ctx, args, cwd, stdout, stderr)
	} else {
		err = writeCLI(ctx, args, cwd, stdin, stdout, stderr, getenv)
	}
	if err != nil && err.Error() != "" {
		fmt.Fprintln(stderr, err)
	}
	return exitCode(err)
}
