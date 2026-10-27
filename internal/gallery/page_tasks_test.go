// Linked task labels on gallery pages come from the same ledger snapshot.
// No bulk record view or local intake is needed to search their current intents.
package gallery

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestGalleryPageSearchesCurrentLinkedTasks(t *testing.T) {
	p := pageProject(t, 1)
	source := model.ArtifactRef{Kind: "content", Content: &model.ContentPin{SHA256: model.HashBytes([]byte("source")), Length: 6, MediaType: "text/plain", Locators: []model.Locator{}}, Selector: model.Selector{Kind: "whole"}}
	provenance := model.Provenance{SourceRefs: []model.ArtifactRef{source}}
	spec := model.TaskSpec{Intent: "Former task intent", Subject: "synthetic linked task", Scope: model.Scope{SourcePaths: []string{"scope"}, ContextRefs: []model.RecordRef{}, AppliesWhen: "testing pages", Limitations: "synthetic facts"}, NonGoals: []string{"no media verdict"}, AcceptanceCriteria: []model.AcceptanceCriterion{{ID: pageID(51), Revision: 1, Criterion: "the title is searchable"}}, ContextRefs: []model.RecordRef{}, ConstraintRefs: []model.RecordRef{}, Prerequisites: []model.Prerequisite{}, NextActor: model.Actor{ID: "contributor"}}
	task := model.RecordRef{Project: p.ID, RecordID: pageID(50), Revision: 1}
	add := &model.GalleryAdd{EntryID: pageID(600), File: model.GalleryFile{Path: "screen-00.png", SHA256: model.HashBytes([]byte("media")), Length: 5}, Title: "Attached artifact", AddedAt: time.Unix(1700000000, 0).UTC(), Actor: model.Actor{ID: "contributor"}, Tasks: []model.RecordRef{task}}
	pageAdmit(t, p, &model.TaskCreate{ID: task.RecordID, Spec: spec, Provenance: provenance}, add)
	page, err := ReadPage(p, PageRequest{Search: "FORMER TASK"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Entries[0].ID != string(add.EntryID) || page.TaskTitles[task.RecordID] != spec.Intent {
		t.Fatalf("linked title control: %+v", page)
	}
	spec.Intent = "Current unique task phrase"
	pageAdmit(t, p, &model.TaskAmend{Target: task, Replacement: spec, Provenance: provenance})
	page, err = ReadPage(p, PageRequest{Search: "current unique"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.TaskTitles[task.RecordID] != spec.Intent {
		t.Fatalf("current title not searched/projected: %+v", page)
	}
	spec.Intent = strings.Repeat("界", 200) + " searchable tail"
	pageAdmit(t, p, &model.TaskAmend{Target: model.RecordRef{Project: p.ID, RecordID: task.RecordID, Revision: 2}, Replacement: spec, Provenance: provenance})
	tail, err := ReadPage(p, PageRequest{Search: "searchable tail"})
	if err != nil {
		t.Fatal(err)
	}
	title := tail.TaskTitles[task.RecordID]
	if tail.Total != 1 || utf8.RuneCountInString(title) > 160 || !strings.HasSuffix(title, "…") {
		t.Fatalf("task preview/search bounds: %+v", tail)
	}
	former, err := ReadPage(p, PageRequest{Search: "Former task intent"})
	if err != nil {
		t.Fatal(err)
	}
	if former.Total != 0 {
		t.Fatalf("search used an old task revision: %+v", former)
	}
}

func TestGalleryTaskPreviewsBoundTextWithoutLimitingSearch(t *testing.T) {
	for _, text := range []string{"short", strings.Repeat("界", 160), strings.Repeat("界", 161)} {
		got := textPreview(text, 160)
		if !utf8.ValidString(got) || utf8.RuneCountInString(got) > 160 {
			t.Fatalf("unbounded or invalid preview: %q", got)
		}
		if utf8.RuneCountInString(text) <= 160 && got != text {
			t.Fatalf("short text changed: %q", got)
		}
		if utf8.RuneCountInString(text) > 160 && !strings.HasSuffix(got, "…") {
			t.Fatal("shortened text was not marked")
		}
	}
}
