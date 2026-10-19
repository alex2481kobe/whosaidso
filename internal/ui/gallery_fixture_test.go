// Synthetic ledger, session and request fixtures support Gallery transport tests.
// Behavioral assertions belong in gallery_test.go and gallery_fifo_test.go.
package ui

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/gallery"
	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
	"github.com/alex2481kobe/whosaidso/internal/testaccess"
	"github.com/alex2481kobe/whosaidso/internal/write"
)

// galleryUI starts a viewer over a temp home holding team/ok's gallery and
// returns the listener, the session and that gallery's file.
func galleryUI(t *testing.T, w *stubWorld) (host string, session sess, file string) {
	t.Helper()
	home := t.TempDir()
	if err := testaccess.MakePrivate(home); err != nil {
		t.Fatal(err)
	}
	t.Setenv(store.HomeEnv, home)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "whosaidso.toml"), []byte("id = 'team/ok'\nledger = '.whosaidso/events'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := galleryProject(t, root)
	w.projects = []store.Registration{{ID: p.ID, Home: root, Project: p}}
	host, token := testUI(t, w)
	file = root

	return host, enter(t, host, token), file
}

// shown writes a file and adds it to the gallery, returning its entry id.
func shown(t *testing.T, file, name string, content []byte) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	e := addShown(t, file, path, "the "+name, "")

	return string(e.EntryID), path
}

// ask sends one request with any extra headers and returns status, headers
// and body.
func ask(t *testing.T, host, method, path, origin string, s sess, extra ...string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+host+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if s.cookie != "" {
		req.Header.Set("Cookie", s.cookie)
	}
	if s.key != "" {
		req.Header.Set("X-WhoSaidSo-Key", s.key)
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

// capOf is entry id's file capability, read from the list as the page reads it.
func capOf(t *testing.T, host string, s sess, id string) string {
	t.Helper()
	got, _, body := ask(t, host, "GET", "/api/gallery?project=team%2Fok", "", s)
	var list struct{ Entries []struct{ ID, Cap string } }
	if err := json.Unmarshal(body, &list); got != 200 || err != nil {
		t.Fatalf("list: %d %v %s", got, err, body)
	}
	for _, e := range list.Entries {
		if e.ID == id {
			return e.Cap
		}
	}
	t.Fatalf("no entry %s listed", id)
	return ""
}

// fileURL is entry id's file URL as the page builds it, capability included.
func fileURL(t *testing.T, host string, s sess, id string) string {
	t.Helper()
	return "/api/gallery/file?project=team%2Fok&id=" + id + "&cap=" + capOf(t, host, s, id)
}

func galleryProject(t *testing.T, root string) store.Project {
	t.Helper()
	p, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func addShown(t *testing.T, root, path, title, note string) model.GalleryAdd {
	t.Helper()
	p := galleryProject(t, root)
	by := model.Actor{ID: "contributor"}
	e, err := gallery.Prepare(root, path, title, note, by, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	packet, err := gallery.Capture(context.Background(), p, by, &e)
	if err != nil {
		t.Fatal(err)
	}
	command, err := model.NewID(time.Now(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, err = write.Admit(context.Background(), p, write.AdmitRequest{CommandID: command, PacketIDs: []model.ID{packet.CommandID}, Admitter: model.Actor{ID: "reviewer"}, Outcome: "accepted", Reason: "fixture reviewed"})
	if err != nil {
		t.Fatal(err)
	}
	return e
}
