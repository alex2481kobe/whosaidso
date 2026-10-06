package ui

// The Gallery's endpoints: each previewable extension is served with its own
// type and nosniff, and nothing else is served; a file gone from disk is
// missing when asked; a file is reached by entry id and never by a path in
// the request; video answers byte ranges; text is bounded and never markup;
// and the two POSTs need this page's Origin and the session.

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/gallery"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

// galleryUI starts a viewer over a temp home holding team/ok's gallery and
// returns the listener, the session cookie and that gallery's file.
func galleryUI(t *testing.T, w *stubWorld) (host, session, file string) {
	t.Helper()
	t.Setenv(store.HomeEnv, t.TempDir())
	w.projects = available()
	host, token := testUI(t, w)
	file, err := store.GalleryFile("team/ok")
	if err != nil {
		t.Fatal(err)
	}
	return host, enter(t, host, token), file
}

// shown writes a file and adds it to the gallery, returning its entry id.
func shown(t *testing.T, file, name string, content []byte) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	e, err := gallery.Add(context.Background(), file, "/", path, "the "+name, "", "agent", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return e.ID, path
}

// ask sends one request with any extra headers and returns status, headers
// and body.
func ask(t *testing.T, host, method, path, origin, cookie string, extra ...string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+host+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	for i := 0; i+1 < len(extra); i += 2 {
		req.Header.Set(extra[i], extra[i+1])
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, body
}

func fileURL(id string) string { return "/api/gallery/file?project=team%2Fok&id=" + id }

func TestGalleryServesEachPreviewKindWithItsTypeAndNothingElse(t *testing.T) {
	host, session, file := galleryUI(t, &stubWorld{})
	// The types are stated here, not read from the table they check.
	for name, want := range map[string]string{
		"a.png": "image/png", "a.jpg": "image/jpeg", "a.JPEG": "image/jpeg", "a.gif": "image/gif", "a.webp": "image/webp",
		"a.mp4": "video/mp4", "a.mov": "video/quicktime",
		"a.txt": "text/plain; charset=utf-8", "a.log": "text/plain; charset=utf-8", "a.md": "text/markdown; charset=utf-8",
		"a.json": "application/json; charset=utf-8", "a.csv": "text/csv; charset=utf-8",
		"a.html": "", "a.svg": "", "a.js": "", "noextension": "",
	} {
		content := []byte("bytes of " + name)
		id, _ := shown(t, file, name, content)
		got, header, body := ask(t, host, "GET", fileURL(id), "", session)
		if want == "" {
			if got != http.StatusUnsupportedMediaType || bytes.Contains(body, content) {
				t.Errorf("%s: status %d; a file with no preview must not be served", name, got)
			}
			continue
		}
		if got != 200 || header.Get("Content-Type") != want || header.Get("X-Content-Type-Options") != "nosniff" || !bytes.Equal(body, content) {
			t.Errorf("%s: %d %q nosniff=%q, want 200 %q with its bytes", name, got, header.Get("Content-Type"), header.Get("X-Content-Type-Options"), want)
		}
	}
}

func TestGalleryShowsAGoneFileAsMissing(t *testing.T) {
	revealed := 0
	host, session, file := galleryUI(t, &stubWorld{reveal: func(string) error { revealed++; return nil }})
	id, path := shown(t, file, "shot.png", []byte("png"))
	keep, _ := shown(t, file, "kept.png", []byte("png"))
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	got, _, body := ask(t, host, "GET", "/api/gallery?project=team%2Fok", "", session)
	if got != 200 || !strings.Contains(string(body), `"id":"`+id+`"`) {
		t.Fatalf("list: %d %s", got, body)
	}
	for _, entry := range []struct{ id, status string }{{id, "missing"}, {keep, "present"}} {
		at := strings.Index(string(body), `"id":"`+entry.id+`"`)
		rest := string(body)[at:]
		if !strings.Contains(rest[:strings.Index(rest, "}")], `"status":"`+entry.status+`"`) {
			t.Errorf("entry %s: want status %s in %s", entry.id, entry.status, body)
		}
	}
	if got, _, _ := ask(t, host, "GET", fileURL(id), "", session); got != http.StatusNotFound {
		t.Errorf("a missing file is served: %d", got)
	}
	if got, _, _ := ask(t, host, "POST", "/api/gallery/reveal?project=team%2Fok&id="+id, "http://"+host, session); got != http.StatusNotFound || revealed != 0 {
		t.Errorf("revealing a missing file: %d, revealed %d", got, revealed)
	}
}

func TestGalleryReachesAFileByEntryIDOnly(t *testing.T) {
	host, session, file := galleryUI(t, &stubWorld{})
	id, _ := shown(t, file, "note.txt", []byte("shown"))
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("never served"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _, body := ask(t, host, "GET", fileURL(id), "", session); got != 200 || string(body) != "shown" {
		t.Fatalf("control: %d %s", got, body)
	}
	for _, path := range []string{
		fileURL(id) + "&path=" + secret,
		fileURL(id) + "&file=" + secret,
		fileURL(secret),
		fileURL("..%2F" + id),
		fileURL(id) + "&id=" + id,
		"/api/gallery/file?project=team%2Fok&path=" + secret,
		"/api/gallery/file" + secret,
		"/api/gallery/file?project=team%2Fnever&id=" + id,
	} {
		got, _, body := ask(t, host, "GET", path, "", session)
		if got == 200 || bytes.Contains(body, []byte("never served")) || bytes.Contains(body, []byte("shown")) {
			t.Errorf("%s: status %d serves a file not named by one entry id", path, got)
		}
	}
	if got, _, _ := ask(t, host, "GET", fileURL(id), "", ""); got != http.StatusForbidden {
		t.Errorf("a file without the session: %d", got)
	}
}

func TestGalleryVideoAnswersByteRanges(t *testing.T) {
	host, session, file := galleryUI(t, &stubWorld{})
	content := bytes.Repeat([]byte("0123456789"), 100)
	id, _ := shown(t, file, "clip.mp4", content)
	got, header, body := ask(t, host, "GET", fileURL(id), "", session, "Range", "bytes=10-19")
	if got != http.StatusPartialContent || header.Get("Content-Range") != "bytes 10-19/1000" || !bytes.Equal(body, content[10:20]) ||
		header.Get("Content-Type") != "video/mp4" {
		t.Fatalf("range: %d %q %q %q", got, header.Get("Content-Range"), header.Get("Content-Type"), body)
	}
	if got, header, _ := ask(t, host, "GET", fileURL(id), "", session); got != 200 || header.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("a whole video says it takes ranges: %d %q", got, header.Get("Accept-Ranges"))
	}
}

func TestGalleryTextIsBoundedAndNeverMarkup(t *testing.T) {
	host, session, file := galleryUI(t, &stubWorld{})
	hostile := "<script>alert(1)</script><img src=x onerror=alert(2)>"
	id, _ := shown(t, file, "hostile.md", []byte(hostile))
	got, header, body := ask(t, host, "GET", fileURL(id), "", session)
	if got != 200 || header.Get("Content-Type") != "text/markdown; charset=utf-8" || header.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(header.Get("Content-Security-Policy"), "default-src 'none'") || string(body) != hostile {
		t.Fatalf("hostile text: %d %q %q", got, header.Get("Content-Type"), header.Get("X-Content-Type-Options"))
	}
	big := bytes.Repeat([]byte("x"), maxText+5000)
	id, _ = shown(t, file, "big.log", big)
	got, header, body = ask(t, host, "GET", fileURL(id), "", session)
	if got != 200 || len(body) != maxText || header.Get("X-WhoSaidSo-Truncated") == "" {
		t.Fatalf("a long text: %d, %d bytes, truncated %q; want %d bytes and the mark", got, len(body), header.Get("X-WhoSaidSo-Truncated"), maxText)
	}
	// A hostile title reaches the page as JSON text, never as markup.
	path := filepath.Join(t.TempDir(), "x.txt")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := gallery.Add(context.Background(), file, "/", path, hostile, hostile, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	got, header, body = ask(t, host, "GET", "/api/gallery?project=team%2Fok", "", session)
	if got != 200 || header.Get("Content-Type") != "application/json; charset=utf-8" || bytes.Contains(body, []byte("<script>")) {
		t.Fatalf("the list carries markup: %d %q %s", got, header.Get("Content-Type"), body)
	}
}

func TestGalleryPostsNeedThisOriginAndTheSession(t *testing.T) {
	var revealed []string
	host, session, file := galleryUI(t, &stubWorld{reveal: func(p string) error { revealed = append(revealed, p); return nil }})
	id, path := shown(t, file, "shot.png", []byte("png"))
	own := "http://" + host
	for _, action := range []string{"reveal", "remove"} {
		url := "/api/gallery/" + action + "?project=team%2Fok&id=" + id
		for _, c := range []struct{ name, origin, cookie string }{
			{"no Origin", "", session}, {"a foreign Origin", "https://evil.example", session}, {"a null Origin", "null", session},
			{"a sibling port", "http://127.0.0.1:1", session}, {"no session", own, ""},
		} {
			if got, _, _ := ask(t, host, "POST", url, c.origin, c.cookie); got != http.StatusForbidden {
				t.Errorf("%s with %s: status %d, want 403", action, c.name, got)
			}
		}
	}
	if len(revealed) != 0 {
		t.Fatalf("a refused POST revealed %v", revealed)
	}
	if entries, _ := gallery.Load(file); len(entries) != 1 {
		t.Fatalf("a refused POST removed an entry: %v", entries)
	}
	if got, _, _ := ask(t, host, "POST", "/api/gallery/reveal?project=team%2Fok&id="+id, own, session); got != 200 || len(revealed) != 1 || revealed[0] != path {
		t.Fatalf("reveal control: %d %v", got, revealed)
	}
	if got, _, _ := ask(t, host, "POST", "/api/gallery/remove?project=team%2Fok&id="+id, own, session); got != 200 {
		t.Fatalf("remove control: %d", got)
	}
	if entries, _ := gallery.Load(file); len(entries) != 0 {
		t.Fatalf("remove left the entry: %v", entries)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "png" {
		t.Fatalf("remove touched the file: %q %v", b, err)
	}
}

func TestRevealSaysWhenItCannot(t *testing.T) {
	host, session, file := galleryUI(t, &stubWorld{})
	id, _ := shown(t, file, "shot.png", []byte("png"))
	got, _, body := ask(t, host, "POST", "/api/gallery/reveal?project=team%2Fok&id="+id, "http://"+host, session)
	if got != http.StatusNotImplemented || !strings.Contains(string(body), "cannot reveal") {
		t.Fatalf("no reveal: %d %s", got, body)
	}
}
