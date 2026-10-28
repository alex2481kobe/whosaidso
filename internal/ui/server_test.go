package ui

// The viewer's guards: the URL's launch token is good for one exchange, for
// the session cookie the page and the API need; only this listener's Host
// and Origin are served; every method but GET is refused before anything is
// read, but for the gallery's two POSTs; the static files are served with
// their types; and a view is answered only for an available project.

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/query"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

type stubWorld struct {
	reads        atomic.Int32
	projects     []store.Registration
	captureActor model.Actor
	reveal       func(path string) error
}

func (w *stubWorld) config() Config {
	return Config{
		CaptureActor: w.captureActor, View: func(_ context.Context, p store.Project, r query.ViewRequest, _ bool) (query.ViewAnswer, error) {
			w.reads.Add(1)
			return &query.TodoAnswer{ViewHeader: query.ViewHeader{View: r.View, Project: p.ID, Result: "KNOWN"}}, nil
		},
		Projects: func() ([]store.Registration, error) { return w.projects, nil },
		Reveal:   w.reveal,
	}
}

func testUI(t *testing.T, w *stubWorld) (host, token string) {
	t.Helper()
	ln, server, address, err := Listen(w.config())
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(ln)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	u, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u.Host, "127.0.0.1:") || u.Path != "/" {
		t.Fatalf("the viewer must listen on loopback only, got %s", address)
	}
	return u.Host, u.Query().Get("token")
}

// sess is what a browser holds after the exchange: the session cookie (as a
// Cookie header value) and the page's key, sent as X-WhoSaidSo-Key.
type sess struct{ cookie, key string }

// probe is one request, carrying whatever of a session it is given.
type probe struct {
	method, path, host, origin string
	s                          sess
}

func do(t *testing.T, listener string, p probe) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest(p.method, "http://"+listener+p.path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = p.host
	if p.origin != "" {
		req.Header.Set("Origin", p.origin)
	}
	if p.s.cookie != "" {
		req.Header.Set("Cookie", p.s.cookie)
	}
	if p.s.key != "" {
		req.Header.Set("X-WhoSaidSo-Key", p.s.key)
	}
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(body)
}

