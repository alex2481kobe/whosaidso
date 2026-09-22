package main

// End-to-end: an agent records the owner's ruling, and fresh `datum show` and
// `datum history` processes name the packet author beside the authority and
// the quote (R10.1 revised). Admission rules live in internal/write.

import (
	"strings"
	"testing"

	"datum/internal/model"
)

func TestCLIShowAndHistoryNameTheRulingsPacketAuthor(t *testing.T) {
	root, _ := cliFixture(t)
	const ruling = `{"ruling":"ship revision one"}`
	proofWrite(t, root, "rulings/decision.json", ruling)
	scope := model.Scope{SourcePaths: []string{}, ContextRefs: []model.RecordRef{}, AppliesWhen: "this fixture", Limitations: "not a real ledger"}
	decision := cliID(10)
	open := &model.DecisionOpen{ID: decision, Provenance: model.Provenance{Author: model.Actor{ID: "lane"}, SourceRefs: []model.ArtifactRef{}},
		Spec: model.DecisionSpec{Question: "ship revision one", Options: []string{"yes", "no"}, WaitingActor: model.Actor{ID: "owner"}, Scope: scope}}
	source := e2ePin(ruling, "rulings/decision.json", "application/json")
	dispose := &model.DecisionDispose{Decision: model.RecordRef{Project: "test/cli", RecordID: decision, Revision: 1}, Disposition: "approved",
		Quote: "ship revision one", Scope: scope, Authority: model.Authority{Actor: model.Actor{ID: "owner"}, SourceRef: source,
			Selector: model.Selector{Kind: "json-pointer", Pointer: "/ruling"}, Scope: scope}}
	for i, step := range []struct {
		event  model.TypedEvent
		author string
	}{{open, "lane"}, {dispose, "agent-sol"}} {
		packet, admission := string(cliID(20+2*i)), string(cliID(21+2*i))
		if _, err := e2eInvoke(t, root, []model.TypedEvent{step.event}, "capture", "--command-id", packet, "--actor", step.author); err != nil {
			t.Fatal(err)
		}
		if _, err := e2eInvoke(t, root, nil, "admit", "--command-id", admission, "--actor", "reviewer", "--outcome", "accepted", "--reason", "recorded the owner's ruling", packet); err != nil {
			t.Fatal(err)
		}
	}
	show := string(readProcess(t, root, nil, "show", string(decision)))
	// The record's own fact (decision.open, by lane) and its disposition (by
	// agent-sol, recording the owner's words) each name their packet.
	for _, want := range []string{`"Packet": "` + string(cliID(20)) + `"`, `"id": "agent-sol"`, `"Packet": "` + string(cliID(22)) + `"`, `"quote": "ship revision one"`} {
		if !strings.Contains(show, want) {
			t.Fatalf("datum show lacks %s beside the authority and quote:\n%s", want, show)
		}
	}
	history := readJSON(t, readProcess(t, root, nil, "history", "--json", string(decision)))
	var disposeAuthor, openAuthor string
	for _, e := range history.History {
		switch e.Event.Type {
		case "decision.dispose":
			disposeAuthor = e.Author.Author.ID
		case "decision.open":
			openAuthor = e.Author.Author.ID
		}
	}
	if disposeAuthor != "agent-sol" || openAuthor != "lane" {
		t.Fatalf("history authors: dispose %q, open %q; want agent-sol and lane", disposeAuthor, openAuthor)
	}
}
