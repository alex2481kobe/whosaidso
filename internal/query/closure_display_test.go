package query

// What a closure and a current support say about themselves, text and JSON:
// a waived closure names the authority it cites or says it cites none (the
// gate requires none), and an UNKNOWN current support carries the reason for
// each premise it could not decide. The views' other honesty rules live in
// views_test.go and verdict_closure_test.go.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"whosaidso/internal/model"
)

func TestWaivedClosureSaysWhetherItCitesAnAuthority(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	ruling := authority()
	waive := func(task int, cited *model.Authority) *model.TaskClose {
		return &model.TaskClose{Task: testRef(task, 1), Outcome: model.ClosureWaived, Authority: cited,
			AcceptanceWitnessRefs: []model.AcceptanceWitness{}, DeliveryWitnessRefs: []model.ArtifactRef{}}
	}
	appendEvents(t, p, 101, admitted(101, testTask(2), testTask(3), testTask(4))...)
	appendEvents(t, p, 102, admitted(102, waive(2, nil), waive(3, &ruling),
		&model.TaskClose{Task: testRef(4, 1), Outcome: model.ClosureCancelled,
			AcceptanceWitnessRefs: []model.AcceptanceWitness{}, DeliveryWitnessRefs: []model.ArtifactRef{}})...)
	a := viewAnswerOf(t, p, ViewRequest{View: "show", Kind: "task"})
	text := assertViewHonest(t, a)
	for _, want := range []string{
		"closed: waived (no authority cited) by lane-a",
		"closed: waived under authority of owner by lane-a",
		"closed: cancelled by lane-a", // the word carries no excuse to qualify
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the brief must read %q:\n%s", want, text)
		}
	}
	var exported bytes.Buffer
	if err := RenderViewJSON(&exported, a); err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(exported.Bytes(), &root); err != nil {
		t.Fatal(err)
	}
	cited := map[string]any{}
	for _, r := range root["records"].([]any) {
		task, _ := r.(map[string]any)["task"].(map[string]any)
		c, _ := task["closure"].(map[string]any)
		if c != nil {
			cited[r.(map[string]any)["ref"].(map[string]any)["record_id"].(string)] = c["authority_cited"]
		}
	}
	want := map[string]any{string(testID(2)): false, string(testID(3)): true, string(testID(4)): false}
	for id, v := range want {
		if cited[id] != v {
			t.Errorf("closure of %s: authority_cited = %v, want %v (all: %v)", id, cited[id], v, cited)
		}
	}
}

func TestUnknownCurrentSupportCarriesItsReasons(t *testing.T) {
	p := testProject(t)
	presetWorld(t, p)
	a := viewAnswerOf(t, p, ViewRequest{View: "show", Kind: "claim"}).(*ShowAnswer)
	text := assertViewHonest(t, a)
	reasons := map[model.ID][]string{}
	for _, r := range a.Records {
		reasons[r.Ref.RecordID] = r.SupportUnknownBecause
		if (r.CurrentSupport == "UNKNOWN") != (len(r.SupportUnknownBecause) > 0) {
			t.Errorf("claim %s: current support %s with reasons %v; UNKNOWN always carries its reasons and nothing else does", r.Ref.RecordID, r.CurrentSupport, r.SupportUnknownBecause)
		}
	}
	// Control: claim 22 is PROVEN, and this read checks neither bytes nor scope.
	proven := reasons[testID(22)]
	if len(proven) != 2 || !strings.HasPrefix(proven[0], "evidence_available UNKNOWN: ") || !strings.HasPrefix(proven[1], "applicable_scope UNKNOWN: ") {
		t.Fatalf("a PROVEN claim's UNKNOWN support must name both undecided premises, got %v", proven)
	}
	if !strings.Contains(text, "\n    support "+proven[0]+"\n") || !strings.Contains(text, "\n    support "+proven[1]+"\n") {
		t.Errorf("the brief must show each reason whole:\n%s", text)
	}
}
