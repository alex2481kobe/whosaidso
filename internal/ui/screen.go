// Screen HTTP requests translate bounded viewer filters. Ledger selection and
// preview limits belong to query; this endpoint never changes CLI view answers.
package ui

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/query"
)

func (h *handler) screen(w http.ResponseWriter, r *http.Request) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		h.fail(w, 400, err)
		return
	}
	allowed := map[string]bool{"project": true, "screen": true, "offset": true, "search": true, "kind": true, "status": true, "sort": true, "status_counts": true}
	for k, v := range q {
		if !allowed[k] || len(v) != 1 {
			h.fail(w, 400, fmt.Errorf("invalid screen parameter %s", k))
			return
		}
	}
	offset := 0
	if q.Has("offset") {
		offset, err = strconv.Atoi(q.Get("offset"))
		if err != nil {
			h.fail(w, 400, err)
			return
		}
	}
	if q.Get("status_counts") != "" && q.Get("status_counts") != "1" {
		h.fail(w, 400, fmt.Errorf("status_counts must be 1 or omitted"))
		return
	}
	request := query.ScreenRequest{StatusCounts: q.Get("status_counts") == "1", View: q.Get("screen"), Offset: offset, Search: q.Get("search"), Kind: q.Get("kind"), Status: q.Get("status"), Sort: q.Get("sort")}
	if err := query.CheckScreen(request); err != nil {
		h.fail(w, 400, err)
		return
	}
	p, ok, err := h.find(model.ProjectID(q.Get("project")))
	if err != nil {
		h.fail(w, 500, err)
		return
	}
	if !ok {
		h.fail(w, 404, fmt.Errorf("no available project"))
		return
	}
	source, err := h.ledger.Load(p)
	if err != nil {
		h.fail(w, 500, err)
		return
	}
	answer, err := query.ReadScreenFrom(p, request, source)
	if err != nil {
		h.fail(w, 500, err)
		return
	}
	h.json(w, answer)
}

func (h *handler) recordTimes(w http.ResponseWriter, r *http.Request) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		h.fail(w, 400, err)
		return
	}
	for key, values := range q {
		if (key != "project" && key != "id") || len(values) != 1 {
			h.fail(w, 400, fmt.Errorf("invalid capture-time parameter %s", key))
			return
		}
	}
	p, ok, err := h.find(model.ProjectID(q.Get("project")))
	if err != nil {
		h.fail(w, 500, err)
		return
	}
	if !ok {
		h.fail(w, 404, fmt.Errorf("no available project"))
		return
	}
	answer, err := query.ReadRecordTimes(p, model.ID(q.Get("id")))
	if err != nil {
		h.fail(w, 400, err)
		return
	}
	h.json(w, answer)
}
