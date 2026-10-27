// Viewer history summarizes admission batches, decoding only the requested
// range by default. Complete event detail remains the CLI history view's job.
package query

import (
	"fmt"
	"sort"
	"strings"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func screenHistoryPage(r ScreenRequest, source Source, a *ScreenAnswer) (*ScreenAnswer, error) {
	a.Counts["events"] = source.Snapshot().Watermark().Events
	total := source.Snapshot().Watermark().Bundles
	a.Counts["batches"] = total
	a.Note = "One row summarizes one admission batch. Open Tasks & records for record detail; whosaidso history reads every event. Search scans complete batches. Previews are shortened."
	search := strings.ToLower(strings.TrimSpace(r.Search))
	var bundles []model.Bundle
	var err error
	all := search != ""
	base := 0
	if !all {
		end := a.bounds(total, r.Offset)
		start := a.Offset
		if r.Sort != "oldest" {
			start, end = total-end, total-start
		}
		base = start
		if reader, ok := source.(interface {
			BundleRange(int, int) ([]model.Bundle, error)
		}); ok {
			bundles, err = reader.BundleRange(start, end-start)
		} else {
			bundles, err = source.Bundles()
			if err == nil {
				bundles = bundles[start:end]
			}
		}
	} else {
		bundles, err = source.Bundles()
	}
	if err != nil {
		return nil, err
	}
	rows := []ScreenRow{}
	for index, b := range bundles {
		position := uint64(base + index + 1)
		types := []string{}
		seen := map[string]bool{}
		matches := search == "" || strings.Contains(strings.ToLower(string(b.CommandID)+" "+actorName(b.Admitter)), search)
		for _, e := range b.Events {
			kind := string(e.Type)
			if !seen[kind] {
				seen[kind] = true
				types = append(types, kind)
			}
			if search != "" && strings.Contains(strings.ToLower(kind+" "+string(e.Data)), search) {
				matches = true
			}
		}
		title := fmt.Sprintf("Batch %d: %s", position, strings.Join(types, ", "))
		if !matches && !strings.Contains(strings.ToLower(title), search) {
			continue
		}
		rows = append(rows, ScreenRow{Title: screenPreview(title), sortTitle: title, Kind: fmt.Sprintf("%d events", len(b.Events)), Status: "Recorded", Actor: screenPreview(actorName(b.Admitter)), Sequence: position})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if r.Sort == "oldest" {
			return rows[i].Sequence < rows[j].Sequence
		}
		return rows[i].Sequence > rows[j].Sequence
	})
	if all {
		end := a.bounds(len(rows), r.Offset)
		rows = rows[a.Offset:end]
	}
	a.Rows = append(a.Rows, rows...)
	return a, nil
}
