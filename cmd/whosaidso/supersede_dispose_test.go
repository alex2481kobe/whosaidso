package main

// End-to-end owner acts that are not decisions, through fresh processes:
// supersede keeps the superseded record in show and history, and after
// artifact.dispose a claim whose proof cited the artifact reads as unverifiable
// (evidence not available, current support FALSE), never as passing.
// Admission rules live in internal/write; replay rules in internal/reduce.

import (
	"encoding/json"
	"strings"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/query"
	"whosaidso/internal/reduce"
)

func e2eAdmitOne(t *testing.T, root string, event model.TypedEvent, packet, admission int, author string) error {
	t.Helper()
	if _, err := e2eInvoke(t, root, []model.TypedEvent{event}, "capture", "--command-id", string(cliID(packet)), "--actor", author); err != nil {
		t.Fatal(err)
	}
	_, err := e2eInvoke(t, root, nil, "admit", "--command-id", string(cliID(admission)), "--actor", "coordinator", "--outcome", "accepted", "--reason", "owner act e2e", string(cliID(packet)))
	return err
}

func e2eRuling(t *testing.T, root, path, body string) model.Authority {
	t.Helper()
	proofWrite(t, root, path, body)
	scope := model.Scope{SourcePaths: []string{}, ContextRefs: []model.RecordRef{}, AppliesWhen: "this fixture", Limitations: "not a real ledger"}
	return model.Authority{Actor: model.Actor{ID: "owner"}, SourceRef: e2ePin(body, path, "application/json"), Selector: model.Selector{Kind: "json-pointer", Pointer: "/ruling"}, Scope: scope}
}

func TestCLISupersededRecordStaysInShowAndHistory(t *testing.T) {
	root, _ := cliFixture(t)
	authority := e2eRuling(t, root, "rulings/supersede.json", `{"ruling":"the second question replaces the first"}`)
	lane := model.Provenance{SourceRefs: []model.ArtifactRef{}}
	open := func(id int, question string) *model.DecisionOpen {
		return &model.DecisionOpen{ID: cliID(id), Provenance: lane, Spec: model.DecisionSpec{Question: question, Options: []string{"yes", "no"}, WaitingActor: model.Actor{ID: "owner"}, Scope: authority.Scope}}
	}
	old, replacement := model.RecordRef{Project: "test/cli", RecordID: cliID(10), Revision: 1}, model.RecordRef{Project: "test/cli", RecordID: cliID(11), Revision: 1}
	ruling := &model.DecisionDispose{Decision: old, Disposition: "approved", Quote: "the second question replaces the first", Scope: authority.Scope, Authority: authority}
	for i, event := range []model.TypedEvent{open(10, "ship revision one"), open(11, "ship revision two"), ruling} {
		if err := e2eAdmitOne(t, root, event, 20+2*i, 21+2*i, "lane"); err != nil {
			t.Fatal(err)
		}
	}
	// An owner ruling is affected, so the supersession needs its authority.
	supersede := &model.Supersede{Prior: old, Replacement: replacement, Reason: "the owner restated the question"}
	if err := e2eAdmitOne(t, root, supersede, 30, 31, "agent-sol"); err == nil || !strings.Contains(err.Error(), "authority-unavailable") {
		t.Fatalf("superseding a ruling without authority: %v", err)
	}
	supersede.Authority = &authority
	if err := e2eAdmitOne(t, root, supersede, 32, 33, "agent-sol"); err != nil {
		t.Fatal(err)
	}
	show := readProcess(t, root, nil, "show", string(old.RecordID))
	t.Logf("whosaidso show %s:\n%s", old.RecordID, show)
	answer := readJSON[query.ShowAnswer](t, readProcess(t, root, nil, "show", "--json", string(old.RecordID)))
	if len(answer.Records) != 1 || answer.Records[0].Fact.Key.ID != old.RecordID || len(answer.Records[0].Supersessions) != 1 {
		t.Fatalf("superseded record is hidden or unmarked: %+v", answer.Records)
	}
	if got := answer.Records[0].Supersessions[0].Supersede.Replacement; got != replacement {
		t.Fatalf("show names replacement %+v", got)
	}
	if answer.Records[0].CurrentSupport != reduce.TruthFalse {
		t.Fatalf("a superseded ruling still reads as current: %s", answer.Records[0].CurrentSupport)
	}
	history := readJSON[query.HistoryAnswer](t, readProcess(t, root, nil, "history", "--json", string(old.RecordID)))
	seen := map[model.EventType]string{}
	for _, e := range history.Events {
		seen[e.Event.Type] = e.Author.Author.ID
	}
	if seen["decision.open"] != "lane" || seen["decision.dispose"] != "lane" || seen["supersede"] != "agent-sol" {
		t.Fatalf("history lost the superseded record's events or authors: %v", seen)
	}
	t.Logf("whosaidso history %s:\n%s", old.RecordID, readProcess(t, root, nil, "history", string(old.RecordID)))
}

