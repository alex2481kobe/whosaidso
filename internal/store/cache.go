package store

// The snapshot cache's files live here: where an image is kept, its framing
// and checksum, the version it must match, and atomic publication. Whether an
// image may be used for a ledger, and the ledger reads that decide it, are
// snapshot.go's.
//
// The cache is derived data and never truth. Losing it, damaging it or
// deleting it costs time and changes no answer, so every failure here means
// "no usable image" and nothing more.

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
)

const (
	cacheFile  = "snapshot"
	cacheMagic = "whosaidso-cache/1\n"
	// cacheTemp prefixes an image being written. A crash can leave one; the
	// next publisher removes those older than staleCacheTemp.
	cacheTemp      = ".snapshot-"
	staleCacheTemp = time.Hour
)

// CacheDir is the disposable snapshot cache: the sibling "cache" of the ledger
// inside its record folder (.whosaidso/events beside .whosaidso/cache). Like
// ArtifactDir it is derived from the configured ledger, never named
// separately. It is gitignored and holds nothing a clone needs.
func (p Project) CacheDir() string {
	return filepath.Join(filepath.Dir(p.Ledger), "cache")
}

// cacheImage is one published snapshot and the ledger prefix it was folded
// from: the prefix's length and the chained digest of every file name and
// content hash in it, never only its head.
type cacheImage struct {
	version string
	project model.ProjectID
	count   int
	chain   [32]byte
	state   []byte
}

// binaryStamp identifies the running executable by path, size and
// modification time. A snapshot is the output of one build's reducer rules;
// a rebuilt binary may fold the same ledger differently, so its images are not
// reused. Empty means the binary cannot be identified, and then no image is.
var binaryStamp = sync.OnceValue(func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	info, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", exe, info.Size(), info.ModTime().UnixNano())))
	return hex.EncodeToString(sum[:8])
})

// cacheVersion is what an image must carry to be read by this process.
func cacheVersion() string {
	stamp := binaryStamp()
	if stamp == "" {
		return ""
	}
	return reduce.SnapshotVersion() + "|" + stamp
}

func encodeImage(im cacheImage) []byte {
	var body []byte
	field := func(b []byte) {
		body = binary.AppendUvarint(body, uint64(len(b)))
		body = append(body, b...)
	}
	field([]byte(im.version))
	field([]byte(im.project))
	body = binary.AppendUvarint(body, uint64(im.count))
	body = append(body, im.chain[:]...)
	body = append(body, im.state...)
	sum := sha256.Sum256(body)
	out := make([]byte, 0, len(cacheMagic)+len(sum)+len(body))
	out = append(append(append(out, cacheMagic...), sum[:]...), body...)
	return out
}

var errImage = errors.New("unusable cache image")

// decodeImage checks the frame and checksum before believing any field. The
// checksum catches accidents (a torn write, a flipped bit), not an adversary
// who can rewrite the cache; the cache is local derived state, never imported.
func decodeImage(data []byte) (cacheImage, error) {
	head := len(cacheMagic) + sha256.Size
	if len(data) < head || string(data[:len(cacheMagic)]) != cacheMagic {
		return cacheImage{}, errImage
	}
	body := data[head:]
	if sum := sha256.Sum256(body); !bytes.Equal(sum[:], data[len(cacheMagic):head]) {
		return cacheImage{}, errImage
	}
	var im cacheImage
	field := func() ([]byte, bool) {
		n, size := binary.Uvarint(body)
		if size <= 0 || n > uint64(len(body)-size) {
			return nil, false
		}
		out := body[size : size+int(n)]
		body = body[size+int(n):]
		return out, true
	}
	version, ok := field()
	if !ok {
		return cacheImage{}, errImage
	}
	project, ok := field()
	if !ok {
		return cacheImage{}, errImage
	}
	count, size := binary.Uvarint(body)
	if size <= 0 || count > 1<<40 || len(body)-size < len(im.chain) {
		return cacheImage{}, errImage
	}
	body = body[size:]
	im.version, im.project, im.count = string(version), model.ProjectID(project), int(count)
	copy(im.chain[:], body)
	im.state = body[len(im.chain):]
	return im, nil
}

// readImage returns the published image if it is intact and written for this
// project by this build. The cache folder must be a real directory and the
// image a regular file: a symlink could point a read, or a later write, out of
// the whosaidso root.
func readImage(p Project, version string) (cacheImage, bool) {
	if version == "" {
		return cacheImage{}, false
	}
	dir := p.CacheDir()
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return cacheImage{}, false
	}
	path := filepath.Join(dir, cacheFile)
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return cacheImage{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cacheImage{}, false
	}
	im, err := decodeImage(data)
	if err != nil || im.version != version || im.project != p.ID {
		return cacheImage{}, false
	}
	return im, true
}

// publishImage is the cache's one write, replaceable by tests that inject a
// failure or a crash. Nothing may depend on it succeeding.
var publishImage = writeImage

// writeImage publishes one complete image: a temporary written in full, then
// one rename. A concurrent reader sees the old image or the new one, never a
// mixture, and two publishers each rename a complete image; whichever lands
// last may be the older, which costs the next reader catch-up, not an answer.
// There is no fsync: after a power loss a torn image fails its checksum and is
// rebuilt, and the cache holds nothing that must survive.
func writeImage(p Project, im cacheImage) error {
	if info, err := os.Lstat(p.Ledger); err != nil || !info.IsDir() {
		// No ledger directory yet: nothing admitted, nothing worth caching.
		return errImage
	}
	dir := p.CacheDir()
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		if err := os.Mkdir(dir, 0o755); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if !info.IsDir() {
		return fmt.Errorf("cache folder %s is not a directory", dir)
	}
	removeStaleTemps(dir)
	f, err := os.CreateTemp(dir, cacheTemp+"*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	_, err = f.Write(encodeImage(im))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, cacheFile))
}

// removeStaleTemps clears images a crashed publisher left. A young temporary
// may belong to a publisher still writing, so only old ones go.
func removeStaleTemps(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasPrefix(e.Name(), cacheTemp) || !strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > staleCacheTemp {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
