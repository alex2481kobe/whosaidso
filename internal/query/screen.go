// Screen reads provide bounded viewer lists from one ledger prefix. Complete
// CLI views and record detail remain in view.go; no intake payload is loaded here.
package query

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

const ScreenPageSize = 24
const screenText = 256

type ScreenRequest struct {
	View, Search, Kind, Status, Sort string
	Under                            model.ID
	Offset                           int
	StatusCounts                     bool
}
type ScreenRow struct {
	Nested      int      `json:"nested"`
	Group       string   `json:"group,omitempty"`
	ID          model.ID `json:"id,omitempty"`
	Title       string   `json:"title"`
	Kind        string   `json:"kind"`
	Status      string   `json:"status"`
	Actor       string   `json:"actor"`
	Sequence    uint64   `json:"sequence"`
	order       int
	sortTitle   string
	searchActor string
}
type ScreenAnswer struct {
	ViewHeader
	Parent   *ScreenRow     `json:"parent,omitempty"`
	Rows     []ScreenRow    `json:"rows"`
	Counts   map[string]int `json:"counts"`
	Total    int            `json:"total"`
	Offset   int            `json:"offset"`
	Previous *int           `json:"previous"`
	Next     *int           `json:"next"`
	Note     string         `json:"note"`
}

func CheckScreen(r ScreenRequest) error {
	if r.View != "home" && r.View != "records" && r.View != "history" && r.View != "status" && r.View != "tasks" {
		return fmt.Errorf("screen must be home, tasks, records, history or status")
	}
	if r.Under != "" && (r.View != "tasks" || !model.ValidID(r.Under)) {
		return fmt.Errorf("under requires a task screen and task ULID")
	}
	if r.Offset < 0 {
		return fmt.Errorf("offset must be nonnegative")
	}
	if !utf8.ValidString(r.Search) || utf8.RuneCountInString(r.Search) > 512 {
		return fmt.Errorf("search must be valid UTF-8, at most 512 characters")
	}
	if r.Kind != "" && !strings.EqualFold(r.Kind, "SOURCE") && viewKinds[strings.ToLower(r.Kind)] == "" {
		return fmt.Errorf("unknown record kind")
	}
	if r.Sort != "" && r.Sort != "recent" && r.Sort != "oldest" && r.Sort != "title" && r.Sort != "kind" {
		return fmt.Errorf("unknown screen sort")
	}
	if len(r.Status) > 64 {
		return fmt.Errorf("status is too long")
	}
	if r.View == "history" && (r.Kind != "" || r.Status != "" || r.StatusCounts || (r.Sort != "" && r.Sort != "recent" && r.Sort != "oldest")) {
		return fmt.Errorf("history has no record status or kind filter")
	}
	return nil
}

func ReadScreen(p store.Project, r ScreenRequest) (*ScreenAnswer, error) {
	if err := CheckScreen(r); err != nil {
		return nil, err
	}
	source, err := store.Load(p)
	if err != nil {
		return nil, err
	}
	return ReadScreenFrom(p, r, source)
}

