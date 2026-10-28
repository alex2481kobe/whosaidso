// Viewer observations reuse hashes only while the opened file's identity and
// metadata agree. Admission and original serving always verify independently.
package gallery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

const PageHashBytes int64 = 128 << 20
const observationCacheEntries = 256

type fileObservation struct {
	info os.FileInfo
	pin  model.GalleryFile
}

// Observer is local to one viewer. Its zero value is ready for concurrent use.
type Observer struct {
	mu    sync.Mutex
	files map[string]fileObservation
	hash  func(io.Reader, string) (model.GalleryFile, error)
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(b)
}

func (o *Observer) observe(ctx context.Context, p store.Project, record reduce.GalleryEntry, budget *int64) Shown {
	entry := localEntry(p, record)
	kind, _ := Kind(entry.Locator)
	shown := Shown{Entry: entry, Kind: kind, Status: "unknown"}
	fail := func(err error) Shown {
		shown.StatusReason = textPreview(err.Error(), 240)
		if errors.Is(err, os.ErrNotExist) {
			shown.Status = "missing"
		}
		return shown
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	f, err := Open(p.Root, entry.Locator)
	if err != nil {
		return fail(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	key := p.Root + "\x00" + entry.Locator
	o.mu.Lock()
	cached, ok := o.files[key]
	o.mu.Unlock()
	pin := cached.pin
	if !ok || !os.SameFile(info, cached.info) || info.Size() != cached.info.Size() || !info.ModTime().Equal(cached.info.ModTime()) {
		if info.Size() > *budget {
			return fail(fmt.Errorf("file exceeds this page's hash budget; open the original to verify"))
		}
		hash := o.hash
		if hash == nil {
			hash = Hash
		}
		pin, err = hash(contextReader{ctx, io.LimitReader(f, *budget+1)}, entry.Locator)
		*budget -= int64(pin.Length)
		if err != nil {
			return fail(err)
		}
		if *budget < 0 {
			return fail(fmt.Errorf("file grew beyond this page's hash budget"))
		}
		after, err := f.Stat()
		if err != nil {
			return fail(err)
		}
		if after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
			return fail(fmt.Errorf("file changed during observation"))
		}
		o.mu.Lock()
		if o.files == nil || len(o.files) >= observationCacheEntries {
			o.files = map[string]fileObservation{}
		}
		o.files[key] = fileObservation{info, pin}
		o.mu.Unlock()
	}
	shown.Status = "changed"
	if pin.SameContent(record.Add.File) {
		shown.Status = "present"
	}
	return shown
}
