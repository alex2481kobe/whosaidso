package ui

// The viewer's two JSON endpoints live here. /api/view is exactly the CLI's
// --json answer for one view, rendered by the same function; /api/projects
// is the registry with each available project's watermark and owed counts
// read through the same todo view. No view logic is repeated here: labels,
// counts and layout are ui.js's.

import (
	"bytes"
	"encoding/json"
	"net/http"

	"whosaidso/internal/model"
	"whosaidso/internal/query"
	"whosaidso/internal/store"
)

// projectRow is one registry entry as the page lists it. Watermark and
// Totals are the todo view's own; an unavailable project has neither and
// says why.
type projectRow struct {
	ID        model.ProjectID   `json:"id"`
	Home      string            `json:"home"`
	Available bool              `json:"available"`
	Reason    string            `json:"reason,omitempty"`
	Watermark *query.Watermark  `json:"watermark,omitempty"`
	Totals    *query.TodoTotals `json:"totals,omitempty"`
}

func (h *handler) projects(w http.ResponseWriter, r *http.Request) {
	list, err := h.cfg.Projects()
	if err != nil {
		h.fail(w, http.StatusInternalServerError, err)
		return
	}
	rows := []projectRow{}
	for _, entry := range list {
		row := projectRow{ID: entry.ID, Home: entry.Home, Reason: entry.Reason}
		if entry.Reason == "" {
			answer, err := h.cfg.View(r.Context(), h.opened(entry), query.ViewRequest{View: "todo"}, false)
			if todo, ok := answer.(*query.TodoAnswer); err == nil && ok {
				row.Available, row.Watermark, row.Totals = true, &todo.Watermark, &todo.Totals
			} else if err != nil {
				row.Reason = err.Error()
			}
		}
		rows = append(rows, row)
	}
	current := model.ProjectID("")
	if h.cfg.Invoked != nil {
		current = h.cfg.Invoked.ID
	}
	h.json(w, map[string]any{"current": current, "projects": rows})
}

// opened is the project a view reads: the one the command was invoked in,
// opened there as the CLI would open it, else the registry's home itself.
func (h *handler) opened(entry store.Registration) store.Project {
	if h.cfg.Invoked != nil && h.cfg.Invoked.ID == entry.ID {
		return *h.cfg.Invoked
	}
	return entry.Project
}

// view answers GET /api/view?project=P&view=V[&id=ID][&stale=1] with the
// bytes `whosaidso V [ID] [--stale] --json` prints for the same project.
func (h *handler) view(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	project, ok, err := h.find(model.ProjectID(q.Get("project")))
	switch {
	case err != nil:
		h.fail(w, http.StatusInternalServerError, err)
		return
	case !ok:
		h.fail(w, http.StatusNotFound, errText("no available project "+q.Get("project")+" on this machine"))
		return
	}
	request := query.ViewRequest{View: q.Get("view"), ID: model.ID(q.Get("id"))}
	stale := q.Get("stale") == "1"
	if stale && request.View != "show" {
		h.fail(w, http.StatusBadRequest, errText("the stale-claims check belongs only to show"))
		return
	}
	if err := query.CheckView(request); err != nil {
		h.fail(w, http.StatusBadRequest, err)
		return
	}
	answer, err := h.cfg.View(r.Context(), project, request, stale)
	if err != nil {
		h.fail(w, http.StatusInternalServerError, err)
		return
	}
	var body bytes.Buffer
	if err := query.RenderViewJSON(&body, answer); err != nil {
		h.fail(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(body.Bytes())
}

// find resolves a project id to an available registered project.
func (h *handler) find(id model.ProjectID) (store.Project, bool, error) {
	list, err := h.cfg.Projects()
	if err != nil {
		return store.Project{}, false, err
	}
	for _, entry := range list {
		if entry.ID == id && entry.Reason == "" {
			return h.opened(entry), true, nil
		}
	}
	return store.Project{}, false, nil
}

type errText string

func (e errText) Error() string { return string(e) }

// fail answers with the CLI's --json failure shape, {"error":{"message"}}.
func (h *handler) fail(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": err.Error()}})
}

func (h *handler) json(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
