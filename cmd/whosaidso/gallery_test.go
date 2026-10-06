package main

// The gallery is not a record: its verbs and the viewer's Gallery actions
// leave every ledger, artifact and intake byte as it was; its file lives in
// the machine's home, never inside the project; rm leaves the file; and add
// refuses a path that is not there.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/store"
	"github.com/alex2481kobe/whosaidso/internal/ui"
)

// recordBytes hashes the content and mode of every file under the resolved
// project root (but its disposable snapshot cache) and the resolved machine
// home (but the gallery directory, which is expected to change). It first
// checks that the places the project really stores records, its ledger, its
// artifact store and its intake as store.Open resolves them, lie inside those
// two trees, so the comparison covers them and not a spelling of them.
//
// Blind to: writes outside those two trees (another home, a temp directory);
// a change made and undone between the two readings; timestamps and extended
// attributes; and the snapshot cache, which is derived data.
func recordBytes(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	p, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	home, err := store.Home()
	if err != nil {
		t.Fatal(err)
	}
	intake, err := store.IntakeDir(p)
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(path string) string {
		// A location not created yet resolves through its nearest parent.
		for missing := ""; ; path, missing = filepath.Dir(path), filepath.Join(filepath.Base(path), missing) {
			if real, err := filepath.EvalSymlinks(path); err == nil {
				return filepath.Join(real, missing)
			}
		}
	}
	trees := []struct{ base, skip string }{{resolve(p.Root), filepath.Join(resolve(p.Root), ".whosaidso", "cache")}, {resolve(home), filepath.Join(resolve(home), "gallery")}}
	for _, place := range []string{p.Ledger, filepath.Join(p.Root, p.ArtifactDir()), intake} {
		covered := false
		for _, tree := range trees {
			rel, err := filepath.Rel(tree.base, resolve(place))
			covered = covered || err == nil && !strings.HasPrefix(rel, "..")
		}
		if !covered {
			t.Fatalf("%s is outside every compared tree", place)
		}
	}
	out := map[string][32]byte{}
	for _, tree := range trees {
		err := filepath.WalkDir(tree.base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && path == tree.skip {
				return filepath.SkipDir
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[path] = sha256.Sum256(append(b, []byte(info.Mode().String())...))
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
		t.Errorf("%s: %d files became %d", when, len(before), len(after))
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
	for _, name := range []string{"shot.png", "run.log"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name+" bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before := recordBytes(t, root)

	out, errs, code := cliRun(t, root, nil, "agent-7", "gallery", "add", "shot.png", "--title", "the shot", "--note", "check the edge")
	if code != 0 || !strings.HasPrefix(out, "added ") {
		t.Fatalf("add: %d %s %s", code, out, errs)
	}
	id := strings.Fields(out)[1]
	out, _, code = cliRun(t, root, nil, "", "gallery", "add", "run.log", "--title", "the run")
	if code != 0 {
		t.Fatalf("add: %s", out)
	}
	logID := strings.Fields(out)[1]
	if _, _, code := cliRun(t, root, nil, "", "gallery", "add", "gone.png", "--title", "gone"); code == 0 {
		t.Fatal("add accepted a missing path")
	}
	out, _, code = cliRun(t, root, nil, "", "gallery", "list", "--json")
	var list struct{ Entries []map[string]any }
	if err := json.Unmarshal([]byte(out), &list); code != 0 || err != nil || len(list.Entries) != 2 {
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
	session := uiSession(t, address)
	status, listed := uiGet(t, address, session, "/api/gallery?project=test%2Fcli")
	var viewed struct{ Entries []struct{ ID, Cap string } }
	if err := json.Unmarshal([]byte(listed), &viewed); status != 200 || err != nil || len(viewed.Entries) != 2 || viewed.Entries[0].Cap == "" {
		t.Fatalf("viewer list: %d %v %s", status, err, listed)
	}
	query := "?project=test%2Fcli&id=" + id
	for _, c := range []struct{ method, path, want string }{
		{"GET", "/api/gallery/file" + query + "&cap=" + viewed.Entries[0].Cap, "shot.png bytes"},
		{"POST", "/api/gallery/reveal" + query, `"revealed"`},
		{"POST", "/api/gallery/remove" + query, `"removed"`},
	} {
		if status, body := uiRequest(t, address, session, c.method, c.path); status != 200 || !strings.Contains(body, c.want) {
			t.Fatalf("%s %s: %d %s", c.method, c.path, status, body)
		}
	}
	if len(revealed) != 1 || revealed[0] != filepath.Join(root, "shot.png") {
		t.Fatalf("revealed %v", revealed)
	}
	if _, _, code := cliRun(t, root, nil, "", "gallery", "rm", id); code == 0 {
		t.Fatal("rm of an entry the viewer removed succeeded")
	}
	if out, errs, code := cliRun(t, root, nil, "", "gallery", "rm", logID); code != 0 || !strings.Contains(out, "untouched") {
		t.Fatalf("rm: %d %s %s", code, out, errs)
	}
	if entries, _, _ := cliRun(t, root, nil, "", "gallery", "list"); !strings.Contains(entries, "empty") {
		t.Fatalf("the gallery after both removals: %s", entries)
	}
	// The project tree includes both shown files, so this also proves
	// neither removal touched its file.
	sameBytes(t, before, recordBytes(t, root), "the viewer's gallery actions and gallery rm")
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
