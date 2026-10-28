// Viewer deletion records and admits a retraction as its configured actor.
// Strict admission policy still refuses self-admission and leaves media intact.
package ui

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/gallery"
	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
	"github.com/alex2481kobe/whosaidso/internal/testaccess"
	"github.com/alex2481kobe/whosaidso/internal/write"
)

func TestStrictPolicyRefusesViewerSelfAdmissionAndAllowsIndependentReview(t *testing.T) {
	home := t.TempDir()
	if err := testaccess.MakePrivate(home); err != nil {
		t.Fatal(err)
	}
	t.Setenv(store.HomeEnv, home)
	root := t.TempDir()
	config := "id = 'team/ok'\nledger = '.whosaidso/events'\nself_admission = 'refuse'\n"
	if err := os.WriteFile(filepath.Join(root, "whosaidso.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	p := galleryProject(t, root)
	world := &stubWorld{captureActor: model.Actor{ID: "contributor"}, projects: []store.Registration{{ID: p.ID, Home: root, Project: p}}}
	host, token := testUI(t, world)
	session := enter(t, host, token)
	id, path := shown(t, root, "clip.mp4", []byte("video"))
	status, _, body := ask(t, host, "POST", "/api/gallery/remove?project=team%2Fok&id="+id, "http://"+host, session)
	if status != 409 || !strings.Contains(string(body), "self-admission") {
		t.Fatalf("strict refusal: %d %s", status, body)
	}
	pending, err := store.ReadIntake(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	var captured model.ID
	for _, packet := range pending {
		if len(packet.Events) == 1 && packet.Events[0].Type == "gallery.retract" {
			if captured != "" {
				t.Fatal("duplicate retraction capture")
			}
			captured = packet.CommandID
		}
	}
	if !model.ValidID(captured) {
		t.Fatal("missing retraction capture")
	}
	packets, err := store.ReadIntake(p, []model.ID{captured})
	if err != nil || len(packets) != 1 || packets[0].Author.ID != "contributor" {
		t.Fatalf("viewer lost its actor: %+v %v", packets, err)
	}
	entries, err := gallery.List(p)
	if err != nil || len(entries) != 1 {
		t.Fatalf("capture published prematurely: %+v %v", entries, err)
	}
	command, err := model.NewID(time.Now(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, err = write.Admit(context.Background(), p, write.AdmitRequest{CommandID: command, PacketIDs: []model.ID{captured}, Admitter: model.Actor{ID: "reviewer"}, Outcome: "accepted", Reason: "retraction reviewed"})
	if err != nil {
		t.Fatalf("independent admission of viewer retraction failed: %v", err)
	}
	entries, err = gallery.List(p)
	if err != nil || len(entries) != 0 {
		t.Fatalf("admitted retraction still shown: %+v %v", entries, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "video" {
		t.Fatalf("retraction touched media: %s %v", data, err)
	}
}

func TestViewerDeletionAdmitsAttributedRetractionAndRetainsTheFile(t *testing.T) {
	world := &stubWorld{captureActor: model.Actor{ID: "contributor"}}
	host, session, root := galleryUI(t, world)
	id, path := shown(t, root, "kept.png", []byte("source bytes"))
	original := fileURL(t, host, session, id)
	code, _, body := ask(t, host, "POST", "/api/gallery/remove?project=team%2Fok&id="+id, "http://"+host, session)
	if code != 200 || !strings.Contains(string(body), `"removed":"`+id+`"`) {
		t.Fatalf("delete: %d %s", code, body)
	}
	p := galleryProject(t, root)
	state, err := store.Replayed(p)
	if err != nil {
		t.Fatal(err)
	}
	entries := state.Snapshot().Gallery()
	if len(entries) != 1 || !entries[0].Retracted {
		t.Fatalf("not retracted: %+v", entries)
	}
	bundles, err := state.Bundles()
	if err != nil {
		t.Fatal(err)
	}
	last := bundles[len(bundles)-1]
	event, err := model.DecodeEvent(last.Events[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := event.(*model.GalleryRetract); !ok || last.Admitter.ID != "contributor" {
		t.Fatal("delete did not admit the owner's retraction")
	}
	review, err := model.DecodeEvent(last.Events[len(last.Events)-1])
	if err != nil {
		t.Fatal(err)
	}
	receipt := review.(*model.ReviewAdmit)
	if len(receipt.Authors) != 1 {
		t.Fatal("missing author")
	}
	for _, author := range receipt.Authors {
		if author.ID != "contributor" {
			t.Fatal("retraction changed author")
		}
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "source bytes" {
		t.Fatalf("delete touched source: %q %v", data, err)
	}
	if code, _, _ := ask(t, host, "GET", original, "", session); code != 404 {
		t.Fatalf("old capability opened retracted entry: %d", code)
	}
	before, err := store.IntakeIDs(p)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, _ := ask(t, host, "POST", "/api/gallery/remove?project=team%2Fok&id="+id, "http://"+host, session); code != 404 {
		t.Fatalf("repeat delete: %d", code)
	}
	after, err := store.IntakeIDs(p)
	if err != nil || len(before) != len(after) {
		t.Fatalf("retracted entry captured another packet: %v %v", after, err)
	}
	again, err := store.Load(p)
	if err != nil || again.Snapshot().Watermark() != state.Snapshot().Watermark() {
		t.Fatal("repeat delete changed the ledger")
	}

}
