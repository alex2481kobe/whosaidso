package ui

// The Gallery screen's endpoints live here: the list, one entry's file, and
// the two POSTs, Reveal in Finder and Remove from gallery. A file is reached
// only through its entry id; no request names a path, and a request that
// tries is refused before anything is opened. Which files preview, and as
// what type, is internal/gallery's table. Delete captures and admits a retraction through the normal admission gate,
// attributed to the configured viewer actor. The media file is never changed.

import (
	"crypto/rand"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/gallery"
	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
	"github.com/alex2481kobe/whosaidso/internal/write"
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
	q, parseErr := url.ParseQuery(r.URL.RawQuery)
	if parseErr != nil {
		h.fail(w, http.StatusBadRequest, errText("invalid gallery query: "+parseErr.Error()))
		return
	}
	for key, values := range q {
		pagedKey := r.URL.Path == "/api/gallery" && q.Has("offset") && (key == "offset" || key == "search" || key == "kind" || key == "sort")
		if (!pagedKey && key != "project" && key != "id" && key != "cap") || len(values) != 1 {
			h.fail(w, http.StatusBadRequest, errText("gallery parameters name a project, entry or page filters; files are reached by entry id, never by path"))
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
		h.galleryList(w, r, file)
		return
	}

	source, err := h.ledger.Load(file)
	if err != nil {
		h.fail(w, http.StatusInternalServerError, err)
		return
	}
	entry, err := gallery.FindEntry(file, source.Snapshot().Gallery(), id)
	if errors.Is(err, gallery.ErrNoEntry) {
		h.fail(w, http.StatusNotFound, errText("no gallery entry "+id))
		return
	}
	if err != nil {
		h.fail(w, http.StatusInternalServerError, err)
		return
	}
	switch r.URL.Path {
	case "/api/gallery/thumb":
		h.serveThumbnail(w, r, file, entry)
	case "/api/gallery/file", "/api/gallery/download":
		h.serveEntry(w, r, file.Root, entry)
	case "/api/gallery/remove":
		ref, err := gallery.Capture(r.Context(), file, h.cfg.CaptureActor, &model.GalleryRetract{EntryID: model.ID(id)})
		if err != nil {
			h.fail(w, http.StatusInternalServerError, err)
			return
		}
		command, err := model.NewID(time.Now(), rand.Reader)
		if err == nil {
			_, err = write.Admit(r.Context(), file, write.AdmitRequest{CommandID: command, PacketIDs: []model.ID{ref.CommandID}, Admitter: h.cfg.CaptureActor, Outcome: "accepted", Reason: "Viewer requested removal from gallery; media file retained"})
		}
		if err != nil {
			h.fail(w, http.StatusConflict, errText("Retraction captured as "+string(ref.CommandID)+" but not admitted: "+err.Error()))
			return
		}
		h.json(w, map[string]string{"captured": string(ref.CommandID), "removed": id})
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
func (h *handler) serveEntry(w http.ResponseWriter, r *http.Request, checkout string, entry gallery.Entry) {
	kind, mediaType := gallery.Kind(entry.Locator)
	if kind == gallery.None && r.URL.Path != "/api/gallery/download" {
		h.fail(w, http.StatusUnsupportedMediaType, errText("no preview for this file type"))
		return
	}
	f, err := gallery.Open(checkout, entry.Locator)
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
	if r.URL.Path == "/api/gallery/download" {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(entry.Path)}))
		http.ServeContent(w, r, "", info.ModTime(), f)
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

func (h *handler) serveThumbnail(w http.ResponseWriter, r *http.Request, p store.Project, entry gallery.Entry) {
	f, err := gallery.Open(p.Root, entry.Locator)
	if err != nil {
		h.fail(w, http.StatusNotFound, err)
		return
	}
	defer f.Close()
	b, err := gallery.ThumbnailContext(r.Context(), p, f, model.GalleryFile{SHA256: entry.SHA256, Length: entry.Length})
	if err != nil {
		h.fail(w, http.StatusConflict, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	// Revalidate ledger membership and the source before a conditional hit.
	// Private caches may retain bytes, but can never hide a changed source.
	w.Header().Set("Cache-Control", "private, no-cache")
	tag := `"` + string(model.HashBytes(b)) + `"`
	w.Header().Set("ETag", tag)
	if r.Header.Get("If-None-Match") == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(b)
}
