package main

// Gallery integration uses synthetic tasks, local media and an isolated home.
// A numeric witness cannot replace the named actor's admitted visual verdict.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/gallery"
	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
	"github.com/alex2481kobe/whosaidso/internal/ui"
)

func galleryCommand(t *testing.T, root string, args ...string) (string, string) {
	t.Helper()
	out, errout, code := cliRun(t, root, nil, "contributor", args...)
	if code != 0 {
		t.Fatalf("%v: %d %s %s", args, code, out, errout)
	}
	return out, errout
}

func galleryEntryID(t *testing.T, diagnostic string) string {
	t.Helper()
	fields := strings.Fields(diagnostic)
	for i, s := range fields {
		if s == "entry" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	t.Fatalf("entry id missing: %s", diagnostic)
	return ""
}

func galleryCaptureEvents(t *testing.T, root string, events ...model.TypedEvent) string {
	t.Helper()
	raw := []model.Event{}
	for _, e := range events {
		r, err := model.EncodeEvent(e)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, r)
	}
	data, err := model.Encode(raw)
	if err != nil {
		t.Fatal(err)
	}
	out, errs, code := cliRun(t, root, data, "contributor", "capture", "--admit", "--reason", "fixture reviewed")
	if code != 0 {
		t.Fatalf("capture events: %d %s %s", code, out, errs)
	}
	return out
}

func TestGalleryVisualAcceptanceEndToEnd(t *testing.T) {
	t.Setenv(store.HomeEnv, t.TempDir())
	root, data := cliFixture(t)
	cliControl(t, root, data)
	video := filepath.Join(root, "clip.mp4")
	videoBytes, err := os.ReadFile("testdata/gallery/clip.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(video, videoBytes, 0600); err != nil {
		t.Fatal(err)
	}
	// Capture alone is pending, never a second source of gallery truth.
	_, diagnostic := galleryCommand(t, root, "gallery", "add", "clip.mp4", "--title", "Arm pose", "--task", string(cliID(1)))
	entry := galleryEntryID(t, diagnostic)
	out, _ := galleryCommand(t, root, "gallery", "list", "--json")
	if !strings.Contains(out, `"entries":[]`) {
		t.Fatalf("pending entry appeared: %s", out)
	}
	p, _ := store.Discover(root)
	packets, err := store.IntakeDir(p)
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := os.ReadDir(packets)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		if dir.Name() != string(cliID(3)) {
			galleryCommand(t, root, "admit", dir.Name(), "--outcome", "accepted", "--reason", "entry reviewed")
		}
	}
	task := model.RecordRef{Project: p.ID, RecordID: cliID(1), Revision: 1}
	galleryCaptureEvents(t, root, &model.GalleryRequire{Task: task, CriterionID: cliID(2), CriterionRevision: 1, EntryID: model.ID(entry), Approver: model.Actor{ID: "reviewer"}})
	witness := []byte("passing number")
	if err := os.WriteFile(filepath.Join(root, "result.txt"), witness, 0600); err != nil {
		t.Fatal(err)
	}
	pin := model.ArtifactRef{Kind: "content", Content: &model.ContentPin{SHA256: model.HashBytes(witness), Length: uint64(len(witness)), MediaType: "text/plain", Locators: []model.Locator{{Path: "result.txt"}}}, Selector: model.Selector{Kind: "whole"}}
	closure := &model.TaskClose{Task: task, Outcome: model.ClosureSuccess, AcceptanceWitnessRefs: []model.AcceptanceWitness{{CriterionID: cliID(2), CriterionRevision: 1, WitnessRef: pin}}, DeliveryWitnessRefs: []model.ArtifactRef{pin}}
	raw, _ := model.EncodeEvent(closure)
	events, _ := model.Encode([]model.Event{raw})
	_, errs, code := cliRun(t, root, events, "contributor", "capture", "--admit", "--reason", "close reviewed")
	if code == 0 || !strings.Contains(errs, "needs admitted gallery approval") {
		t.Fatalf("number closed unapproved visual task: %d %s", code, errs)
	}
	// A captured approval still does not count before admission.
	approval, _ := galleryCommand(t, root, "gallery", "approve", entry, "--by", "reviewer")
	approvalPacket := strings.Fields(approval)[1]
	_, errs, code = cliRun(t, root, events, "contributor", "capture", "--admit", "--reason", "close reviewed")
	if code == 0 || !strings.Contains(errs, "needs admitted gallery approval") {
		t.Fatalf("pending approval counted: %d %s", code, errs)
	}
	galleryCommand(t, root, "admit", approvalPacket, "--outcome", "accepted", "--reason", "visual inspected")
	galleryCaptureEvents(t, root, closure)
	state, err := store.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	projected, ok := state.Snapshot().Task(reduce.Ident{Project: p.ID, ID: cliID(1)})
	if !ok || projected.Status != reduce.StatusClosed {
		t.Fatalf("approved task not closed: %+v", projected)
	}
	out, _ = galleryCommand(t, root, "gallery", "list", "--json")
	if !strings.Contains(out, `"verdict":"approved"`) || !strings.Contains(out, `"status":"present"`) {
		t.Fatalf("approved entry: %s", out)
	}
	if err := os.WriteFile(video, []byte("different pose"), 0600); err != nil {
		t.Fatal(err)
	}
	out, _ = galleryCommand(t, root, "gallery", "list", "--json")
	if !strings.Contains(out, `"status":"changed"`) {
		t.Fatalf("replacement not flagged: %s", out)
	}
	if _, _, code := cliRun(t, root, nil, "", "gallery", "approve", entry, "--by", "reviewer"); code == 0 {
		t.Fatal("approved changed bytes")
	}
	// The UI reads that same ledger pin and verdict, with local changed status.
	cfg := uiConfig(root)
	ln, srv, address, err := ui.Listen(cfg)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	session := uiSession(t, address)
	status, body := uiGet(t, address, session, "/api/gallery?project=test%2Fcli")
	if status != 200 || !strings.Contains(body, `"status":"changed"`) || !strings.Contains(body, `"verdict":"approved"`) {
		t.Fatalf("ui: %d %s", status, body)
	}
	galleryCommand(t, root, "gallery", "rm", entry, "--admit", "--reason", "retire preview")
	out, _ = galleryCommand(t, root, "gallery", "list")
	if !strings.Contains(out, "empty") {
		t.Fatalf("retraction not projected: %s", out)
	}
	if b, err := os.ReadFile(video); err != nil || string(b) != "different pose" {
		t.Fatalf("retraction touched media: %q %v", b, err)
	}
}

