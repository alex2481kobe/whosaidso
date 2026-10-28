// Gallery pages select and order ledger metadata before observing local media.
// HTTP parameters, capabilities, and rendering belong to the viewer.
package gallery

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

// PageSize bounds file observations and media elements for a gallery page.
const PageSize = 24

// PageRequest filters the complete live gallery before applying its offset.
type PageRequest struct {
	Offset int
	Search string
	Kind   string
	Sort   string
}

// Page carries navigation offsets, so clients do not repeat pagination policy.
// TaskTitles contains current local task intents linked from the selected page.
type Page struct {
	Revision   model.Digest        `json:"revision"`
	Entries    []Shown             `json:"entries"`
	Total      int                 `json:"total"`
	Offset     int                 `json:"offset"`
	Previous   *int                `json:"previous"`
	Next       *int                `json:"next"`
	TaskTitles map[model.ID]string `json:"task_titles"`
}

// CheckPage refuses invalid queries before any ledger or media read.
func CheckPage(r PageRequest) error {
	switch {
	case r.Offset < 0:
		return fmt.Errorf("gallery offset must be nonnegative")
	case !utf8.ValidString(r.Search) || utf8.RuneCountInString(r.Search) > 512:
		return fmt.Errorf("gallery search must be valid UTF-8 and at most 512 characters")
	case r.Kind != "" && r.Kind != Image && r.Kind != Video && r.Kind != Text && r.Kind != "other":
		return fmt.Errorf("unknown gallery kind %q", r.Kind)
	case r.Sort != "" && r.Sort != "recent" && r.Sort != "oldest" && r.Sort != "title" && r.Sort != "kind":
		return fmt.Errorf("unknown gallery sort %q", r.Sort)
	}
	return nil
}

// ReadPage observes the selected files without reusing previous observations.
func ReadPage(p store.Project, r PageRequest) (Page, error) {
	return ReadPageContext(context.Background(), p, r, nil)
}

// ReadPageContext stops file reads on cancellation and bounds hashing per page.
func ReadPageContext(ctx context.Context, p store.Project, r PageRequest, observer *Observer) (Page, error) {
	if observer == nil {
		observer = &Observer{}
	}
	budget := PageHashBytes
	return readPageContext(ctx, p, r, func(p store.Project, e reduce.GalleryEntry) Shown { return observer.observe(ctx, p, e, &budget) })
}
func readPage(p store.Project, r PageRequest, observe func(store.Project, reduce.GalleryEntry) Shown) (Page, error) {
	return readPageContext(context.Background(), p, r, observe)
}
func readPageContext(ctx context.Context, p store.Project, r PageRequest, observe func(store.Project, reduce.GalleryEntry) Shown) (Page, error) {
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	if err := CheckPage(r); err != nil {
		return Page{}, err
	}
	snapshot, err := gallerySnapshot(p)
	if err != nil {
		return Page{}, err
	}
	return pageFromContext(ctx, p, r, snapshot, observe)
}

// ReadPageFromContext observes one page of a validated immutable prefix.
func ReadPageFromContext(ctx context.Context, p store.Project, r PageRequest, observer *Observer, snapshot reduce.Snapshot) (Page, error) {
	if observer == nil {
		observer = &Observer{}
	}
	budget := PageHashBytes
	return pageFromContext(ctx, p, r, snapshot, func(p store.Project, e reduce.GalleryEntry) Shown { return observer.observe(ctx, p, e, &budget) })
}

