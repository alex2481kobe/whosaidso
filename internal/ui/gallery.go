package ui

// The Gallery screen's endpoints live here: the list, one entry's file, and
// the two POSTs, Reveal in Finder and Remove from gallery. A file is reached
// only through its entry id; no request names a path, and a request that
// tries is refused before anything is opened. Which files preview, and as
// what type, is internal/gallery's table. Nothing here touches a ledger;
// Remove changes the gallery list and never the file.

import (
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"

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
		if (key != "project" && key != "id") || len(values) != 1 {
			h.fail(w, http.StatusBadRequest, errText("the gallery takes only project and id; files are reached by entry id, never by path"))
			return
		}
	}
	project, ok, err := h.find(model.ProjectID(q.Get("project")))
	if err == nil && ok {
		var file string
		if file, err = store.GalleryFile(project.ID); err == nil {
			h.galleryAnswer(w, r, file, q.Get("id"))
			return
		}
	}
	if err != nil {
		h.fail(w, http.StatusInternalServerError, err)
		return
	}
	h.fail(w, http.StatusNotFound, errText("no available project "+q.Get("project")+" on this machine"))
}

func (h *handler) galleryAnswer(w http.ResponseWriter, r *http.Request, file, id string) {
	if r.URL.Path == "/api/gallery" {
		shown, err := gallery.List(file)
		if err != nil {
			h.fail(w, http.StatusInternalServerError, err)
			return
		}
		h.json(w, map[string]any{"entries": shown})
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
		if _, err := gallery.Remove(file, id); err != nil {
			h.fail(w, http.StatusInternalServerError, err)
			return
		}
		h.json(w, map[string]string{"removed": id})
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
	f, err := os.Open(entry.Path)
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