func TestGalleryImportLeavesLegacyFileAndUsesOnlyLedger(t *testing.T) {
	t.Setenv(store.HomeEnv, t.TempDir())
	root, _ := cliFixture(t)
	path := filepath.Join(t.TempDir(), "clip.mov")
	if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	legacy, err := store.GalleryFile("test/cli")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	before := []byte(`[{"id":"old-entry","path":` + strconvQuote(path) + `,"title":"Old render","added_at":"2026-01-01T00:00:00Z"}]`)
	if err := os.WriteFile(legacy, before, 0600); err != nil {
		t.Fatal(err)
	}
	out, _ := galleryCommand(t, root, "gallery", "list")
	if !strings.Contains(out, "empty") {
		t.Fatalf("legacy read before import: %s", out)
	}
	galleryCommand(t, root, "gallery", "import", "--admit", "--reason", "import reviewed")
	after, err := os.ReadFile(legacy)
	if err != nil || string(after) != string(before) {
		t.Fatalf("import changed old file: %s %v", after, err)
	}
	galleryCommand(t, root, "gallery", "import", "--admit", "--reason", "retry")
	if err := os.WriteFile(legacy, []byte("broken legacy list"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, code := cliRun(t, root, nil, "contributor", "gallery", "import", "--admit", "--reason", "invalid source"); code == 0 {
		t.Fatal("invalid replacement list accepted")
	}
	out, _ = galleryCommand(t, root, "gallery", "list", "--json")
	var list struct{ Entries []struct{ ID string } }
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list.Entries) != 1 {
		t.Fatalf("dual reading or duplicate import: %s %v", out, err)
	}
	p, _ := store.Discover(root)
	prefix, err := store.ReadPrefix(p)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := reduce.Replay(prefix)
	if err != nil || len(snap.Gallery()) != 1 {
		t.Fatalf("import not in ledger: %v", err)
	}
	if got := snap.Gallery()[0].AddedAt.Format(time.RFC3339); got != "2026-01-01T00:00:00Z" {
		t.Fatalf("import re-dated entry: got %s", got)
	}
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestGalleryLedgerTravelsWithoutMedia(t *testing.T) {
	t.Setenv(store.HomeEnv, t.TempDir())
	root, _ := cliFixture(t)
	if err := os.WriteFile(filepath.Join(root, "clip.mp4"), []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	galleryCommand(t, root, "gallery", "add", "clip.mp4", "--title", "Portable entry", "--admit", "--reason", "entry reviewed")
	first, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	config, err := os.ReadFile(filepath.Join(root, "whosaidso.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "whosaidso.toml"), config, 0600); err != nil {
		t.Fatal(err)
	}
	second, err := store.Discover(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(second.Ledger, 0700); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(first.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if filepath.Ext(file.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(first.Ledger, file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(second.Ledger, file.Name()), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := gallery.List(second)
	if err != nil || len(entries) != 1 || entries[0].Title != "Portable entry" || entries[0].Status != "missing" {
		t.Fatalf("ledger-only clone lost gallery or required media: %+v %v", entries, err)
	}
}
