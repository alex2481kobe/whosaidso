// Package ui serves `whosaidso ui`: a local, short-lived, read-only viewer.
//
// This file holds the server and its guards: a random loopback port, a
// single-use launch token exchanged for a session cookie, Host and Origin
// checks, GET only but for the gallery's two POSTs, and the embedded static
// files. The JSON it serves is api.go's and gallery.go's; what the page does
// with it is ui.js's. Nothing in the package writes to a
// ledger. Remove captures a gallery retraction in intake for later admission.
package ui

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/query"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

//go:embed index.html ui.css ui-screens.css ui-gallery.css ui.js ui-icons.js ui-parts.js ui-derive.js ui-home.js ui-records.js ui-detail.js ui-kinds.js ui-history.js ui-gallery.js favicon-32.png favicon-180.png owl.png
var files embed.FS

// staticFiles maps each served path to its embedded file and media type.
// Only "/" serves the page, and only to the session; any path outside this
// table and the API paths is not found.
var staticFiles = map[string][2]string{
	"/ui.css":          {"ui.css", "text/css; charset=utf-8"},
	"/ui-screens.css":  {"ui-screens.css", "text/css; charset=utf-8"},
	"/ui-gallery.css":  {"ui-gallery.css", "text/css; charset=utf-8"},
	"/ui.js":           {"ui.js", "text/javascript; charset=utf-8"},
	"/ui-home.js":      {"ui-home.js", "text/javascript; charset=utf-8"},
	"/ui-records.js":   {"ui-records.js", "text/javascript; charset=utf-8"},
	"/ui-detail.js":    {"ui-detail.js", "text/javascript; charset=utf-8"},
	"/ui-kinds.js":     {"ui-kinds.js", "text/javascript; charset=utf-8"},
	"/ui-history.js":   {"ui-history.js", "text/javascript; charset=utf-8"},
	"/ui-icons.js":     {"ui-icons.js", "text/javascript; charset=utf-8"},
	"/ui-parts.js":     {"ui-parts.js", "text/javascript; charset=utf-8"},
	"/ui-derive.js":    {"ui-derive.js", "text/javascript; charset=utf-8"},
	"/ui-gallery.js":   {"ui-gallery.js", "text/javascript; charset=utf-8"},
	"/owl.png":         {"owl.png", "image/png"},
	"/favicon-32.png":  {"favicon-32.png", "image/png"},
	"/favicon-180.png": {"favicon-180.png", "image/png"},
	"/favicon.ico":     {"favicon-32.png", "image/png"},
}

// Viewer answers one view for one project exactly as the CLI's read verbs
// do: continue with its fresh workspace look, show --stale with its git.
type Viewer func(ctx context.Context, project store.Project, request query.ViewRequest, stale bool) (query.ViewAnswer, error)

// Config is what the viewer reads through. Invoked is the project the
// command was run inside, opened as the CLI opens it there; nil elsewhere.
// Projects lists this machine's registry (store.Registered). Reveal shows
// a gallery file in the desktop's file manager (RevealInFinder); nil, the
// viewer says it cannot. CaptureActor is the attributed author of a retraction
// packet; a missing actor remains explicitly unknown.
type Config struct {
	CaptureActor model.Actor
	Invoked      *store.Project
	View         Viewer
	Projects     func() ([]store.Registration, error)
	Reveal       func(path string) error
}

type handler struct {
	cfg     Config
	host    string
	origin  string
	token   string // the launch token, good for one exchange
	session string // the session cookie's value, issued by that exchange
	key     string // the page's own secret, issued by that exchange, never in a cookie
	cookie  string // the cookie's name, one per port: cookies ignore ports
	mu      sync.Mutex
	spent   bool
}