// enter trades the launch token for the session cookie and the page's key,
// as the browser's first visit does.
func enter(t *testing.T, host, token string) sess {
	t.Helper()
	got, header, body := do(t, host, probe{"GET", "/?token=" + token, host, "", sess{}})
	cookies := (&http.Response{Header: header}).Cookies()
	if got != http.StatusSeeOther || !strings.HasPrefix(header.Get("Location"), "/#key=") || len(cookies) != 1 {
		t.Fatalf("the launch token was not exchanged: %d %v %s", got, header, body)
	}
	c := cookies[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Name != "whosaidso-"+host[strings.Index(host, ":")+1:] {
		t.Fatalf("the session cookie must be HttpOnly, SameSite=Strict, named for its port: %+v", c)
	}
	key := strings.TrimPrefix(header.Get("Location"), "/#key=")
	if len(key) != 64 || key == c.Value || key == token {
		t.Fatalf("the page's key must be its own 32 random bytes: %q", key)
	}
	return sess{c.Name + "=" + c.Value, key}
}

func available() []store.Registration {
	return []store.Registration{{ID: "team/ok", Home: "/ok", Project: store.Project{ID: "team/ok", Root: "/ok"}}}
}

func TestViewerRequiresTheSessionAndItsOwnHostAndOrigin(t *testing.T) {
	w := &stubWorld{projects: available()}
	host, token := testUI(t, w)
	wrong := strings.Repeat("0", len(token))
	view := "/api/view?project=team%2Fok&view=todo"
	type row struct {
		name string
		p    probe
		want int
	}
	check := func(rows []row) {
		for _, c := range rows {
			if got, _, body := do(t, host, c.p); got != c.want {
				t.Errorf("%s: status %d, want %d (%s)", c.name, got, c.want, body)
			}
		}
	}
	// Before the exchange: nothing opens, and no refused attempt spends the token.
	check([]row{
		{"page without a token", probe{"GET", "/", host, "", sess{}}, 403},
		{"page with a wrong token of the same length", probe{"GET", "/?token=" + wrong, host, "", sess{}}, 403},
		{"page with a token prefix", probe{"GET", "/?token=" + token[:len(token)-1], host, "", sess{}}, 403},
		{"page with a foreign Host", probe{"GET", "/?token=" + token, "localhost" + host[strings.Index(host, ":"):], "", sess{}}, 403},
		{"page with the token from a foreign Origin", probe{"GET", "/?token=" + token, host, "https://evil.example", sess{}}, 403},
		{"API with the token only in the URL", probe{"GET", view + "&token=" + token, host, "", sess{}}, 403},
		{"API without a session", probe{"GET", view, host, "", sess{}}, 403},
	})
	session := enter(t, host, token)
	name := session.cookie[:strings.Index(session.cookie, "=")]
	check([]row{
		{"page in session (control)", probe{"GET", "/", host, "", session}, 200},
		{"API in session (control)", probe{"GET", view, host, "", session}, 200},
		{"API from this page's origin (control)", probe{"GET", view, host, "http://" + host, session}, 200},
		{"the launch token replayed", probe{"GET", "/?token=" + token, host, "", sess{}}, 403},
		{"the launch token replayed in session", probe{"GET", "/?token=" + token, host, "", session}, 403},
		{"API with a forged session of the same length", probe{"GET", view, host, "", sess{name + "=" + strings.Repeat("0", len(session.cookie)-len(name)-1), session.key}}, 403},
		{"API with the launch token as the session", probe{"GET", view, host, "", sess{name + "=" + token, session.key}}, 403},
		{"API with the cookie but not the key", probe{"GET", view, host, "", sess{cookie: session.cookie}}, 403},
		{"API with the cookie and a wrong key", probe{"GET", view, host, "", sess{session.cookie, strings.Repeat("0", 64)}}, 403},
		{"API with the key but not the cookie", probe{"GET", view, host, "", sess{key: session.key}}, 403},
		{"projects without a session", probe{"GET", "/api/projects", host, "", sess{}}, 403},
		{"API with a foreign Host", probe{"GET", view, "evil.example:80", "", session}, 403},
		{"API with a foreign Origin", probe{"GET", view, host, "https://evil.example", session}, 403},
		{"API with a null Origin", probe{"GET", view, host, "null", session}, 403},
	})
	if got := w.reads.Load(); got != 2 {
		t.Fatalf("only the two admitted API requests may read a view, read %d", got)
	}
}

// A second exchange, from anyone, gets no cookie: the launch URL opens the
// viewer once, so a copy of it left in a log or history opens nothing.
func TestLaunchTokenIsSpentByItsFirstExchange(t *testing.T) {
	host, token := testUI(t, &stubWorld{projects: available()})
	enter(t, host, token)
	got, header, _ := do(t, host, probe{"GET", "/?token=" + token, host, "", sess{}})
	if got != http.StatusForbidden || header.Get("Set-Cookie") != "" {
		t.Fatalf("a replayed launch token: %d, Set-Cookie %q; want 403 and no cookie", got, header.Get("Set-Cookie"))
	}
}

func TestViewerRefusesEveryMethodButGetOrTheGalleryPosts(t *testing.T) {
	w := &stubWorld{projects: available()}
	host, token := testUI(t, w)
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
		for _, path := range []string{"/?token=" + token, "/api/view?project=team%2Fok&view=todo", "/api/projects", "/ui.js"} {
			if got, header, _ := do(t, host, probe{method, path, host, "", sess{cookie: token}}); got != http.StatusMethodNotAllowed || header.Get("Allow") != "GET" {
				t.Errorf("%s %s: status %d allow %q, want 405 GET", method, path, got, header.Get("Allow"))
			}
		}
	}
	for _, method := range []string{"GET", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
		for path := range posts {
			if got, header, _ := do(t, host, probe{method, path + "?project=team%2Fok&id=x", host, "", sess{cookie: token}}); got != http.StatusMethodNotAllowed || header.Get("Allow") != "POST" {
				t.Errorf("%s %s: status %d allow %q, want 405 POST", method, path, got, header.Get("Allow"))
			}
		}
	}
	if w.reads.Load() != 0 {
		t.Fatal("a refused method must read nothing")
	}
}

