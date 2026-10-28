// The todo brief reads no task's source list, so a caller that renders only
// the brief may ask for answers without them. The brief text must not change
// by that, and the full answer must still carry them.
package query

import (
	"bytes"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestTodoBriefIsTheSameWithoutEachTasksSources(t *testing.T) {
	p := screenProject(t)
	appendEvents(t, p, 1000, testTask(1), testTask(2),
		&model.SourceIntake{SourceID: testID(50), SourceRef: testArtifact(), OriginalDigest: testArtifact().Content.SHA256,
			Length: 3, Speaker: model.Actor{ID: "owner"}, Order: 1, Referents: []model.RecordRef{testRef(1, 1), testRef(2, 1)}})
	full := view_(t, p, ViewRequest{View: "todo"}).(*TodoAnswer)
	light := view_(t, p, ViewRequest{View: "todo", Brief: true}).(*TodoAnswer)
	if len(full.Ready) != 2 || len(full.Ready[0].Sources) != 1 {
		t.Fatalf("control: the full answer lost its sources: %+v", full.Ready)
	}
	for _, task := range light.Ready {
		if len(task.Sources) != 0 {
			t.Fatalf("a brief answer still lists sources: %+v", task.Sources)
		}
	}
	var fullText, lightText bytes.Buffer
	if err := RenderViewBrief(&fullText, full); err != nil {
		t.Fatal(err)
	}
	if err := RenderViewBrief(&lightText, light); err != nil {
		t.Fatal(err)
	}
	if fullText.String() != lightText.String() {
		t.Fatalf("the brief changed with the sources omitted:\n%s\nvs\n%s", fullText.String(), lightText.String())
	}
}
