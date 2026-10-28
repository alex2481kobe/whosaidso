package main

// This file holds the ui verb: it starts internal/ui's viewer on a
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
	actor := actorFlag(fs)
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
		cfg.CaptureActor = actor(c)
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
	cfg := ui.Config{View: answerView, Projects: store.Registered, Reveal: ui.RevealFile}
	if project, err := store.Discover(cwd); err == nil {
		cfg.Invoked = &project
	}
	return cfg
}

// openBrowser asks the desktop to open url; when it cannot, the printed URL
// is the way in.
func openBrowser(url string, c *call) {
	cmd := browserCommand(runtime.GOOS, url)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(c.stderr, "whosaidso ui: open the URL above in a browser (%v)\n", err)
		return
	}
	go func() { _ = cmd.Wait() }()
}

// browserCommand keeps a URL in one argument, including on Windows.
func browserCommand(platform, url string) *exec.Cmd {
	switch platform {
	case "windows":
		return exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url)
	case "darwin":
		return exec.Command("open", url)
	default:
		return exec.Command("xdg-open", url)
	}
}

const uiHelp = `Serves Gallery, Overview, Tasks, Records and Activity on a random
127.0.0.1 port while the command runs (Ctrl-C stops it). The printed URL
carries a one-time token exchanged for a session cookie on its first visit.
Run ui again for a fresh URL. --no-open keeps browser launch headless.
Screens return bounded lists; Gallery loads more files as you scroll.
Delete captures and admits a gallery retraction as --actor or WHOSAIDSO_ACTOR,
without deleting the media file. The project's self-admission policy still
applies: a refused admission leaves the packet pending and the entry visible.
Reveal opens Finder on macOS, Explorer on Windows, or the containing folder
in the Linux file manager. Copy path and Download work on every platform.
Inside a checkout, the viewer opens that checkout; elsewhere it lists the
projects registered with whosaidso home PATH, including unavailable homes.
`
