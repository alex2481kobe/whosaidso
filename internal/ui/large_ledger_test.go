// Large synthetic ledgers measure screen responses and first-screen image bytes.
// These HTTP timings exclude browser layout, paint and native file managers.
package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/gallery"
	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func largeID(n int) model.ID { return model.ID(fmt.Sprintf("%026d", n)) }

func largeLedger(t *testing.T) (string, sess, store.Project) {
	t.Helper()
	world := &stubWorld{}
	host, session, root := galleryUI(t, world)
	p := galleryProject(t, root)
	if err := os.MkdirAll(p.Ledger, 0700); err != nil {
		t.Fatal(err)
	}
	scope := model.Scope{SourcePaths: []string{"example"}, ContextRefs: []model.RecordRef{}, AppliesWhen: "synthetic test", Limitations: "no production claims"}
	task := model.TaskSpec{Intent: "Synthetic task", Subject: "screen fixture", Scope: scope, NonGoals: []string{"production use"}, AcceptanceCriteria: []model.AcceptanceCriterion{{ID: largeID(900000), Revision: 1, Criterion: "screen is bounded"}}, ContextRefs: []model.RecordRef{}, ConstraintRefs: []model.RecordRef{}, Prerequisites: []model.Prerequisite{}, NextActor: model.Actor{ID: "contributor"}}
	claim := model.ClaimSpec{Assertion: "Synthetic record", Falsifier: "fixture differs", Scope: scope, ExternalRefs: []model.ExternalReference{}}
	provenance := model.Provenance{SourceRefs: []model.ArtifactRef{}}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pins := make([]model.GalleryFile, 12)
	for i := range pins {
		img := image.NewNRGBA(image.Rect(0, 0, 1600, 900))
		for y := 0; y < 900; y++ {
			for x := 0; x < 1600; x++ {
				img.SetNRGBA(x, y, color.NRGBA{uint8(x + i*17), uint8(y), uint8(x*y + i), 255})
			}
		}
		var b bytes.Buffer
		if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 85}); err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("image-%02d.jpg", i)
		if err := os.WriteFile(filepath.Join(root, name), b.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		pins[i] = model.GalleryFile{Path: name, SHA256: model.HashBytes(b.Bytes()), Length: uint64(b.Len())}
	}
	if err := os.WriteFile(filepath.Join(root, "fixture.png"), []byte("not media"), 0600); err != nil {
		t.Fatal(err)
	}
	previous := model.ID("")
	totalBytes := 0
	for batch := 0; batch < 600; batch++ {
		packet := largeID(200000 + batch)
		ref := model.PacketRef{CommandID: packet, Digest: model.HashBytes([]byte(fmt.Sprint(batch)))}
		b := model.Bundle{Version: 1, Project: p.ID, Sequence: uint64(batch + 1), CommandID: largeID(300000 + batch), Predecessor: previous, RequestDigest: ref.Digest, Admitter: model.Actor{ID: "reviewer"}, RecordedAt: now, Packets: []model.PacketRef{ref}}
		carried := []model.ID{}
		for j := 0; j < 99; j++ {
			n := batch*99 + j
			var e model.TypedEvent
			switch {
			case n < 15000:
				e = &model.TaskCreate{ID: largeID(n + 1), Provenance: provenance, Spec: task}
			case n < 20000:
				e = &model.ClaimAssert{ID: largeID(n + 1), Provenance: provenance, Spec: claim}
			case n < 21800:
				e = &model.GalleryAdd{EntryID: largeID(n + 1), File: pins[n%len(pins)], Title: fmt.Sprintf("Image %04d", n-20000), Actor: model.Actor{ID: "contributor"}, Tasks: []model.RecordRef{}, AddedAt: now.Add(time.Duration(n) * time.Second)}
			default:
				k := n - 21800
				e = &model.ClaimRevise{Target: model.RecordRef{Project: p.ID, RecordID: largeID(15001 + k%5000), Revision: model.Revision(1 + k/5000)}, Provenance: provenance, Replacement: claim}
			}
			raw, err := model.EncodeEvent(e)
			if err != nil {
				t.Fatal(err)
			}
			b.Events = append(b.Events, raw)
			carried = append(carried, packet)
		}
		review, err := model.EncodeEvent(&model.ReviewAdmit{Packets: b.Packets, Outcome: "accepted", Actor: b.Admitter, Reason: "synthetic review", Authors: map[model.ID]model.Actor{packet: {ID: "contributor"}}, CapturedAt: map[model.ID]model.Availability[time.Time]{packet: {State: model.Known, Value: &now}}, EventPackets: carried})
		if err != nil {
			t.Fatal(err)
		}
		b.Events = append(b.Events, review)
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		name, err := model.BundleName(b.Sequence, b.CommandID)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p.Ledger, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
		totalBytes += len(raw)
		previous = b.CommandID
	}
	t.Logf("fixture: 15000 tasks, 5000 other records, 1800 gallery items, 60000 events, 600 bundles, %d ledger bytes", totalBytes)
	return host, session, p
}