func TestStaticFilesAreServedWithTheirTypes(t *testing.T) {
	host, token := testUI(t, &stubWorld{})
	if code, _, _ := do(t, host, probe{"GET", "/owl.png", host, "", sess{}}); code != 404 {
		t.Fatalf("unused asset is still routed: %d", code)
	}
	// The types are stated here, not read from the table they check.
	types := map[string]string{".css": "text/css; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".png": "image/png", ".ico": "image/png"}
	for path, file := range staticFiles {
		want, err := files.ReadFile(file[0])
		if err != nil {
			t.Fatal(err)
		}
		kind := types[path[strings.LastIndex(path, "."):]]
		got, header, body := do(t, host, probe{"GET", path, host, "", sess{}})
		if got != 200 || kind == "" || header.Get("Content-Type") != kind || body != string(want) || header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: %d %q, want 200 %q with its embedded bytes", path, got, header.Get("Content-Type"), kind)
		}
	}
	embedded, err := files.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range embedded {
		if entry.Name() != "index.html" {
			if _, ok := staticFiles["/"+entry.Name()]; !ok {
				t.Fatalf("embedded asset has no served path: %s", entry.Name())
			}
		}
	}

	got, header, body := do(t, host, probe{"GET", "/", host, "", enter(t, host, token)})
	if got != 200 || header.Get("Content-Type") != "text/html; charset=utf-8" || !strings.Contains(body, `src="/ui.js"`) ||
		!strings.Contains(header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("the page: %d %q", got, header.Get("Content-Type"))
	}
	for _, path := range []string{"/index.html", "/server.go", "/api/", "/ui.css/", "/../ui.css"} {
		if got, _, _ := do(t, host, probe{"GET", path, host, "", sess{cookie: token}}); got != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, got)
		}
	}
}

func TestViewAnswersOnlyAnAvailableProjectAndAValidRequest(t *testing.T) {
	w := &stubWorld{projects: append(available(), store.Registration{ID: "team/gone", Home: "/gone", Reason: "home unavailable"})}
	host, token := testUI(t, w)
	session := enter(t, host, token)
	for query, want := range map[string]int{
		"project=team%2Fok&view=todo":           200,
		"project=team%2Fok&view=show&stale=1":   200,
		"project=team%2Fgone&view=todo":         404,
		"project=team%2Fnever&view=todo":        404,
		"project=team%2Fok&view=todo&stale=1":   400,
		"project=team%2Fok&view=write":          400,
		"project=team%2Fok&view=continue":       400,
		"project=team%2Fok&view=show&id=not-id": 400,
	} {
		if got, _, body := do(t, host, probe{"GET", "/api/view?" + query, host, "", session}); got != want {
			t.Errorf("%s: status %d, want %d (%s)", query, got, want, body)
		}
	}
}

func TestProjectsListsAnUnavailableHomeWithItsReason(t *testing.T) {
	w := &stubWorld{projects: append(available(), store.Registration{ID: "team/gone", Home: "/gone", Reason: "home /gone is unavailable: no such directory"})}
	w.projects[0].Project.ID = model.ProjectID("team/ok")
	host, token := testUI(t, w)
	got, _, body := do(t, host, probe{"GET", "/api/projects", host, "", enter(t, host, token)})
	if got != 200 || !strings.Contains(body, `{"id":"team/ok","home":"/ok","available":true,"watermark"`) ||
		!strings.Contains(body, `{"id":"team/gone","home":"/gone","available":false,"reason":"home /gone is unavailable: no such directory"}`) {
		t.Fatalf("projects: %d %s", got, body)
	}
	if w.reads.Load() != 1 {
		t.Fatalf("todo is read for the available project only, read %d", w.reads.Load())
	}
}
