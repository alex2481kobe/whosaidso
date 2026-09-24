package query

// Tests that an admitted source.intake is read by the id its capture printed:
// show SOURCE_ID is its detail, history SOURCE_ID its event, continue says it
// is a source rather than calling it absent, and bare show counts and lists
// every source. What a record's own detail says of its sources is history_test.go.

import (
	"strings"
	"testing"

	"whosaidso/internal/model"
)

func TestASourceIsReadByItsSourceID(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	src := &model.SourceIntake{SourceID: testID(50), SourceRef: testArtifact(), OriginalDigest: testArtifact().Content.SHA256,
		Length: 3, Speaker: model.Actor{ID: "owner"}, Order: 2, Referents: []model.RecordRef{testRef(1, 1)}}
	appendEvents(t, p, 101, testTask(3), src)

	// Control: an id nothing admitted is still a watermarked UNKNOWN.
	if a := showOf(t, p, testID(51)); a.Result != "UNKNOWN" || a.Sources != nil {
		t.Fatalf("control: an absent id reads UNKNOWN with no source, got %+v", a)
	}
	a := showOf(t, p, testID(50))
	if a.Result != "KNOWN" || len(a.Records) != 0 || a.Sources == nil || len(*a.Sources) != 1 {
		t.Fatalf("show SOURCE_ID must be KNOWN with that one source, got %+v", a)
	}
	got := (*a.Sources)[0]
	if got.Key.Source != testID(50) || got.Intake.Speaker.ID != "owner" || got.Intake.Order != 2 || got.Intake.Length != 3 ||
		got.Intake.OriginalDigest != src.OriginalDigest || got.Intake.SourceRef.Kind != "content" ||
		len(got.Intake.Referents) != 1 || got.Intake.Referents[0] != testRef(1, 1) || got.Origin.Sequence != 2 || got.Origin.EventIndex != 1 {
		t.Errorf("show SOURCE_ID must carry the admitted intake and its origin, got %+v", got)
	}
	if got.AdmittedBy == nil || got.SelfAdmitted == "" {
		t.Errorf("show SOURCE_ID must say who admitted it, UNKNOWN included, got %+v", got)
	}
	text := rendered(t, a)
	for _, want := range []string{"SOURCE " + string(testID(50)) + " order 2 speaker owner", "length 3 pinned content", string(testID(1)) + " rev 1"} {
		if !strings.Contains(text, want) {
			t.Errorf("the brief of show SOURCE_ID must say %q:\n%s", want, text)
		}
	}

	h := historyOf(t, p, testID(50))
	if h.Result != "KNOWN" || len(h.Events) != 1 || h.Events[0].Event.Type != "source.intake" || h.Events[0].Origin != got.Origin {
		t.Errorf("history SOURCE_ID must list exactly its source.intake, got %+v", h.Events)
	}
	c := view_(t, p, ViewRequest{View: "continue", ID: testID(50)}).(*ContinueAnswer)
	if c.Result != "UNKNOWN" || !strings.Contains(c.Reason, "is an admitted source") || strings.Contains(c.Reason, "no admitted record") {
		t.Errorf("continue SOURCE_ID must say it is a source, never that it is absent, got %q %q", c.Result, c.Reason)
	}

	bare := view_(t, p, ViewRequest{View: "show"}).(*ShowAnswer)
	if bare.Sources == nil || len(*bare.Sources) != 1 || bare.Summary.Sources == nil || *bare.Summary.Sources != 1 {
		t.Errorf("bare show must count and list every source, got %+v %+v", bare.Sources, bare.Summary)
	}
	if kind := view_(t, p, ViewRequest{View: "show", Kind: "task"}).(*ShowAnswer); kind.Sources != nil || kind.Summary.Sources != nil {
		t.Errorf("show --kind task lists only tasks, got sources %+v", kind.Sources)
	}
}
