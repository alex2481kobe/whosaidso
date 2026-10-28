// Thumbnail requests must stay small, verify source bytes on cache hits, and
// preserve the authenticated original. Fixtures contain no repository data.
package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/gallery"
)

func TestThumbnailsBoundRastersAndRecheckCachedSources(t *testing.T) {
	for _, codec := range []string{"png", "jpg"} {
		t.Run(codec, func(t *testing.T) {
			host, session, root := galleryUI(t, &stubWorld{})
			img := image.NewNRGBA(image.Rect(0, 0, 1200, 600))
			for y := 0; y < 600; y++ {
				for x := 0; x < 1200; x++ {
					img.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), uint8(x * y), 255})
				}
			}
			var data bytes.Buffer
			var err error
			if codec == "png" {
				err = png.Encode(&data, img)
			} else {
				err = jpeg.Encode(&data, img, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			id, path := shown(t, root, "shot."+codec, data.Bytes())
			original := fileURL(t, host, session, id)
			thumb := strings.Replace(original, "/file?", "/thumb?", 1)
			code, headers, body := ask(t, host, "GET", thumb, "", session)
			cfg, format, err := image.DecodeConfig(bytes.NewReader(body))
			if code != 200 || headers.Get("Content-Type") != "image/png" || err != nil || format != "png" || cfg.Width != gallery.ThumbnailEdge || cfg.Height != gallery.ThumbnailEdge/2 || len(body) >= data.Len() {
				t.Fatalf("thumbnail: code %d %v %s, bytes %d/%d", code, cfg, body[:min(40, len(body))], len(body), data.Len())
			}
			p := galleryProject(t, root)
			names, err := filepath.Glob(filepath.Join(p.CacheDir(), "thumb-v1-*.png"))
			if err != nil || len(names) != 1 {
				t.Fatalf("cache: %v %v", names, err)
			}
			stat, err := os.Stat(names[0])
			if err != nil {
				t.Fatal(err)
			}
			code, _, again := ask(t, host, "GET", thumb, "", session)
			stat2, err := os.Stat(names[0])
			if err != nil {
				t.Fatal(err)
			}
			if code != 200 || !bytes.Equal(body, again) || !stat.ModTime().Equal(stat2.ModTime()) {
				t.Fatal("cache hit regenerated or changed the thumbnail")
			}
			cached, err := os.ReadFile(names[0])
			if err != nil {
				t.Fatal(err)
			}
			cached[len(cached)-1] ^= 1
			if err := os.WriteFile(names[0], cached, 0600); err != nil {
				t.Fatal(err)
			}
			code, _, again = ask(t, host, "GET", thumb, "", session)
			if code != 200 || !bytes.Equal(body, again) {
				t.Fatal("damaged derived cache must regenerate")
			}
			code, _, again = ask(t, host, "GET", original, "", session)
			if code != 200 || !bytes.Equal(again, data.Bytes()) {
				t.Fatal("opening original must retain exact source bytes")
			}
			changed := append([]byte(nil), data.Bytes()...)
			changed[len(changed)-1] ^= 1
			if err := os.WriteFile(path, changed, 0600); err != nil {
				t.Fatal(err)
			}
			code, _, _ = ask(t, host, "GET", thumb, "", session)
			if code != http.StatusConflict {
				t.Fatalf("cached thumbnail hid changed source: %d", code)
			}
		})
	}
}

func TestThumbnailCapabilitiesAndDecodeBudgets(t *testing.T) {
	host, session, root := galleryUI(t, &stubWorld{})
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 10, 20))); err != nil {
		t.Fatal(err)
	}
	id, path := shown(t, root, "tiny.png", data.Bytes())
	url := strings.Replace(fileURL(t, host, session, id), "/file?", "/thumb?", 1)
	code, _, body := ask(t, host, "GET", url, "", session)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
	if code != 200 || err != nil || cfg.Width != 10 || cfg.Height != 20 {
		t.Fatalf("small image was enlarged: %d %v %v", code, cfg, err)
	}
	cookieOnly := sess{cookie: session.cookie}
	bad := strings.Split(url, "&cap=")[0]
	if code, _, _ := ask(t, host, "GET", bad, "", cookieOnly); code != 403 {
		t.Fatalf("cookie alone opened thumbnail: %d", code)
	}
	if code, _, _ := ask(t, host, "GET", url, "", sess{}); code != 403 {
		t.Fatalf("thumbnail capability bypassed session: %d", code)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := ask(t, host, "GET", url, "", session); code != 404 {
		t.Fatalf("cache hid missing source: %d", code)
	}
	invalid, _ := shown(t, root, "invalid.png", []byte("not an image"))
	bad = strings.Replace(fileURL(t, host, session, invalid), "/file?", "/thumb?", 1)
	if code, _, _ := ask(t, host, "GET", bad, "", session); code == 200 {
		t.Fatal("undecodable image became a successful thumbnail")
	}
}

func TestSaveOriginalUsesAnAuthenticatedAttachmentOnEveryPlatform(t *testing.T) {
	host, session, root := galleryUI(t, &stubWorld{})
	id, _ := shown(t, root, "screen.png", []byte("pinned original"))
	url := strings.Replace(fileURL(t, host, session, id), "/file?", "/download?", 1)
	code, headers, body := ask(t, host, "GET", url, "", session)
	if code != 200 || string(body) != "pinned original" || headers.Get("Content-Disposition") != "attachment; filename=screen.png" {
		t.Fatalf("save original: %d %v %s", code, headers, body)
	}
	if code, _, _ := ask(t, host, "GET", strings.Split(url, "&cap=")[0], "", sess{cookie: session.cookie}); code != 403 {
		t.Fatalf("unauthenticated download: %d", code)
	}
}
