package query

// Every JSON answer names its fields in snake_case. Agents read --json, so a
// Go-cased key ("Author", "EventIndex") is a second spelling of the same
// vocabulary they must learn. This file walks every view's export on one rich
// fixture. Keys that are data rather than field names are recognised by what
// they are: packet ids in a review's authors map by being ids, and the status
// values bare show's summary counts by (PROVEN, BLOCKED, "trust FALSE") by
// being keys of summary's four count maps, which are keyed by value.

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"unicode"

	"whosaidso/internal/model"
	"whosaidso/internal/store"
)

// upperKeys returns every object key in v with an uppercase letter, by path.
func upperKeys(v any, path string, out *[]string) {
	switch x := v.(type) {
	case map[string]any:
		for key, child := range x {
			counted := false
			for _, kind := range []string{"tasks", "claims", "decisions", "instruments"} {
				counted = counted || strings.HasSuffix(path, "/summary/"+kind)
			}
			if strings.IndexFunc(key, unicode.IsUpper) >= 0 && !model.ValidID(model.ID(key)) && !counted {
				*out = append(*out, path+"/"+key)
			}
			upperKeys(child, path+"/"+key, out)
		}
	case []any:
		for _, child := range x {
			upperKeys(child, path+"/[]", out)
		}
	}
}

// richWorld is presetWorld plus every fact kind a read can carry: a hold, a
// terminal receipt, a trust withdrawal, a supersession, a correction, a source,
// a rejected review and a pending packet.
func richWorld(t *testing.T, p store.Project) {
	t.Helper()
	presetWorld(t, p)
	ruling := authority()
	appendEvents(t, p, 106,
		testTask(2), &model.BlockerHold{Task: testRef(2, 1), BlockerID: testID(80), Reason: model.BlockerResume,
			Actor: model.Actor{ID: "owner"}, Criterion: "resume is authorized"},
		&model.DecisionOpen{ID: testID(32), Provenance: prov("lane-c"), Spec: decisionSpec()},
		&model.TrustWithdraw{Instrument: testRef(10, 1), Scope: testScope(), RevalidationCondition: "rerun the known-answer suite"},
		&model.Supersede{Prior: testRef(31, 1), Replacement: testRef(32, 1), Reason: "the owner restated the ruling", Authority: &ruling},
		&model.Correction{Target: model.CorrectionTarget{Kind: "record", Record: ptrRef(testRef(20, 1))},
			AffectedRevisions: []model.RecordRef{testRef(20, 1)}, Reason: "wrong denominator", CorrectiveRef: testArtifact()},
		&model.SourceIntake{SourceID: testID(50), SourceRef: testArtifact(), OriginalDigest: testArtifact().Content.SHA256,
			Length: 3, Speaker: model.Actor{UnknownReason: "speaker was not identified"}, Referents: []model.RecordRef{testRef(1, 1)}})
	appendEvents(t, p, 107, &model.AttemptTerminal{Task: testRef(1, 1), AttemptID: testID(70), Outcome: model.AttemptNoReading,
		Reason: "instrument unvalidated", NextAction: "validate instrument 11", DeliveryRefs: []model.ArtifactRef{testArtifact()}})
	reviewPacket(t, p, 108, capturePacket(t, p, 3), "rejected")
	capturePacket(t, p, 4)
}

func TestEveryJSONAnswerKeyIsSnakeCase(t *testing.T) {
	p := testProject(t)
	richWorld(t, p)
	requests := append(richRequests(), ViewRequest{View: "show", Kind: "decision"}, ViewRequest{View: "show", Kind: "task"},
		ViewRequest{View: "show", Stale: richStale()}, ViewRequest{View: "continue", ID: testID(10)})
	// Control: the fixture really carries the facts whose keys were Go-cased.
	show, history := view_(t, p, ViewRequest{View: "show"}).(*ShowAnswer), historyOf(t, p, "")
	if len(show.Records) < 10 || len(history.Reviews) == 0 || len(todoOf(t, p).IntakePending) != 2 {
		t.Fatalf("control: the rich fixture must hold records, reviews and intake, got %d records, %d reviews", len(show.Records), len(history.Reviews))
	}
	for _, r := range requests {
		a, err := ReadView(p, r)
		if err != nil {
			t.Fatalf("control %s view must succeed: %v", r.View, err)
		}
		var exported bytes.Buffer
		if err := RenderViewJSON(&exported, a); err != nil {
			t.Fatal(err)
		}
		var v any
		if err := json.Unmarshal(exported.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		var upper []string
		upperKeys(v, r.View, &upper)
		if len(upper) > 0 {
			sort.Strings(upper)
			t.Errorf("%s %s exports %d keys with an uppercase letter; every field is snake_case: %s", r.View, r.ID, len(upper), strings.Join(upper[:min(len(upper), 8)], ", "))
		}
		root, _ := v.(map[string]any)
		reviews, _ := root["reviews"].([]any)
		intake, _ := root["intake_pending"].([]any)
		for _, packet := range intake {
			if review := packet.(map[string]any)["review"]; review != nil {
				reviews = append(reviews, review)
			}
		}
		for _, review := range reviews {
			var self []string
			for key := range review.(map[string]any) {
				if strings.HasPrefix(key, "self") {
					self = append(self, key)
				}
			}
			if len(self) != 1 || self[0] != "self_admission" {
				t.Errorf("%s: a review must export exactly one self_admission key, got %v", r.View, self)
			}
		}
	}
}
