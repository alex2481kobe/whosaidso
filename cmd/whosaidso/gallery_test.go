package main

// The gallery is not a record: its verbs and the viewer's Gallery actions
// leave every ledger, artifact and intake byte as it was; its file lives in
// the machine's home, never inside the project; rm leaves the file; and add
// refuses a path that is not there.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/store"
	"github.com/alex2481kobe/whosaidso/internal/ui"
)

// recordBytes is every file under the project's .whosaidso (but the
// disposable snapshot cache) and under the machine home (but the gallery),
// each with its content hash.
func recordBytes(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	home, err := store.Home()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][32]byte{}
	for _, dir := range []struct{ base, skip string }{{filepath.Join(root, ".whosaidso"), "cache"}, {home, "gallery"}} {
		err := filepath.WalkDir(dir.base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == dir.skip && filepath.Dir(path) == dir.base {
				return filepath.SkipDir
			}
			if !d.IsDir() {
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				out[path] = sha256.Sum256(b)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func sameBytes(t *testing.T, before, after map[string][32]byte, when string) {
	t.Helper()
	if len(before) != len(after) {
		t.Errorf("%s: %d record files became %d", when, len(before), len(after))
	}
	for path, sum := range before {
		if after[path] != sum {
			t.Errorf("%s changed %s", when, path)
		}
	}
}

func TestGalleryLeavesTheLedgerAndTheFileUntouched(t *testing.T) {
	root, data := cliFixture(t)
	cliControl(t, root, data)
	if err := os.WriteFile(filepath.Join(root, "shot.png"), []byte("png bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := recordBytes(t, root)

	out, errs, code := cliRun(t, root, nil, "agent-7", "gallery", "add", "shot.png", "--title", "the shot", "--note", "check the edge")
	if code != 0 || !strings.HasPrefix(out, "added ") {
		t.Fatalf("add: %d %s %s", code, out, errs)
	}
	id := strings.Fields(out)[1]
	if _, _, code := cliRun(t, root, nil, "", "gallery", "add", "gone.png", "--title", "gone"); code == 0 {
		t.Fatal("add accepted a missing path")
	}
	out, _, code = cliRun(t, root, nil, "", "gallery", "list", "--json")
	var list struct{ Entries []map[string]any }
	if err := json.Unmarshal([]byte(out), &list); code != 0 || err != nil || len(list.Entries) != 1 {
		t.Fatalf("list: %d %v %s", code, err, out)
	}
	if e := list.Entries[0]; e["id"] != id || e["path"] != filepath.Join(root, "shot.png") || e["added_by"] != "agent-7" || e["status"] != "present" || e["note"] != "check the edge" {
		t.Fatalf("listed entry: %v", e)
	}
	galleryFile, err := store.GalleryFile("test/cli")
	if err != nil {
		t.Fatal(err)
	}
	if rel, err := filepath.Rel(root, galleryFile); err != nil || !strings.HasPrefix(rel, "..") {
		t.Fatalf("the gallery file %s is inside the project %s", galleryFile, root)
	}
	sameBytes(t, before, recordBytes(t, root), "gallery add and list")

	// The viewer's Gallery: list, file, reveal and remove.
	var revealed []string
	cfg := uiConfig(root)
	cfg.Reveal = func(p string) error { revealed = append(revealed, p); return nil }
	ln, server, address, err := ui.Listen(cfg)
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(ln)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	u, _ := url.Parse(address)
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(address)
	if err != nil || len(resp.Cookies()) != 1 {
		t.Fatalf("exchange: %v", err)
	}
	resp.Body.Close()
	cookie := resp.Cookies()[0]
	query := "?project=test%2Fcli&id=" + id
	for _, c := range []struct{ method, path, want string }{
		{"GET", "/api/gallery?project=test%2Fcli", `"title":"the shot"`},
		{"GET", "/api/gallery/file" + query, "png bytes"},
		{"POST", "/api/gallery/reveal" + query, `"revealed"`},
		{"POST", "/api/gallery/remove" + query, `"removed"`},
	} {
		req, _ := http.NewRequest(c.method, "http://"+u.Host+c.path, nil)
		req.AddCookie(cookie)
		req.Header.Set("Origin", "http://"+u.Host)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(body), c.want) {
			t.Fatalf("%s %s: %d %s", c.method, c.path, resp.StatusCode, body)
		}
	}
	if len(revealed) != 1 || revealed[0] != filepath.Join(root, "shot.png") {
		t.Fatalf("revealed %v", revealed)
	}
	if _, _, code := cliRun(t, root, nil, "", "gallery", "rm", id); code == 0 {
		t.Fatal("rm of an entry the viewer removed succeeded")
	}
	sameBytes(t, before, recordBytes(t, root), "the viewer's gallery actions")
	if b, err := os.ReadFile(filepath.Join(root, "shot.png")); err != nil || string(b) != "png bytes" {
		t.Fatalf("removal touched the file: %q %v", b, err)
	}
}

func TestGalleryRmLeavesTheFile(t *testing.T) {
	root, _ := cliFixture(t)
	path := filepath.Join(t.TempDir(), "run.log")
	if err := os.WriteFile(path, []byte("log"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code := cliRun(t, root, nil, "", "gallery", "add", path, "--title", "the run")
	if code != 0 {
		t.Fatalf("add: %s", out)
	}
	if out, errs, code := cliRun(t, root, nil, "", "gallery", "rm", strings.Fields(out)[1]); code != 0 || !strings.Contains(out, "untouched") {
		t.Fatalf("rm: %d %s %s", code, out, errs)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "log" {
		t.Fatalf("rm touched the file: %q %v", b, err)
	}
	if out, _, _ := cliRun(t, root, nil, "", "gallery", "list"); !strings.Contains(out, "empty") {
		t.Fatalf("list after rm: %s", out)
	}
}