// Listen opens the viewer on a random 127.0.0.1 port. The server runs only
// while its caller serves the listener. The returned URL carries the launch
// token.
func Listen(cfg Config) (net.Listener, *http.Server, string, error) {
	if cfg.CaptureActor.ID == "" && cfg.CaptureActor.UnknownReason == "" {
		cfg.CaptureActor = model.Actor{UnknownReason: "viewer did not identify an actor"}
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, nil, "", err
	}
	var secret [96]byte
	if _, err := rand.Read(secret[:]); err != nil {
		ln.Close()
		return nil, nil, "", err
	}
	host := ln.Addr().String()
	_, port, _ := net.SplitHostPort(host)
	h := &handler{cfg: cfg, host: host, origin: "http://" + host, token: hex.EncodeToString(secret[:32]),
		session: hex.EncodeToString(secret[32:64]), key: hex.EncodeToString(secret[64:]), cookie: "whosaidso-" + port}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	return ln, srv, h.origin + "/?token=" + h.token, nil
}

// posts are the only paths that take POST, and they take nothing else.
var posts = map[string]bool{"/api/gallery/reveal": true, "/api/gallery/remove": true}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; media-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	// A page on another origin, or a rebinding DNS name, must not read the
	// ledger: the Host must be this listener and any Origin this page.
	if r.Host != h.host || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != h.origin) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if want := map[bool]string{true: http.MethodPost, false: http.MethodGet}[posts[r.URL.Path]]; r.Method != want {
		w.Header().Set("Allow", want)
		http.Error(w, "method not allowed: the viewer is read-only but for the gallery's Reveal and Remove", http.StatusMethodNotAllowed)
		return
	}
	// A POST must name this page as its Origin, never leave it out.
	if r.Method == http.MethodPost && r.Header.Get("Origin") != h.origin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	switch path := r.URL.Path; {
	case path == "/":
		if r.URL.Query().Has("token") {
			h.exchange(w, r)
			return
		}
		if !h.inSession(r) {
			http.Error(w, "forbidden: open the URL whosaidso ui printed; it works once", http.StatusForbidden)
			return
		}
		h.serveFile(w, "index.html", "text/html; charset=utf-8")
	case path == "/api/projects" || path == "/api/view" || path == "/api/gallery" || path == "/api/gallery/file" || posts[path]:
		if !h.inSession(r) || !h.keyed(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h.api(w, r)
	case staticFiles[path][0] != "":
		h.serveFile(w, staticFiles[path][0], staticFiles[path][1])
	default:
		http.NotFound(w, r)
	}
}

// exchange spends the launch token once: the first request carrying it gets
// the session cookie and is sent on to the page with the page's key in the
// URL fragment, which no request carries; every later one, the same token
// included, is refused. The cookie is HttpOnly, so no script reads it, and
// SameSite=Strict, so no other site's request carries it. A cookie is not
// scoped to a port, though: any server on another 127.0.0.1 port can be
// sent it and replay it. So every API call also needs the key, which only
// this origin's page holds (keyed).
func (h *handler) exchange(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	ok := !h.spent && same(r.URL.Query().Get("token"), h.token)
	h.spent = h.spent || ok
	h.mu.Unlock()
	if !ok {
		http.Error(w, "forbidden: this viewer URL was already opened; run whosaidso ui again", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: h.cookie, Value: h.session, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/#key="+h.key, http.StatusSeeOther)
}

// keyed reports whether r proves it comes from this origin's page: the key
// in its X-WhoSaidSo-Key header, or, for a gallery file, which media tags
// fetch and cannot add a header to, that entry's capability in the query.
func (h *handler) keyed(r *http.Request) bool {
	if r.URL.Path == "/api/gallery/file" {
		q := r.URL.Query()
		return same(q.Get("cap"), h.capability(q.Get("project"), q.Get("id")))
	}
	return same(r.Header.Get("X-WhoSaidSo-Key"), h.key)
}

// capability is the key's HMAC of one project's entry id: a file URL that
// opens that one entry and nothing else, and only with the session cookie.
func (h *handler) capability(project, id string) string {
	mac := hmac.New(sha256.New, []byte(h.key))
	mac.Write([]byte(project + "\x00" + id))
	return hex.EncodeToString(mac.Sum(nil))
}

// inSession reports whether r carries the session the exchange issued.
func (h *handler) inSession(r *http.Request) bool {
	h.mu.Lock()
	spent := h.spent
	h.mu.Unlock()
	c, err := r.Cookie(h.cookie)
	return spent && err == nil && same(c.Value, h.session)
}

// same compares in constant time, so response timing does not reveal how
// much of a guessed secret was right.
func same(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
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
