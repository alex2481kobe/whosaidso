// External media is verified at admission without storing its bytes. These
// tests also exercise raw packets, so CLI preflight cannot hide a bypass.
package write

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/gallery"
	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestGalleryAdmissionVerifiesCurrentBytesAndDoesNotStoreMedia(t *testing.T) {
	for _, mode := range []string{"control", "changed-before-add", "changed-before-approve", "changed-before-close", "missing-before-close"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("WHOSAIDSO_HOME", filepath.Join(t.TempDir(), "home"))
			w := newCloseWorld(t, true)
			f := w.f
			path := filepath.Join(f.project.Root, "clip.mp4")
			if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
				t.Fatal(err)
			}
			reviewer := f.author
			f.author = model.Actor{ID: "maker"}
			add, err := gallery.Prepare(f.project.Root, path, "Rendered pose", "", f.author, nil, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			packet := f.capture(nil, &add)
			f.author = reviewer
			change := func() {
				if err := os.WriteFile(path, []byte("other"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "changed-before-add" {
				change()
				f.refuse(f.request(packet), "gallery-changed")
				return
			}
			f.accept(packet)
			if _, err := os.Stat(filepath.Join(f.project.Root, f.project.ArtifactDir(), string(add.File.SHA256))); !os.IsNotExist(err) {
				t.Fatalf("gallery media copied into artifact store: %v", err)
			}
			criterion := w.spec.AcceptanceCriteria[0]
			f.accept(f.capture(nil, &model.GalleryRequire{Task: w.task, CriterionID: criterion.ID, CriterionRevision: criterion.Revision, EntryID: add.EntryID, Approver: f.author}))
			approval := f.capture(nil, &model.GalleryApprove{EntryID: add.EntryID, SHA256: add.File.SHA256, Actor: f.author})
			if mode == "changed-before-approve" {
				change()
				f.refuse(f.request(approval), "gallery-changed")
				return
			}
			f.accept(approval)
			if mode == "changed-before-close" {
				change()
			}
			if mode == "missing-before-close" {
				if err := os.Rename(path, path+".moved"); err != nil {
					t.Fatal(err)
				}
			}
			close := w.closure(model.ClosureSuccess)
			close.Authority = nil
			request := f.request(f.capture(nil, close))
			if mode == "changed-before-close" {
				f.refuse(request, "gallery-changed")
				return
			}
			if mode == "missing-before-close" {
				f.refuse(request, "gallery-unavailable")
				return
			}
			if _, err := Admit(context.Background(), f.project, request); err != nil {
				t.Fatalf("control: %v", err)
			}
		})
	}
}

func TestGalleryPacketSetOrdersEntryBeforeVerdictAndImport(t *testing.T) {
	t.Setenv("WHOSAIDSO_HOME", filepath.Join(t.TempDir(), "home"))
	f := newAdmissionFixture(t)
	path := filepath.Join(f.project.Root, "clip.mp4")
	if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	add, err := gallery.Prepare(f.project.Root, path, "Render", "", f.author, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	inventory := []model.Digest{model.HashBytes([]byte("legacy"))}
	source := model.GallerySource(inventory)
	add.ImportID = string(source) + ":" + string(inventory[0])
	marker := f.capture(nil, &model.GalleryImport{Source: source, Inventory: inventory, Entries: []model.ID{add.EntryID}})
	verdict := f.capture(nil, &model.GalleryApprove{EntryID: add.EntryID, SHA256: add.File.SHA256, Actor: f.author})
	entry := f.capture(nil, &add)
	f.accept(marker, verdict, entry)
	entries := f.snapshot().Gallery()
	if len(entries) != 1 || entries[0].Verdict != "approved" {
		t.Fatalf("packet dependencies not applied: %+v", entries)
	}
}

func TestGalleryAdmissionRefusesRelativeEscapes(t *testing.T) {
	for _, mode := range []string{"parent", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("WHOSAIDSO_HOME", filepath.Join(t.TempDir(), "home"))
			f := newAdmissionFixture(t)
			outside := filepath.Join(t.TempDir(), "outside.mp4")
			if err := os.WriteFile(outside, []byte("video"), 0600); err != nil {
				t.Fatal(err)
			}
			add, err := gallery.Prepare(f.project.Root, outside, "Render", "", f.author, nil, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if mode == "parent" {
				add.File.Path, err = filepath.Rel(f.project.Root, outside)
			} else {
				add.File.Path = "link.mp4"
				err = os.Symlink(outside, filepath.Join(f.project.Root, add.File.Path))
			}
			if err != nil {
				t.Fatal(err)
			}
			packet := f.capture(nil, &add)
			if _, err := Admit(context.Background(), f.project, f.request(packet)); err == nil {
				t.Fatal("admission accepted relative escape")
			}
		})
	}
}
