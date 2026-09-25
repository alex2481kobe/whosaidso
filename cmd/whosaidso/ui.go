package main

// This file holds the ui verb: it starts internal/ui's read-only viewer on a
// loopback port for as long as the command runs, prints its URL and opens
// the browser. The server, its guards and the page live in internal/ui; the
// views it serves are the read verbs' own (answerView in read.go).

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"

	"github.com/alex2481kobe/whosaidso/internal/store"
	"github.com/alex2481kobe/whosaidso/internal/ui"
)

func uiVerb(fs *flag.FlagSet) func(*call) error {
	noOpen := fs.Bool("no-open", false, "print the URL without opening a browser")
	return func(c *call) error {
		if err := noPositionals(c, "ui"); err != nil {
			return err
		}
		ln, server, url, err := ui.Listen(uiConfig(c.cwd))
		if err != nil {
			return err
		}
		defer ln.Close()
		fmt.Fprintln(c.stdout, url)
		if !*noOpen {
			openBrowser(url, c)
		}
		go func() {
			<-c.ctx.Done()
			_ = server.Shutdown(context.Background())
		}()
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// uiConfig is what the viewer reads through: the read verbs' own answers,
// this machine's registry and, inside a bound project, that project opened
// through the same home and checkout as every read verb run in cwd.
func uiConfig(cwd string) ui.Config {
	cfg := ui.Config{View: answerView, Projects: store.Registered}
	if project, err := store.Open(cwd); err == nil {
		cfg.Invoked = &project
	}
	return cfg
}

// openBrowser asks the desktop to open url; when it cannot, the printed URL
// is the way in.
func openBrowser(url string, c *call) {
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	cmd := exec.Command(command, url)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(c.stderr, "whosaidso ui: open the URL above in a browser (%v)\n", err)
		return
	}
	go func() { _ = cmd.Wait() }()
}
