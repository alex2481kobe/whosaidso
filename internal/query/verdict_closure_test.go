package query

// Proof verdicts and task acceptance as the reads show them: a refuted claim's standing says
// REFUTED, never UNMEASURED, and a closed task names its closer and whether
// the closer also did the work, in the brief and in --json alike. Read
// through show, which absorbed the old state preset.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
)

// refutedAndClosedWorld is presetWorld plus a refutation of claim 21 (its only
// run failed) and task 1 closed by worker, who also wrote its success receipt (its holder).
func refutedAndClosedWorld(t *testing.T) *ShowAnswer {
	t.Helper()
	p := testProject(t)
	presetWorld(t, p)
	appendEvents(t, p, 106, admittedAs(106, map[int]string{1: "worker"},
		&model.ProofAdmit{Claim: testRef(21, 1), CriterionRef: criterionRef(21), Verdict: model.VerdictRefutes,
			Evidence: []model.ObservationDisposition{{InvocationRef: model.InvocationRef{Project: projectID, InvocationID: testID(50)},
				Disposition: "contradicts", Reason: "the run failed"}},
			Judgment: model.ResponsibleJudgment{Actor: model.Actor{ID: "reviewer"}, Reason: "the failing run contradicts the criterion"}},
		&model.AttemptTerminal{Task: testRef(1, 1), AttemptID: testID(70), Outcome: "success", Reason: "done",
			NextAction: "accept it", DeliveryRefs: []model.ArtifactRef{testArtifact()}})...)
	appendEvents(t, p, 107, admittedAs(107, map[int]string{0: "worker"}, &model.TaskClose{Task: testRef(1, 1), Outcome: model.ClosureSuccess,
		AcceptanceWitnessRefs: []model.AcceptanceWitness{{CriterionID: testID(90), CriterionRevision: 1, WitnessRef: testArtifact()}},
		DeliveryWitnessRefs:   []model.ArtifactRef{testArtifact()}})...)
	return viewAnswerOf(t, p, ViewRequest{View: "show"}).(*ShowAnswer)
}

func TestRefutedClaimAndClosureReadHonestly(t *testing.T) {
	a := refutedAndClosedWorld(t)
	text := assertViewHonest(t, a)
	var claim *ClaimDetail
	for _, r := range a.Records {
		if r.Ref == testRef(21, 1) {
			claim = r.Claim
		}
	}
	if claim == nil || claim.Status != reduce.StatusRefuted || !strings.HasPrefix(claim.Standing, "REFUTED at revision 1") ||
		len(claim.Missing) != 1 || strings.Contains(claim.Missing[0], "local observation") {
		t.Fatalf("a refuted claim must read REFUTED with what would prove it, got %+v", claim)
	}
	if !strings.Contains(text, "CLAIM "+string(testID(21))+" rev 1 REFUTED") || !strings.Contains(text, "REFUTED at revision 1") {
		t.Fatalf("the brief must show the refuted standing:\n%s", text)
	}
	if !strings.Contains(text, "closed: success by worker closer authored a receipt TRUE") {
		t.Fatalf("the brief must name the closer and self-acceptance:\n%s", text)
	}
	var exported bytes.Buffer
	if err := RenderViewJSON(&exported, a); err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(exported.Bytes(), &root); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range root["records"].([]any) {
		task, _ := r.(map[string]any)["task"].(map[string]any)
		c, _ := task["closure"].(map[string]any)
		by, _ := c["closer"].(map[string]any)
		closer, _ := by["actor"].(map[string]any)
		if closer["id"] == "worker" && c["closer_authored_receipt"] == "TRUE" && c["authority_cited"] == false {
			found = true
		}
	}
	if !found {
		t.Fatalf("--json must carry the closure's closer, closer_authored_receipt and authority_cited:\n%s", exported.String())
	}
}
