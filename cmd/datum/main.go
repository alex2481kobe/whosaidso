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
	"fmt"
	"os"
	"os/signal"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if len(os.Args) > 1 && os.Args[1] == "id" {
		stop()
		os.Exit(idCLI(os.Args[2:], os.Stdout, os.Stderr))
	}
	cwd, err := os.Getwd()
	if err == nil {
		if isReadCommand(os.Args[1:]) {
			err = readCLI(ctx, os.Args[1:], cwd, os.Stdout, os.Stderr)
		} else {
			err = writeCLI(ctx, os.Args[1:], cwd, os.Stdin, os.Stdout, os.Stderr, os.Getenv)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