func pageFromContext(ctx context.Context, p store.Project, r PageRequest, snapshot reduce.Snapshot, observe func(store.Project, reduce.GalleryEntry) Shown) (Page, error) {
	if err := CheckPage(r); err != nil {
		return Page{}, err
	}
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	records := snapshot.Gallery()
	revisionBytes, err := json.Marshal(records)
	if err != nil {
		return Page{}, err
	}
	revision := model.HashBytes(revisionBytes)
	names := linkedTaskTitles(snapshot, p.ID, records)
	selected := make([]reduce.GalleryEntry, 0, len(records))
	search := strings.ToLower(strings.TrimSpace(r.Search))
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return Page{}, err
		}
		if record.Retracted {
			continue
		}
		kind, _ := Kind(record.Add.File.Path)
		if kind == None {
			kind = "other"
		}
		if r.Kind != "" && r.Kind != kind {
			continue
		}
		if search != "" && !strings.Contains(searchable(p, record, names), search) {
			continue
		}
		selected = append(selected, record)
	}
	switch r.Sort {
	case "oldest":
	case "kind":
		slices.SortStableFunc(selected, func(a, b reduce.GalleryEntry) int {
			ak, _ := Kind(a.Add.File.Path)
			bk, _ := Kind(b.Add.File.Path)
			return strings.Compare(ak, bk)
		})
	case "title":
		slices.SortStableFunc(selected, func(a, b reduce.GalleryEntry) int {
			return strings.Compare(strings.ToLower(a.Add.Title), strings.ToLower(b.Add.Title))
		})
	default:
		slices.Reverse(selected)
	}
	page := Page{Revision: revision, Entries: []Shown{}, Total: len(selected), TaskTitles: map[model.ID]string{}}
	if len(selected) == 0 {
		return page, nil
	}
	// Retractions can remove the last page while it is open. Clamp to the last
	// nonempty page without adding untrusted offsets and risking integer overflow.
	page.Offset = r.Offset
	if page.Offset >= len(selected) {
		page.Offset = ((len(selected) - 1) / PageSize) * PageSize
	}
	end := page.Offset + min(PageSize, len(selected)-page.Offset)
	if page.Offset > 0 {
		previous := max(0, page.Offset-PageSize)
		page.Previous = &previous
	}
	if end < len(selected) {
		next := end
		page.Next = &next
	}
	for _, record := range selected[page.Offset:end] {
		if err := ctx.Err(); err != nil {
			return Page{}, err
		}
		page.Entries = append(page.Entries, observe(p, record))
		if err := ctx.Err(); err != nil {
			return Page{}, err
		}
		for _, task := range record.Add.Tasks {
			if task.Project == p.ID {
				if title, ok := names[task.RecordID]; ok {
					page.TaskTitles[task.RecordID] = textPreview(title, 160)
				}
			}
		}
	}
	return page, nil
}

func linkedTaskTitles(snapshot reduce.Snapshot, project model.ProjectID, records []reduce.GalleryEntry) map[model.ID]string {
	names := map[model.ID]string{}
	seen := map[model.ID]bool{}
	for _, record := range records {
		if record.Retracted {
			continue
		}
		for _, ref := range record.Add.Tasks {
			if ref.Project != project || seen[ref.RecordID] {
				continue
			}
			seen[ref.RecordID] = true
			if task, ok := snapshot.Current(reduce.Ident{Project: project, ID: ref.RecordID}); ok && task.Task != nil {
				names[ref.RecordID] = strings.TrimSpace(task.Task.Intent)
			}
		}
	}
	return names
}

func searchable(p store.Project, record reduce.GalleryEntry, names map[model.ID]string) string {
	a := record.Add
	path := a.File.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(p.Root, filepath.FromSlash(path))
	}
	words := []string{a.Title, a.Note, a.File.Path, path, a.Actor.ID, a.AddedAt.Format(time.RFC3339Nano)}
	for _, task := range a.Tasks {
		words = append(words, string(task.RecordID), string(task.Project))
		if task.Project == p.ID {
			words = append(words, names[task.RecordID])
		}
	}
	return strings.ToLower(strings.Join(words, "\n"))
}

// Text previews bound response metadata without altering the full text searched
// in the ledger. An ellipsis marks every shortened value, including diagnostics.
func textPreview(text string, limit int) string {
	count, end := 0, 0
	for at := range text {
		if count == limit {
			return text[:end] + "…"
		}
		if count == limit-1 {
			end = at
		}
		count++
	}
	return text
}