func TestCLIDisposedArtifactLeavesItsProofUnverifiable(t *testing.T) {
	root, criterion, instrument, attempt := e2eWorld(t)
	out, err := e2eInvoke(t, root, nil, "run", "--attempt-id", string(attempt), "--instrument", string(instrument.RecordID),
		"--claim", string(criterion.Claim.RecordID), "--claim-revision", "1", "--criterion-id", string(criterion.CriterionID), "--criterion-revision", "1", "--", "/bin/sh", "tools/measure.sh")
	if err != nil {
		t.Fatal(err)
	}
	var run struct {
		Envelope    model.InvocationEnvelope `json:"envelope"`
		StartPacket model.PacketRef          `json:"start_packet"`
		SealPacket  model.PacketRef          `json:"seal_packet"`
	}
	if err := json.Unmarshal(out, &run); err != nil {
		t.Fatal(err)
	}
	if _, err := e2eInvoke(t, root, nil, "admit", "--command-id", string(cliID(900)), "--actor", "coordinator", "--outcome", "accepted", "--reason", "run", string(run.StartPacket.CommandID), string(run.SealPacket.CommandID)); err != nil {
		t.Fatal(err)
	}
	proof := &model.ProofAdmit{Claim: criterion.Claim, CriterionRef: criterion, Verdict: model.VerdictSupports, Judgment: model.ResponsibleJudgment{Actor: model.Actor{ID: "lane"}, Reason: "the run passed"},
		Evidence: []model.ObservationDisposition{{InvocationRef: model.InvocationRef{Project: "test/cli", InvocationID: run.Envelope.InvocationID}, Disposition: "supports", Reason: "passed"}}}
	if err := e2eAdmitOne(t, root, proof, 901, 902, "lane"); err != nil {
		t.Fatal(err)
	}
	var output model.ArtifactRef
	for _, out := range *run.Envelope.Outputs.Value {
		if out.Name == "out/result.json" {
			output = out.Ref()
		}
	}
	dispose := &model.ArtifactDispose{Artifact: output, Digest: output.Content.SHA256, PreviousLocation: ".whosaidso/artifacts/" + string(output.Content.SHA256),
		SupportLoss: []model.SupportLoss{{Target: criterion.Claim, Reason: "its only supporting run output is deleted"}},
		Authority:   e2eRuling(t, root, "rulings/dispose.json", `{"ruling":"delete that run output"}`)}
	if err := e2eAdmitOne(t, root, dispose, 903, 904, "agent-sol"); err != nil {
		t.Fatal(err)
	}
	show := readProcess(t, root, nil, "show", string(criterion.Claim.RecordID))
	t.Logf("whosaidso show %s:\n%s", criterion.Claim.RecordID, show)
	answer := readJSON[query.ShowAnswer](t, readProcess(t, root, nil, "show", "--json", string(criterion.Claim.RecordID)))
	claim := answer.Records[0].Claim
	if answer.Records[0].Support.EvidenceAvailable != reduce.TruthFalse || answer.Records[0].CurrentSupport == reduce.TruthTrue {
		t.Fatalf("a proof citing a disposed artifact still reads as verified: %+v, %s", answer.Records[0].Support, claim.Status)
	}
}
