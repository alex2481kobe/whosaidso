package ui

// The Gallery screen's endpoints live here: the list, one entry's file, and
// the two POSTs, Reveal in Finder and Remove from gallery. A file is reached
// only through its entry id; no request names a path, and a request that
// tries is refused before anything is opened. Which files preview, and as
// what type, is internal/gallery's table. Remove captures a retraction packet;
// admission, through the CLI, publishes it. The media file is never changed.

import (
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"

	"github.com/alex2481kobe/whosaidso/internal/gallery"
	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

// maxText is how much of a text file the viewer shows: enough to debug by,
// light enough to load a screen of them.
const maxText = 128 << 10

// RevealInFinder shows path selected in a Finder window. It runs
// /usr/bin/open itself, never a shell, and works on macOS only.
func RevealInFinder(path string) error {
	if runtime.GOOS != "darwin" {
		return errText("Reveal in Finder works on macOS only")
	}
	return exec.Command("/usr/bin/open", "-R", path).Run()
}

func (h *handler) gallery(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for key, values := range q {
		if (key != "project" && key != "id" && key != "cap") || len(values) != 1 {
			h.fail(w, http.StatusBadRequest, errText("the gallery takes only project, id and a file's cap; files are reached by entry id, never by path"))
			return
		}
	}
	project, ok, err := h.find(model.ProjectID(q.Get("project")))
	if err == nil && ok {
		h.galleryAnswer(w, r, project, q.Get("id"))
		return
	}
	if err != nil {
		h.fail(w, http.StatusInternalServerError, err)
		return
	}
	h.fail(w, http.StatusNotFound, errText("no available project "+q.Get("project")+" on this machine"))
}

func (h *handler) galleryAnswer(w http.ResponseWriter, r *http.Request, file store.Project, id string) {
	if r.URL.Path == "/api/gallery" {
		shown, err := gallery.List(file)
		if err != nil {
			h.fail(w, http.StatusInternalServerError, err)
			return
		}
		// Each entry carries its file capability, which the page puts in the
		// file's URL (keyed); the CLI's list has none.
		type withCap struct {
			gallery.Shown
			Cap string `json:"cap"`
		}
		entries := make([]withCap, len(shown))
		for i, e := range shown {
			entries[i] = withCap{e, h.capability(r.URL.Query().Get("project"), e.ID)}
		}
		h.json(w, map[string]any{"entries": entries})
		return
	}
	entry, err := gallery.Find(file, id)
	if errors.Is(err, gallery.ErrNoEntry) {
		h.fail(w, http.StatusNotFound, errText("no gallery entry "+id))
		return
	}
	if err != nil {
		h.fail(w, http.StatusInternalServerError, err)
		return
	}
	switch r.URL.Path {
	case "/api/gallery/file":
		h.serveEntry(w, r, entry)
	case "/api/gallery/remove":
		ref, err := gallery.Capture(r.Context(), file, h.cfg.CaptureActor, &model.GalleryRetract{EntryID: model.ID(id)})
		if err != nil {
			h.fail(w, http.StatusInternalServerError, err)
			return
		}
		h.json(w, map[string]string{"captured": string(ref.CommandID)})
	case "/api/gallery/reveal":
		if info, err := os.Stat(entry.Path); err != nil || !info.Mode().IsRegular() {
			h.fail(w, http.StatusNotFound, errText("missing: "+entry.Path))
			return
		}
		if h.cfg.Reveal == nil {
			h.fail(w, http.StatusNotImplemented, errText("this viewer cannot reveal files"))
			return
		}
		if err := h.cfg.Reveal(entry.Path); err != nil {
			h.fail(w, http.StatusNotImplemented, err)
			return
		}
		h.json(w, map[string]string{"revealed": id})
	}
}

// serveEntry streams an entry's file with its kind's media type: images and
// videos whole and seekable (ranges answered by http.ServeContent, never read
// into memory), text up to maxText. A file the viewer does not preview is
// not served at all.
func (h *handler) serveEntry(w http.ResponseWriter, r *http.Request, entry gallery.Entry) {
	kind, mediaType := gallery.Kind(entry.Path)
	if kind == gallery.None {
		h.fail(w, http.StatusUnsupportedMediaType, errText("no preview for this file type"))
		return
	}
	// Non-blocking, so a path swapped for a FIFO or device answers at once
	// instead of holding the handler until something writes to it; the
	// fstat below then refuses anything but a regular file, whose reads the
	// flag does not change.
	f, err := os.OpenFile(entry.Path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		h.fail(w, http.StatusNotFound, errText("missing: "+entry.Path))
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		h.fail(w, http.StatusNotFound, errText("missing: "+entry.Path))
		return
	}
	pin, err := gallery.Hash(f, entry.Path)
	if err != nil || !pin.SameContent(model.GalleryFile{SHA256: entry.SHA256, Length: entry.Length}) {
		h.fail(w, http.StatusConflict, errText("changed: "+entry.Path))
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		h.fail(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", mediaType)
	if kind != gallery.Text {
		http.ServeContent(w, r, "", info.ModTime(), f)
		return
	}
	if info.Size() > maxText {
		w.Header().Set("X-WhoSaidSo-Truncated", strconv.Itoa(maxText))
	}
	_, _ = io.Copy(w, io.LimitReader(f, maxText))
}
