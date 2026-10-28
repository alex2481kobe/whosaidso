// Thumbnail decoding has explicit byte and pixel budgets. Derived cache files
// are optional and must never become a second source of truth.
package gallery

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

type forbiddenThumbnailRead struct{}

func (forbiddenThumbnailRead) Read([]byte) (int, error) {
	panic("oversized source must be refused before read")
}

func TestThumbnailBudgetsPrecedeSourceReadsAndRasterAllocation(t *testing.T) {
	p := pageProject(t, 0)
	if _, err := Thumbnail(p, forbiddenThumbnailRead{}, model.GalleryFile{Length: thumbnailSourceBytes + 1}); err == nil {
		t.Fatal("byte budget was ignored")
	}
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	data := b.Bytes()
	binary.BigEndian.PutUint32(data[16:20], 100000)
	binary.BigEndian.PutUint32(data[20:24], 100000)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width != 100000 {
		t.Fatalf("oversized header control: %v %v", cfg, err)
	}
	_, err = Thumbnail(p, bytes.NewReader(data), model.GalleryFile{SHA256: model.HashBytes(data), Length: uint64(len(data))})
	if err == nil || err.Error() != "image exceeds thumbnail pixel budget; open the original" {
		t.Fatalf("raster budget: %v", err)
	}
}

func TestThumbnailCacheIsOptionalAndNoCacheDoesNotPublish(t *testing.T) {
	p := pageProject(t, 0)
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	pin := model.GalleryFile{SHA256: model.HashBytes(b.Bytes()), Length: uint64(b.Len())}
	// A read-only or disabled cache cannot prevent previewing valid source bytes.
	t.Setenv(store.NoCacheEnv, "1")
	thumb, err := Thumbnail(p, bytes.NewReader(b.Bytes()), pin)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(thumb))
	if err != nil || cfg.Width != 2 || cfg.Height != 3 {
		t.Fatalf("no-cache preview: %v %v", cfg, err)
	}
	names, err := filepath.Glob(filepath.Join(p.CacheDir(), "thumb-*"))
	if err != nil || len(names) != 0 {
		t.Fatalf("disabled cache published %v %v", names, err)
	}
	t.Setenv(store.NoCacheEnv, "")
	if err := os.MkdirAll(p.CacheDir(), 0700); err != nil {
		t.Fatal(err)
	}
	// A filename occupied by a directory is a cache failure, never an image failure.
	name := filepath.Join(p.CacheDir(), "thumb-v1-"+string(pin.SHA256)+".png")
	if err := os.Mkdir(name, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Thumbnail(p, io.LimitReader(bytes.NewReader(b.Bytes()), int64(b.Len())), pin); err != nil {
		t.Fatal(err)
	}
}

func TestThumbnailWaitsForAFreeDecodeSlot(t *testing.T) {
	p := pageProject(t, 0)
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	pin := model.GalleryFile{SHA256: model.HashBytes(b.Bytes()), Length: uint64(b.Len())}
	t.Setenv(store.NoCacheEnv, "1")
	for range cap(thumbnailSlots) {
		thumbnailSlots <- struct{}{}
	}
	done := make(chan error, 1)
	go func() {
		_, err := Thumbnail(p, bytes.NewReader(b.Bytes()), pin)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("decoded with every slot taken: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	<-thumbnailSlots
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for range cap(thumbnailSlots) - 1 {
		<-thumbnailSlots
	}
}

func TestThumbnailCancellationDoesNotWaitForADecodeSlot(t *testing.T) {
	p := pageProject(t, 0)
	for range cap(thumbnailSlots) {
		thumbnailSlots <- struct{}{}
	}
	defer func() {
		for range cap(thumbnailSlots) {
			<-thumbnailSlots
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { _, err := ThumbnailContext(ctx, p, forbiddenThumbnailRead{}, model.GalleryFile{}); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled decode: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled thumbnail remained queued")
	}
}
