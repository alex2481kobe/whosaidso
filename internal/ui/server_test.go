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
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/query"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

type stubWorld struct {
	reads    atomic.Int32
	projects []store.Registration
	reveal   func(path string) error
}

func (w *stubWorld) config() Config {
	return Config{
		View: func(_ context.Context, p store.Project, r query.ViewRequest, _ bool) (query.ViewAnswer, error) {
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

// probe is one request; cookie is sent as the Cookie header.
type probe struct{ method, path, host, origin, cookie string }

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
	if p.cookie != "" {
		req.Header.Set("Cookie", p.cookie)
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

// enter trades the launch token for the session cookie, as the browser's
// first visit does, and returns it as a Cookie header value.
func enter(t *testing.T, host, token string) string {
	t.Helper()
	got, header, body := do(t, host, probe{"GET", "/?token=" + token, host, "", ""})
	cookies := (&http.Response{Header: header}).Cookies()
	if got != http.StatusSeeOther || header.Get("Location") != "/" || len(cookies) != 1 {
		t.Fatalf("the launch token was not exchanged: %d %v %s", got, header, body)
	}
	c := cookies[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Name != "whosaidso-"+host[strings.Index(host, ":")+1:] {
		t.Fatalf("the session cookie must be HttpOnly, SameSite=Strict, named for its port: %+v", c)
	}
	return c.Name + "=" + c.Value
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
		{"page without a token", probe{"GET", "/", host, "", ""}, 403},
		{"page with a wrong token of the same length", probe{"GET", "/?token=" + wrong, host, "", ""}, 403},
		{"page with a token prefix", probe{"GET", "/?token=" + token[:len(token)-1], host, "", ""}, 403},
		{"page with a foreign Host", probe{"GET", "/?token=" + token, "localhost" + host[strings.Index(host, ":"):], "", ""}, 403},
		{"page with the token from a foreign Origin", probe{"GET", "/?token=" + token, host, "https://evil.example", ""}, 403},
		{"API with the token only in the URL", probe{"GET", view + "&token=" + token, host, "", ""}, 403},
		{"API without a session", probe{"GET", view, host, "", ""}, 403},
	})
	session := enter(t, host, token)
	name := session[:strings.Index(session, "=")]
	check([]row{
		{"page in session (control)", probe{"GET", "/", host, "", session}, 200},
		{"API in session (control)", probe{"GET", view, host, "", session}, 200},
		{"API from this page's origin (control)", probe{"GET", view, host, "http://" + host, session}, 200},
		{"the launch token replayed", probe{"GET", "/?token=" + token, host, "", ""}, 403},
		{"the launch token replayed in session", probe{"GET", "/?token=" + token, host, "", session}, 403},
		{"API with a forged session of the same length", probe{"GET", view, host, "", name + "=" + strings.Repeat("0", len(session)-len(name)-1)}, 403},
		{"API with the launch token as the session", probe{"GET", view, host, "", name + "=" + token}, 403},
		{"projects without a session", probe{"GET", "/api/projects", host, "", ""}, 403},
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
	got, header, _ := do(t, host, probe{"GET", "/?token=" + token, host, "", ""})
	if got != http.StatusForbidden || header.Get("Set-Cookie") != "" {
		t.Fatalf("a replayed launch token: %d, Set-Cookie %q; want 403 and no cookie", got, header.Get("Set-Cookie"))
	}
}

func TestViewerRefusesEveryMethodButGetOrTheGalleryPosts(t *testing.T) {
	w := &stubWorld{projects: available()}
	host, token := testUI(t, w)
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
		for _, path := range []string{"/?token=" + token, "/api/view?project=team%2Fok&view=todo", "/api/projects", "/ui.js"} {
			if got, header, _ := do(t, host, probe{method, path, host, "", token}); got != http.StatusMethodNotAllowed || header.Get("Allow") != "GET" {
				t.Errorf("%s %s: status %d allow %q, want 405 GET", method, path, got, header.Get("Allow"))
			}
		}
	}
	for _, method := range []string{"GET", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
		for path := range posts {
			if got, header, _ := do(t, host, probe{method, path + "?project=team%2Fok&id=x", host, "", token}); got != http.StatusMethodNotAllowed || header.Get("Allow") != "POST" {
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
	// The types are stated here, not read from the table they check.
	types := map[string]string{".css": "text/css; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".png": "image/png", ".ico": "image/png"}
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
	if len(staticFiles) != 17 {
		t.Fatalf("every embedded file but the page is served: %d paths", len(staticFiles))
	}
	got, header, body := do(t, host, probe{"GET", "/", host, "", enter(t, host, token)})
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

// The owner's design rules, checked in the files that draw the page: no
// dot separators or bullet glyphs in its text; no pill-shaped tags (the one
// capsule is the theme switch, and no other corner is rounder than 16px);
// no checkboxes on rows, no avatars or profile pictures, no saved views; and
// no picture but the owl and the favicons, all served from this package.
// A file that brings any of them back fails here.
func TestPageKeepsTheOwnersDesignRules(t *testing.T) {
	page := []string{"index.html", "ui.css", "ui-screens.css", "ui.js", "ui-icons.js", "ui-parts.js", "ui-derive.js",
		"ui-home.js", "ui-records.js", "ui-detail.js", "ui-kinds.js", "ui-history.js", "ui-gallery.js", "ui-gallery.css"}
	images := regexp.MustCompile(`/[A-Za-z0-9_-]+\.(png|jpe?g|gif|webp|svg)\b`)
	radius := regexp.MustCompile(`border-radius:\s*(\d+)px`)
	capsules := 0
	for _, file := range page {
		b, err := files.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		lower := strings.ToLower(text)
		for _, banned := range []string{"·", "•", "<template", "type=\"checkbox\"", "type = \"checkbox\"", "type: \"checkbox\"", "avatar", "profile", "saved view"} {
			if strings.Contains(lower, strings.ToLower(banned)) {
				t.Errorf("%s carries %q", file, banned)
			}
		}
		for _, image := range images.FindAllString(text, -1) {
			if image != "/owl.png" && image != "/favicon-32.png" && image != "/favicon-180.png" {
				t.Errorf("%s draws %s; the owl and the favicons are the only pictures", file, image)
			}
		}
		for _, m := range radius.FindAllStringSubmatch(text, -1) {
			if px, _ := strconv.Atoi(m[1]); px >= 99 {
				capsules++
			} else if px > 16 {
				t.Errorf("%s rounds a corner %dpx, rounder than a rounded rectangle", file, px)
			}
		}
	}
	if capsules != 1 {
		t.Errorf("%d capsule shapes; only the theme switch is one", capsules)
	}
	if page, _ := files.ReadFile("index.html"); !strings.Contains(string(page), `<img src="/owl.png"`) {
		t.Error("the header's logo is not the owl")
	}
}
