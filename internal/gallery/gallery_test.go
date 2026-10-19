package gallery

// Pinning tests distinguish bytes from paths and refuse non-files or long words.
// Admission, retraction and import behavior are exercised by the CLI tests.

import (
	"github.com/alex2481kobe/whosaidso/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreparePinsActualBytesAndChecksWords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := Prepare(dir, path, " video ", "note", model.Actor{ID: "contributor"}, nil, time.Now())
	if err != nil || e.File.SHA256 != model.HashBytes([]byte("video")) || e.File.Length != 5 || e.File.Path != "clip.mp4" || e.Title != "video" {
		t.Fatalf("control: %+v %v", e, err)
	}
	for _, c := range []struct{ path, title, note string }{
		{"missing", "video", ""}, {dir, "video", ""}, {path, " ", ""}, {path, "a\nb", ""}, {path, strings.Repeat("t", 121), ""}, {path, "t", strings.Repeat("n", 1001)},
	} {
		if _, err := Prepare(dir, c.path, c.title, c.note, model.Actor{ID: "contributor"}, nil, time.Now()); err == nil {
			t.Errorf("accepted invalid input: %+v", c)
		}
	}
	if err := os.WriteFile(path, []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	pin, err := Pin(path)
	if err != nil || pin.SHA256 == e.File.SHA256 {
		t.Fatalf("same-length replacement not detected: %+v %v", pin, err)
	}
}
