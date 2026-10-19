// Gallery retraction capture preserves its configured author and remains
// pending until independently admitted; media and read endpoints are separate.
package ui

import (
	"context"
	"crypto/rand"
	"encoding/json"
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

func TestGalleryRemovalCanBeAdmittedUnderStrictPolicy(t *testing.T) {
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
	var receipt struct {
		Captured model.ID `json:"captured"`
	}
	if err := json.Unmarshal(body, &receipt); err != nil || status != 200 || !model.ValidID(receipt.Captured) {
		t.Fatalf("capture: %d %s %v", status, body, err)
	}
	packets, err := store.ReadIntake(p, []model.ID{receipt.Captured})
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
	_, err = write.Admit(context.Background(), p, write.AdmitRequest{CommandID: command, PacketIDs: []model.ID{receipt.Captured}, Admitter: model.Actor{ID: "reviewer"}, Outcome: "accepted", Reason: "retraction reviewed"})
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
