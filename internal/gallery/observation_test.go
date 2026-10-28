// Polling must avoid rehashing stable files, stop at cancellation, and never
// treat a byte budget refusal as a successful content comparison.
package gallery

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestPageObservationsReuseOnlyTheSameUnchangedFile(t *testing.T) {
	p := pageProject(t, 1)
	calls := 0
	observer := &Observer{hash: func(r io.Reader, path string) (model.GalleryFile, error) { calls++; return Hash(r, path) }}
	read := func(status string) {
		t.Helper()
		page, err := ReadPageContext(context.Background(), p, PageRequest{}, observer)
		if err != nil || len(page.Entries) != 1 || page.Entries[0].Status != status {
			t.Fatalf("page: %+v %v", page, err)
		}
	}
	read("present")
	read("present")
	if calls != 1 {
		t.Fatalf("stable poll read file again: %d hashes", calls)
	}
	path := filepath.Join(p.Root, "screen-00.png")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	changed := info.ModTime().Add(time.Second)
	if err := os.Chtimes(path, changed, changed); err != nil {
		t.Fatal(err)
	}
	read("changed")
	if calls != 2 {
		t.Fatalf("mtime change did not invalidate: %d", calls)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, changed, changed); err != nil {
		t.Fatal(err)
	}
	read("present")
	if calls != 3 {
		t.Fatalf("replacement inode hid behind size and mtime: %d", calls)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	read("missing")
}

func TestPageCancellationStopsAnActiveHashAndFurtherObservations(t *testing.T) {
	p := pageProject(t, 3)
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	observer := &Observer{hash: func(r io.Reader, path string) (model.GalleryFile, error) {
		calls++
		cancel()
		pin, err := Hash(r, path)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("active hash ignored cancellation: %v", err)
		}
		return pin, err
	}}
	_, err := ReadPageContext(ctx, p, PageRequest{}, observer)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancelled page: %v, hashes %d", err, calls)
	}
	_, err = ReadPageContext(ctx, p, PageRequest{}, observer)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("already cancelled page: %v hashes %d", err, calls)
	}
}

func TestPageHashBudgetReportsUnknownWithoutReadingAnOversizedVideo(t *testing.T) {
	p := pageProject(t, 2)
	path := filepath.Join(p.Root, "screen-01.mp4")
	if err := os.Truncate(path, PageHashBytes+1); err != nil {
		t.Fatal(err)
	}
	observer := &Observer{hash: func(r io.Reader, path string) (model.GalleryFile, error) {
		if filepath.Ext(path) == ".mp4" {
			t.Fatal("oversized file was read")
		}
		return Hash(r, path)
	}}
	page, err := ReadPageContext(context.Background(), p, PageRequest{}, observer)
	if err != nil || page.Entries[0].Status != "unknown" || page.Entries[0].StatusReason == "" || page.Entries[1].Status != "present" {
		t.Fatalf("byte bound: %+v %v", page, err)
	}
}
