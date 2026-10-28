// Text rendering preserves the export's reading without retaining a second
// fact inventory. Export auditing still returns the complete facts and paths.
package query

import (
	"bytes"
	"io"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestTextBriefMatchesTheAuditedExportWithoutRetainingFacts(t *testing.T) {
	p := screenProject(t)
	events := []model.TypedEvent{}
	for i := 1; i <= 40; i++ {
		events = append(events, testTask(i))
	}
	appendEvents(t, p, 1000, events...)
	answer := view_(t, p, ViewRequest{View: "todo"})
	var exported bytes.Buffer
	if err := RenderViewJSON(&exported, answer); err != nil {
		t.Fatal(err)
	}
	want, facts, err := ViewBriefOf(exported.Bytes())
	if err != nil || len(facts) == 0 {
		t.Fatalf("audit control: %v", err)
	}
	var text bytes.Buffer
	if err := RenderViewBrief(&text, answer); err != nil || text.String() != want {
		t.Fatalf("text differs from export: %v", err)
	}
	reference := testing.AllocsPerRun(1, func() {
		var data bytes.Buffer
		if err := RenderViewJSON(&data, answer); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ViewBriefOf(data.Bytes()); err != nil {
			t.Fatal(err)
		}
	})
	fast := testing.AllocsPerRun(1, func() {
		if err := RenderViewBrief(io.Discard, answer); err != nil {
			t.Fatal(err)
		}
	})
	if fast >= reference*.7 {
		t.Fatalf("text retained the export's fact inventory: %.0f allocations, reference %.0f", fast, reference)
	}
}
