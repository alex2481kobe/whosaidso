// Package ui serves `whosaidso ui`: a local, short-lived, read-only viewer.
//
// This file holds the server and its guards: a random loopback port, a random
// token, Host and Origin checks, GET only, and the embedded static files. The
// JSON it serves is api.go's; what the page does with it is ui.js's. Nothing
// here, or anywhere in the package, writes to a ledger or to intake.
package ui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"net"
	"net/http"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/query"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

//go:embed index.html ui.css ui.js ui-home.js ui-records.js ui-detail.js ui-kinds.js ui-history.js favicon-32.png favicon-180.png
var files embed.FS

// staticFiles maps each served path to its embedded file and media type.
// Only "/" serves the page, and only with the token; any path outside this
// table and the two API paths is not found.
var staticFiles = map[string][2]string{
	"/ui.css":          {"ui.css", "text/css; charset=utf-8"},
	"/ui.js":           {"ui.js", "text/javascript; charset=utf-8"},
	"/ui-home.js":      {"ui-home.js", "text/javascript; charset=utf-8"},
	"/ui-records.js":   {"ui-records.js", "text/javascript; charset=utf-8"},
	"/ui-detail.js":    {"ui-detail.js", "text/javascript; charset=utf-8"},
	"/ui-kinds.js":     {"ui-kinds.js", "text/javascript; charset=utf-8"},
	"/ui-history.js":   {"ui-history.js", "text/javascript; charset=utf-8"},
	"/favicon-32.png":  {"favicon-32.png", "image/png"},
	"/favicon-180.png": {"favicon-180.png", "image/png"},
	"/favicon.ico":     {"favicon-32.png", "image/png"},
}

// Viewer answers one view for one project exactly as the CLI's read verbs
// do: continue with its fresh workspace look, show --stale with its git.
type Viewer func(ctx context.Context, project store.Project, request query.ViewRequest, stale bool) (query.ViewAnswer, error)

// Config is what the viewer reads through. Invoked is the project the
// command was run inside, opened as the CLI opens it there; nil elsewhere.
// Projects lists this machine's registry (store.Registered).
type Config struct {
	Invoked  *store.Project
	View     Viewer
	Projects func() ([]store.Registration, error)
}

type handler struct {
	cfg    Config
	host   string
	origin string
	token  string
}

// Listen opens the viewer on a random 127.0.0.1 port. The server runs only
// while its caller serves the listener. The returned URL carries the token.
func Listen(cfg Config) (net.Listener, *http.Server, string, error) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, nil, "", err
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		ln.Close()
		return nil, nil, "", err
	}
	host := ln.Addr().String()
	h := &handler{cfg: cfg, host: host, origin: "http://" + host, token: hex.EncodeToString(secret[:])}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	return ln, srv, h.origin + "/?token=" + h.token, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	// A page on another origin, or a rebinding DNS name, must not read the
	// ledger: the Host must be this listener and any Origin this page.
	if r.Host != h.host || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != h.origin) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed: the viewer is read-only", http.StatusMethodNotAllowed)
		return
	}
	switch path := r.URL.Path; {
	case path == "/":
		if !h.allowed(r.URL.Query().Get("token")) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h.serveFile(w, "index.html", "text/html; charset=utf-8")
	case path == "/api/projects" || path == "/api/view":
		if !h.allowed(r.Header.Get("X-WhoSaidSo-Token")) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if path == "/api/projects" {
			h.projects(w, r)
		} else {
			h.view(w, r)
		}
	case staticFiles[path][0] != "":
		h.serveFile(w, staticFiles[path][0], staticFiles[path][1])
	default:
		http.NotFound(w, r)
	}
}

// allowed compares in constant time, so response timing does not reveal
// how much of a guessed token was right.
func (h *handler) allowed(got string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(h.token)) == 1
}

func (h *handler) serveFile(w http.ResponseWriter, name, kind string) {
	b, err := files.ReadFile(name)
	if err != nil {
		http.Error(w, "UI file unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", kind)
	_, _ = w.Write(b)
}
