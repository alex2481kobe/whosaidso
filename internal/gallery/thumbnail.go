// Bounded raster previews and their disposable content cache belong here.
// Ledger pins and HTTP responses remain the caller's responsibility.
package gallery

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

const ThumbnailEdge = 384
const thumbnailPixels = 32_000_000
const thumbnailSourceBytes = 64 << 20
const thumbnailCacheBytes = 1 << 20

// thumbnailSlots bounds concurrent decodes, so a page of large images holds at
// most this many source buffers and rasters in memory at once.
var thumbnailSlots = make(chan struct{}, 2)

// Thumbnail verifies the exact bytes it decodes, including on a cache hit.
// Unsupported codecs and oversized rasters return errors, never originals.
func Thumbnail(p store.Project, f io.Reader, pin model.GalleryFile) ([]byte, error) {
	if pin.Length > thumbnailSourceBytes {
		return nil, fmt.Errorf("image exceeds thumbnail byte budget; open the original")
	}
	thumbnailSlots <- struct{}{}
	defer func() { <-thumbnailSlots }()
	data, err := io.ReadAll(io.LimitReader(f, thumbnailSourceBytes+1))
	if err != nil {
		return nil, err
	}
	if uint64(len(data)) != pin.Length || model.HashBytes(data) != pin.SHA256 {
		return nil, fmt.Errorf("image changed since it was recorded")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("thumbnail decoder: %w; open the original", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > thumbnailPixels/cfg.Height {
		return nil, fmt.Errorf("image exceeds thumbnail pixel budget; open the original")
	}
	name := fmt.Sprintf("thumb-v1-%s.png", pin.SHA256)
	root := thumbnailCache(p)
	if root != nil {
		defer root.Close()
		if b := readThumbnail(root, name); b != nil {
			return b, nil
		}
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	width, height := cfg.Width, cfg.Height
	if width > ThumbnailEdge || height > ThumbnailEdge {
		if width >= height {
			height = max(1, height*ThumbnailEdge/width)
			width = ThumbnailEdge
		} else {
			width = max(1, width*ThumbnailEdge/height)
			height = ThumbnailEdge
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, width, height))
	bounds := src.Bounds()
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			dst.Set(x, y, src.At(bounds.Min.X+x*cfg.Width/width, bounds.Min.Y+y*cfg.Height/height))
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, err
	}
	result := out.Bytes()
	if root != nil {
		writeThumbnail(root, name, result)
	}
	return result, nil
}

func thumbnailCache(p store.Project) *os.Root {
	if os.Getenv(store.NoCacheEnv) == "1" {
		return nil
	}
	// The cache is disposable. A symlink or inaccessible folder disables it;
	// failures must never turn a readable source into an unreadable preview.
	if err := os.Mkdir(p.CacheDir(), 0700); err != nil && !os.IsExist(err) {
		return nil
	}
	info, err := os.Lstat(p.CacheDir())
	if err != nil || !info.IsDir() {
		return nil
	}
	root, err := os.OpenRoot(p.CacheDir())
	if err != nil {
		return nil
	}
	return root
}

func readThumbnail(root *os.Root, name string) []byte {
	f, err := openMedia(root, name)
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > thumbnailCacheBytes {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(f, thumbnailCacheBytes+1))
	if err != nil || len(b) < sha256.Size || len(b) > thumbnailCacheBytes {
		return nil
	}
	sum := sha256.Sum256(b[sha256.Size:])
	if !bytes.Equal(sum[:], b[:sha256.Size]) {
		return nil
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b[sha256.Size:]))
	if err != nil || format != "png" || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > ThumbnailEdge || cfg.Height > ThumbnailEdge {
		return nil
	}
	return b[sha256.Size:]
}

func writeThumbnail(root *os.Root, name string, b []byte) {
	// Unique temporary files permit concurrent requests and atomic replacement.
	tmp := ".thumb-" + rand.Text() + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return
	}
	defer root.Remove(tmp)
	sum := sha256.Sum256(b)
	_, err = f.Write(append(sum[:], b...))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		_ = root.Rename(tmp, name)
	}
}
