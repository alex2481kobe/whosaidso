package ui

// The viewer's guards: the page needs the URL's token and the API the token
// header; only this listener's Host and Origin are served; every method but
// GET is refused before anything is read; the static files are served with
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

	"whosaidso/internal/model"
	"whosaidso/internal/query"
	"whosaidso/internal/store"
)

type stubWorld struct {
	reads    atomic.Int32
	projects []store.Registration
}

func (w *stubWorld) config() Config {
	return Config{
		View: func(_ context.Context, p store.Project, r query.ViewRequest, _ bool) (query.ViewAnswer, error) {
			w.reads.Add(1)
			return &query.TodoAnswer{ViewHeader: query.ViewHeader{View: r.View, Project: p.ID, Result: "KNOWN"}}, nil
		},
		Projects: func() ([]store.Registration, error) { return w.projects, nil },
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

type probe struct{ method, path, host, origin, header string }

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
	if p.header != "" {
		req.Header.Set("X-WhoSaidSo-Token", p.header)
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(body)
}

func available() []store.Registration {
	return []store.Registration{{ID: "team/ok", Home: "/ok", Project: store.Project{ID: "team/ok", Root: "/ok"}}}
}

func TestViewerRequiresTheTokenAndItsOwnHostAndOrigin(t *testing.T) {
	w := &stubWorld{projects: available()}
	host, token := testUI(t, w)
	wrong := strings.Repeat("0", len(token))
	view := "/api/view?project=team%2Fok&view=todo"
	for _, c := range []struct {
		name string
		p    probe
		want int
	}{
		{"page with the token (control)", probe{"GET", "/?token=" + token, host, "", ""}, 200},
		{"API with the token header (control)", probe{"GET", view, host, "", token}, 200},
		{"API from this page's origin (control)", probe{"GET", view, host, "http://" + host, token}, 200},
		{"page without a token", probe{"GET", "/", host, "", ""}, 403},
		{"page with a wrong token of the same length", probe{"GET", "/?token=" + wrong, host, "", ""}, 403},
		{"page with a token prefix", probe{"GET", "/?token=" + token[:len(token)-1], host, "", ""}, 403},
		{"API without a token", probe{"GET", view, host, "", ""}, 403},
		{"API with the token only in the URL", probe{"GET", view + "&token=" + token, host, "", ""}, 403},
		{"projects without a token", probe{"GET", "/api/projects", host, "", ""}, 403},
		{"API with a foreign Host", probe{"GET", view, "evil.example:80", "", token}, 403},
		{"page with a foreign Host", probe{"GET", "/?token=" + token, "localhost" + host[strings.Index(host, ":"):], "", ""}, 403},
		{"API with a foreign Origin", probe{"GET", view, host, "https://evil.example", token}, 403},
		{"API with a null Origin", probe{"GET", view, host, "null", token}, 403},
	} {
		if got, _, body := do(t, host, c.p); got != c.want {
			t.Errorf("%s: status %d, want %d (%s)", c.name, got, c.want, body)
		}
	}
	if got := w.reads.Load(); got != 2 {
		t.Fatalf("only the two admitted API requests may read a view, read %d", got)
	}
}

func TestViewerRefusesEveryMethodButGet(t *testing.T) {
	w := &stubWorld{projects: available()}
	host, token := testUI(t, w)
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
		for _, path := range []string{"/?token=" + token, "/api/view?project=team%2Fok&view=todo", "/api/projects", "/ui.js"} {
			if got, header, _ := do(t, host, probe{method, path, host, "", token}); got != http.StatusMethodNotAllowed || header.Get("Allow") != "GET" {
				t.Errorf("%s %s: status %d allow %q, want 405 GET", method, path, got, header.Get("Allow"))
			}
		}
	}
	if w.reads.Load() != 0 {
		t.Fatal("a refused method must read nothing")
	}
}

func TestStaticFilesAreServedWithTheirTypes(t *testing.T) {
	host, token := testUI(t, &stubWorld{})
	// The types are stated here, not read from the table they check.
	types := map[string]string{".css": "text/css; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".svg": "image/svg+xml", ".ico": "image/svg+xml"}
	for path, file := range staticFiles {
		want, err := files.ReadFile(file[0])
		if err != nil {
			t.Fatal(err)
		}
		kind := types[path[strings.LastIndex(path, "."):]]
		got, header, body := do(t, host, probe{"GET", path, host, "", ""})
		if got != 200 || kind == "" || header.Get("Content-Type") != kind || body != string(want) || header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: %d %q, want 200 %q with its embedded bytes", path, got, header.Get("Content-Type"), kind)
		}
	}
	if len(staticFiles) != 10 {
		t.Fatalf("every embedded file but the page is served: %d paths", len(staticFiles))
	}
	got, header, body := do(t, host, probe{"GET", "/?token=" + token, host, "", ""})
	if got != 200 || header.Get("Content-Type") != "text/html; charset=utf-8" || !strings.Contains(body, `src="/ui.js"`) ||
		!strings.Contains(header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("the page: %d %q", got, header.Get("Content-Type"))
	}
	for _, path := range []string{"/index.html", "/server.go", "/api/", "/ui.css/", "/../ui.css"} {
		if got, _, _ := do(t, host, probe{"GET", path, host, "", token}); got != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, got)
		}
	}
}

func TestViewAnswersOnlyAnAvailableProjectAndAValidRequest(t *testing.T) {
	w := &stubWorld{projects: append(available(), store.Registration{ID: "team/gone", Home: "/gone", Reason: "home unavailable"})}
	host, token := testUI(t, w)
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
		if got, _, body := do(t, host, probe{"GET", "/api/view?" + query, host, "", token}); got != want {
			t.Errorf("%s: status %d, want %d (%s)", query, got, want, body)
		}
	}
}

func TestProjectsListsAnUnavailableHomeWithItsReason(t *testing.T) {
	w := &stubWorld{projects: append(available(), store.Registration{ID: "team/gone", Home: "/gone", Reason: "home /gone is unavailable: no such directory"})}
	w.projects[0].Project.ID = model.ProjectID("team/ok")
	host, token := testUI(t, w)
	got, _, body := do(t, host, probe{"GET", "/api/projects", host, "", token})
	if got != 200 || !strings.Contains(body, `{"id":"team/ok","home":"/ok","available":true,"watermark"`) ||
		!strings.Contains(body, `{"id":"team/gone","home":"/gone","available":false,"reason":"home /gone is unavailable: no such directory"}`) {
		t.Fatalf("projects: %d %s", got, body)
	}
	if w.reads.Load() != 1 {
		t.Fatalf("todo is read for the available project only, read %d", w.reads.Load())
	}
}
