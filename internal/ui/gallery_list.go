// Gallery list transport translates page parameters and adds file capabilities.
// Filtering, ordering, page bounds, and availability belong to internal/gallery.
package ui

import (
	"net/http"
	"strconv"

	"github.com/alex2481kobe/whosaidso/internal/gallery"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func (h *handler) galleryList(w http.ResponseWriter, r *http.Request, p store.Project) {
	q := r.URL.Query()
	var page gallery.Page
	var shown []gallery.Shown
	var err error
	paged := q.Has("offset")
	if paged {
		offset, parseErr := strconv.Atoi(q.Get("offset"))
		request := gallery.PageRequest{Offset: offset, Search: q.Get("search"), Kind: q.Get("kind"), Sort: q.Get("sort")}
		if parseErr != nil {
			h.fail(w, http.StatusBadRequest, errText("gallery offset must be an integer"))
			return
		}
		if err := gallery.CheckPage(request); err != nil {
			h.fail(w, http.StatusBadRequest, err)
			return
		}
		page, err = gallery.ReadPageContext(r.Context(), p, request, &h.observations)
		shown = page.Entries
	} else {
		shown, err = gallery.List(p)
	}
	if err != nil {
		h.fail(w, http.StatusInternalServerError, err)
		return
	}
	type withCap struct {
		gallery.Shown
		Cap string `json:"cap"`
	}
	entries := make([]withCap, len(shown))
	for i, e := range shown {
		entries[i] = withCap{e, h.capability(string(p.ID), e.ID)}
	}
	if paged {
		h.json(w, struct {
			gallery.Page
			Entries []withCap `json:"entries"`
		}{page, entries})
	} else {
		h.json(w, map[string]any{"entries": entries})
	}
}
