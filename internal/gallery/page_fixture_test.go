// Synthetic projects and admitted events support gallery page behavior tests.
// The fixtures never depend on this checkout's ledger, intake, or media.
package gallery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func pageID(n int) model.ID { return model.ID(fmt.Sprintf("%026d", n)) }
func pageProject(t *testing.T, count int) store.Project {
	t.Helper()
	t.Setenv(store.HomeEnv, t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "whosaidso.toml"), []byte("id = 'test/pages'\nledger = '.whosaidso/events'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	events := []model.TypedEvent{}
	for i := 0; i < count; i++ {
		ext := []string{".png", ".mp4", ".txt"}[i%3]
		path := fmt.Sprintf("screen-%02d%s", i, ext)
		if err := os.WriteFile(filepath.Join(root, path), []byte("media"), 0600); err != nil {
			t.Fatal(err)
		}
		note := ""
		if i == 1 || i == 29 {
			note = "needle"
		}
		title := fmt.Sprintf("Screen %02d", i)
		if i == 50 {
			title = "Alpha"
		}
		events = append(events, &model.GalleryAdd{EntryID: pageID(100 + i), AddedAt: time.Unix(1700000000+int64(i), 0).UTC(), File: model.GalleryFile{Path: path, SHA256: model.HashBytes([]byte("media")), Length: 5}, Title: title, Note: note, Actor: model.Actor{ID: "contributor"}, Tasks: []model.RecordRef{}})
	}
	pageAdmit(t, p, events...)
	return p
}

func pageAdmit(t *testing.T, p store.Project, events ...model.TypedEvent) {
	t.Helper()
	state, err := store.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	sequence := int(state.Snapshot().Watermark().Sequence) + 1
	packet := pageID(10000 + sequence)
	ref := model.PacketRef{CommandID: packet, Digest: model.HashBytes([]byte(fmt.Sprint(sequence)))}
	raw := []model.Event{}
	carried := []model.ID{}
	for _, event := range events {
		encoded, err := model.EncodeEvent(event)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, encoded)
		carried = append(carried, packet)
	}
	now := time.Unix(1750000000, 0).UTC()
	review, err := model.EncodeEvent(&model.ReviewAdmit{Packets: []model.PacketRef{ref}, Outcome: "accepted", Actor: model.Actor{ID: "reviewer"}, Reason: "synthetic fixture reviewed", EventPackets: carried, Authors: map[model.ID]model.Actor{packet: {ID: "contributor"}}, CapturedAt: map[model.ID]model.Availability[time.Time]{packet: {State: model.Known, Value: &now}}})
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, review)
	_, err = store.Transact(context.Background(), p, pageID(20000+sequence), model.HashBytes([]byte(fmt.Sprint(sequence))), func([]model.Bundle) (model.Bundle, error) {
		return model.Bundle{Admitter: model.Actor{ID: "reviewer"}, Packets: []model.PacketRef{ref}, Events: raw}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Entries(p); err != nil {
		t.Fatal(err)
	}
}
