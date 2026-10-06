//go:build darwin || linux

package ui

// A gallery path swapped for a FIFO is refused at once: opening it must not
// wait for a writer, and only a regular file is served.

import (
	"net/http"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestGalleryRefusesAFIFOWithoutWaiting(t *testing.T) {
	host, session, file := galleryUI(t, &stubWorld{})
	id, path := shown(t, file, "run.log", []byte("log"))
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	// A handler stuck in open would also stall the server's shutdown; a
	// writer opened here at the end releases it, so a regression fails
	// below rather than hanging the suite.
	t.Cleanup(func() {
		if w, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			w.Close()
		}
	})
	start := time.Now()
	got, _, _ := ask(t, host, "GET", fileURL(t, host, session, id), "", session)
	if got != http.StatusNotFound || time.Since(start) > time.Second {
		t.Fatalf("a FIFO: status %d after %v; want 404 at once", got, time.Since(start))
	}
}