func TestLargeLedgerScreensAndFirstImages(t *testing.T) {
	if os.Getenv("WHOSAIDSO_PERF") != "1" {
		t.Skip("set WHOSAIDSO_PERF=1 for the full-size synthetic HTTP measurement")
	}
	host, session, p := largeLedger(t)
	start := time.Now()
	state, err := store.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if state.Snapshot().Watermark().Events != 60000 {
		t.Fatal("fixture event count")
	}
	t.Logf("cold ledger load: %s", time.Since(start))
	for _, screen := range []struct{ name, query string }{{"Overview", "home"}, {"Tasks", "records&kind=TASK"}, {"Records", "records"}, {"Activity", "history"}} {
		start = time.Now()
		code, _, body := ask(t, host, "GET", "/api/screen?project=team%2Fok&offset=0&screen="+screen.query, "", session)
		elapsed := time.Since(start)
		var page struct {
			Rows  []json.RawMessage
			Total int
		}
		if err := json.Unmarshal(body, &page); code != 200 || err != nil || len(page.Rows) != 24 || len(body) > 40000 {
			t.Fatalf("%s: %d %d bytes %v", screen.name, code, len(body), err)
		}
		t.Logf("%s warm: %s, %d rows, %d total, %d bytes", screen.name, elapsed, len(page.Rows), page.Total, len(body))
		if os.Getenv("WHOSAIDSO_PERF_CHECK") == "1" && elapsed >= time.Second {
			t.Errorf("%s exceeded one second: %s", screen.name, elapsed)
		}
	}
	// Start with no thumbnail files, as a new viewer's first gallery visit does.
	start = time.Now()
	code, _, body := ask(t, host, "GET", "/api/gallery?project=team%2Fok&offset=0", "", session)
	var page struct {
		Entries []struct {
			gallery.Shown
			Cap string
		}
	}
	if err := json.Unmarshal(body, &page); code != 200 || err != nil || len(page.Entries) != 24 {
		t.Fatalf("gallery %d: %v", code, err)
	}
	listTime := time.Since(start)
	var wg sync.WaitGroup
	var mu sync.Mutex
	bytesRead := 0
	slots := make(chan struct{}, 6)
	for _, e := range page.Entries[:12] {
		wg.Add(1)
		go func(id, cap string) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			code, _, b := ask(t, host, "GET", "/api/gallery/thumb?project=team%2Fok&id="+id+"&cap="+cap, "", session)
			if code != http.StatusOK {
				t.Errorf("thumbnail: %d %s", code, b)
				return
			}
			mu.Lock()
			bytesRead += len(b)
			mu.Unlock()
		}(e.ID, e.Cap)
	}
	wg.Wait()
	elapsed := time.Since(start)
	t.Logf("first 12 images, cold thumbnails: %s including list %s; %d thumbnail bytes", elapsed, listTime, bytesRead)
	if os.Getenv("WHOSAIDSO_PERF_CHECK") == "1" && elapsed >= time.Second {
		t.Errorf("first screen exceeded one second: %s", elapsed)
	}
}
