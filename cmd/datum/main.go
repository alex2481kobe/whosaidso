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
