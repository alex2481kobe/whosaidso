// Record capture times are a bounded projection of that record's history.
// Missing packet attribution remains UNKNOWN; neighboring timestamps never fill it.
package query

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

type RecordTimes struct {
	ViewHeader
	Times map[string]model.Availability[time.Time] `json:"times"`
}

func ReadRecordTimes(p store.Project, id model.ID) (*RecordTimes, error) {
	if id == "" || !model.ValidID(id) {
		return nil, fmt.Errorf("capture times require a record ULID")
	}
	source, err := store.Load(p)
	if err != nil {
		return nil, err
	}
	answer, err := ReadViewFrom(p, ViewRequest{View: "history", ID: id}, source)
	if err != nil {
		return nil, err
	}
	history := answer.(*HistoryAnswer)
	stamps := map[uint64]map[model.ID]model.Availability[time.Time]{}
	result := map[string]model.Availability[time.Time]{}
	for _, row := range history.Events {
		sequence := row.Origin.Sequence
		if _, ok := stamps[sequence]; !ok {
			stamps[sequence] = map[model.ID]model.Availability[time.Time]{}
			selected, err := source.BundleRange(int(sequence)-1, 1)
			if err != nil {
				return nil, err
			}
			if len(selected) != 1 {
				return nil, fmt.Errorf("event sequence is outside the selected prefix")
			}
			for _, raw := range selected[0].Events {
				if raw.Type != (*model.ReviewAdmit)(nil).EventType() {
					continue
				}
				var review struct {
					CapturedAt map[model.ID]model.Availability[time.Time] `json:"captured_at"`
				}
				if err := json.Unmarshal(raw.Data, &review); err != nil {
					return nil, err
				}
				stamps[sequence] = review.CapturedAt
			}
		}
		at, ok := stamps[sequence][row.Author.Packet]
		if !ok {
			at = model.Availability[time.Time]{State: model.Unknown, Reason: "this event has no recorded packet capture time"}
		}
		result[fmt.Sprintf("%d:%d", sequence, row.Origin.EventIndex)] = at
	}
	return &RecordTimes{ViewHeader: history.ViewHeader, Times: result}, nil
}
