package store

// A blob is published under its digest. When that name is taken, only a
// regular file of the same length is the same bytes captured earlier in the
// request; anything else there is refused, never acknowledged. (POSIX rename
// replaces a file at the name with the real bytes, which is also correct.)

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestCaptureRefusesAnythingElseAtTheDigestName(t *testing.T) {
	plants := map[string]func(t *testing.T, path string){
		"directory":   func(t *testing.T, path string) { must(t, os.Mkdir(path, 0o700)) },
		"other bytes": func(t *testing.T, path string) { must(t, os.WriteFile(path, []byte("longer than source"), 0o600)) },
	}
	for name, plant := range plants {
		t.Run(name, func(t *testing.T) {
			p := intakeProject(t)
			disk := systemIntakeIO()
			planted := ""
			// The blob's flush comes just before the rename to its digest name.
			disk.sync = func(f *os.File) error {
				if planted == "" && strings.HasPrefix(filepath.Base(f.Name()), "blob-") {
					planted = filepath.Join(filepath.Dir(f.Name()), string(model.HashBytes([]byte("source"))))
					plant(t, planted)
				}
				return f.Sync()
			}
			_, err := writeIntake(context.Background(), p, requestFor(commandID(1), "source"), disk)
			if planted == "" {
				t.Fatal("the blob was never flushed")
			}
			packets, readErr := ReadIntake(p, nil)
			if err == nil && name != "directory" {
				if readErr != nil || len(packets) != 1 {
					t.Fatalf("an acknowledged capture must read back: %+v, %v", packets, readErr)
				}
				return
			}
			requireFault(t, err, "io")
			if readErr != nil || len(packets) != 0 {
				t.Fatalf("a refused capture published %+v, %v", packets, readErr)
			}
		})
	}
}
