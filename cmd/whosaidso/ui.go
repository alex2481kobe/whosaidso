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
	"sync"

	"github.com/alex2481kobe/whosaidso/internal/buildinfo"
	"github.com/alex2481kobe/whosaidso/internal/store"
	"github.com/alex2481kobe/whosaidso/internal/ui"
)

func uiVerb(fs *flag.FlagSet) func(*call) error {
	noOpen := fs.Bool("no-open", false, "print the URL without opening a browser")
	return func(c *call) error {
		if err := noPositionals(c, "ui"); err != nil {
			return err
		}
		cfg := uiConfig(c.cwd)
		// Registry reads open projects on each request. Keep warning state for
		// this viewer's lifetime and serialize diagnostics across requests.
		var warningMu sync.Mutex
		warned := make(map[string]bool)
		warn := func(project store.Project) error {
			warningMu.Lock()
			defer warningMu.Unlock()
			id := string(project.ID)
			if warned[id] {
				return nil
			}
			message, _ := project.Policy.VersionRequirement(buildinfo.Version())
			if message == "" {
				return nil
			}
			_, err := fmt.Fprintf(c.stderr, "warning: project %s: %s\n", id, message)
			if err == nil {
				warned[id] = true
			}
			return err
		}
		if cfg.Invoked != nil {
			if err := warn(*cfg.Invoked); err != nil {
				return err
			}
		}
		projects := cfg.Projects
		cfg.Projects = func() ([]store.Registration, error) {
			list, err := projects()
			if err != nil {
				return nil, err
			}
			for _, entry := range list {
				if entry.Reason == "" {
					if err := warn(entry.Project); err != nil {
						return nil, err
					}
				}
			}
			return list, nil
		}
		ln, server, url, err := ui.Listen(cfg)
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
// this machine's registry and, inside a checkout, that checkout, exactly as
// every read verb run in cwd opens it.
func uiConfig(cwd string) ui.Config {
	cfg := ui.Config{View: answerView, Projects: store.Registered, Reveal: ui.RevealInFinder}
	if project, err := store.Discover(cwd); err == nil {
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