// ReadScreenFrom selects a bounded answer from an already validated prefix.
func ReadScreenFrom(p store.Project, r ScreenRequest, source Source) (*ScreenAnswer, error) {
	s := source.Snapshot()
	a := &ScreenAnswer{ViewHeader: ViewHeader{View: r.View, Project: p.ID, Watermark: watermarkOf(s), Result: "KNOWN"}, Rows: []ScreenRow{}, Counts: map[string]int{}, Note: "Previews are shortened. Open a record for its complete detail. Counts cover the whole ledger; lists contain one page. Task status totals load on request. Intake proposals are available through whosaidso packets."}
	if r.View == "status" {
		a.Note = "Ledger watermark only"
		return a, nil
	}
	if r.View == "tasks" {
		return taskScreen(s, r, a), nil
	}
	if r.View == "home" {
		return overview(s, r, a), nil
	}
	rows := []ScreenRow{}
	search := strings.ToLower(strings.TrimSpace(r.Search))
	if r.View == "history" {
		return screenHistoryPage(r, source, a)
	} else {
		if r.StatusCounts {
			for _, status := range []string{"READY", "BLOCKED", "IN FLIGHT", "CLOSED"} {
				a.Counts[status] = 0
			}
		}
		for _, head := range s.RecordHeads() {
			row := ScreenRow{ID: head.Key.ID, Kind: string(head.Kind), Actor: screenPreview(actorName(s.EventAuthor(head.Origin).Author)), Sequence: head.Origin.Sequence, order: head.Origin.EventIndex}
			row.searchActor = actorName(s.EventAuthor(head.Origin).Author)
			title := head.Text
			if r.StatusCounts || r.Status != "" || search != "" {
				fillScreenStatus(s, &row)
			}
			a.Counts[row.Kind]++
			a.Counts["records"]++
			if r.StatusCounts && head.Kind == model.Task {
				a.Counts[row.Status]++
			}
			if (r.Kind != "" && strings.ToUpper(r.Kind) != row.Kind) || (r.Status != "" && r.Status != row.Status) || (search != "" && !strings.Contains(strings.ToLower(title+" "+string(row.ID)+" "+row.searchActor+" "+row.Status), search)) {
				continue
			}
			row.sortTitle = title
			row.Title = screenPreview(title)
			rows = append(rows, row)
		}
		for _, src := range s.SourceHeads() {
			title := fmt.Sprintf("%s said, %d bytes", actorName(src.Speaker), src.Length)
			row := ScreenRow{ID: src.Key.Source, Kind: "SOURCE", Status: "Captured", Actor: screenPreview(actorName(src.Speaker)), Title: screenPreview(title), sortTitle: title, Sequence: src.Origin.Sequence}
			a.Counts["SOURCE"]++
			a.Counts["records"]++
			if (r.Kind == "" || strings.ToUpper(r.Kind) == row.Kind) && (r.Status == "" || r.Status == row.Status) && (search == "" || strings.Contains(strings.ToLower(title+" "+string(row.ID)), search)) {
				rows = append(rows, row)
			}
		}
	}
	sortScreenRows(rows, r.Sort)
	end := a.bounds(len(rows), r.Offset)
	a.Rows = append(a.Rows, rows[a.Offset:end]...)
	for i := range a.Rows {
		if a.Rows[i].Kind != "SOURCE" && a.Rows[i].Status == "" {
			fillScreenStatus(s, &a.Rows[i])
		}
	}
	return a, nil
}

func screenPreview(text string) string {
	r := []rune(text)
	if len(r) > screenText {
		return string(r[:screenText-1]) + "…"
	}
	return text
}

func (a *ScreenAnswer) bounds(total, offset int) int {
	a.Total = total
	a.Offset = offset
	if a.Offset >= total {
		a.Offset = max(0, (total-1)/ScreenPageSize*ScreenPageSize)
	}
	end := a.Offset + min(ScreenPageSize, total-a.Offset)
	if a.Offset > 0 {
		v := max(0, a.Offset-ScreenPageSize)
		a.Previous = &v
	}
	if end < total {
		a.Next = &end
	}
	return end
}
func fillScreenStatus(s reduce.Snapshot, row *ScreenRow) {
	fact, ok := s.RecordSummary(reduce.Ident{Project: s.Project(), ID: row.ID})
	if !ok {
		row.Status = "UNKNOWN"
		return
	}
	row.Status = fact.Status
	if fact.Kind == model.Instrument {
		row.Status = "See validation and trust in record"
	}
	if fact.NextActor != nil {
		row.searchActor = actorName(*fact.NextActor)
		row.Actor = screenPreview(row.searchActor)
	}
}

func sortScreenRows(rows []ScreenRow, sortBy string) {
	sort.SliceStable(rows, func(i, j int) bool {
		if sortBy == "title" && rows[i].sortTitle != rows[j].sortTitle {
			return rows[i].sortTitle < rows[j].sortTitle
		}
		if sortBy == "kind" && rows[i].Kind != rows[j].Kind {
			return rows[i].Kind < rows[j].Kind
		}
		if rows[i].Sequence == rows[j].Sequence {
			if rows[i].order != rows[j].order {
				if sortBy == "oldest" {
					return rows[i].order < rows[j].order
				}
				return rows[i].order > rows[j].order
			}
			if sortBy == "oldest" {
				return rows[i].ID < rows[j].ID
			}
			return rows[i].ID > rows[j].ID
		}
		if sortBy == "oldest" {
			return rows[i].Sequence < rows[j].Sequence
		}
		return rows[i].Sequence > rows[j].Sequence
	})
}
