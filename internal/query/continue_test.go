package query

// continue and provider-seam tests: the brief composes NOW, STATE and one
// task's closure, attempts and runs with the caller's observation, writes
// nothing, and no provider can change what the deterministic rules answer.

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"datum/internal/model"
)

func TestContinueComposesTheBriefAndWritesNothing(t *testing.T) {
	p := testProject(t)
	presetWorld(t, p)
	before := treeBytes(t, p.Root)
	a := presetAnswer(t, p, Request{Command: "continue", ID: testID(1)})
	assertHonestRendering(t, a)
	if !reflect.DeepEqual(before, treeBytes(t, p.Root)) {
		t.Fatal("continue wrote to the project; the brief is a disposable export and no handoff record exists")
	}
	c := a.Preset.Continue
	for name, got := range map[string]model.AvailabilityState{"head": c.Observed.Head.State, "dirty": c.Observed.Dirty.State, "time": c.Observed.ObservedAt.State} {
		if got != model.Unknown {
			t.Fatalf("an unsupplied %s observation must be UNKNOWN, got %s", name, got)
		}
	}
	if c.Now == nil || c.State == nil || len(*c.Now.InFlight) != 1 || len(*c.State.Claims) != 3 || len(c.Runs) != 3 || len(c.Attempts) != 1 {
		t.Fatalf("continue must carry NOW, STATE, this task's runs and attempts, got %+v", c)
	}
	if _, ok := c.Attempts[0].NextAction.(Unknown); !ok || !c.Attempts[0].Live || !strings.HasPrefix(c.Handoff, "none") {
		t.Fatalf("a live attempt has no recorded next action yet: that is UNKNOWN, got %+v", c.Attempts[0])
	}
	if _, ok := c.Progress.(Unknown); !ok {
		t.Fatalf("absent progress must be UNKNOWN, got %+v", c.Progress)
	}
	appendEvents(t, p, 106, &model.AttemptTerminal{Task: testRef(1, 1), AttemptID: testID(70), Outcome: model.AttemptNoReading,
		Reason: "instrument unvalidated", NextAction: "validate instrument 11", DeliveryRefs: []model.ArtifactRef{testArtifact()}})
	at := presetStart.Add(3 * time.Hour)
	dirty := true
	head := model.GitHead{ObjectFormat: "sha1", Commit: strings.Repeat("b", 40)}
	observed := Observation{Head: known(head), Dirty: known(dirty), ObservedAt: known(at)}
	a = presetAnswer(t, p, Request{Command: "continue", ID: testID(1), Observed: &observed})
	assertHonestRendering(t, a)
	c = a.Preset.Continue
	got := c.Attempts[0]
	if got.NextAction != "validate instrument 11" || len(got.DeliveryRefs) != 1 || got.Live || got.Outcome != model.AttemptNoReading {
		t.Fatalf("continue must carry the terminal next action and delivery refs, got %+v", got)
	}
	if *c.Observed.Head.Value != head || !*c.Observed.Dirty.Value || !c.Observed.ObservedAt.Value.Equal(at) {
		t.Fatalf("continue must carry the caller's observation verbatim, got %+v", c.Observed)
	}
}

type fakeProvider struct {
	proposal Proposal
	err      error
	asked    []Question
}

func (f *fakeProvider) Propose(_ context.Context, q Question) (Proposal, error) {
	f.asked = append(f.asked, q)
	return f.proposal, f.err
}

func TestNilProviderWorksOfflineAndNoProviderChangesTheAnswer(t *testing.T) {
	p := testProject(t)
	presetWorld(t, p)
	offline := presetAnswer(t, p, Request{Command: "context", ID: testID(1)})
	assertHonestRendering(t, offline)
	if prop := offline.Preset.Proposals; prop.Availability != "UNKNOWN" || prop.Reason == "" || len(prop.Candidates) != 0 {
		t.Fatalf("a nil provider must yield no proposals and say so, got %+v", prop)
	}
	eager := &fakeProvider{proposal: Proposal{Provider: "laya", Version: "0.1", Thresholds: map[string]string{"min": "0.7"},
		Candidates: []Candidate{{Ref: testRef(20, 1), Score: "0.91"}}}}
	failing := &fakeProvider{err: errors.New("network down")}
	anonymous := &fakeProvider{proposal: Proposal{Candidates: []Candidate{{Ref: testRef(20, 1), Score: "1"}}}}
	for _, provider := range []*fakeProvider{eager, failing, anonymous} {
		a := presetAnswer(t, p, Request{Command: "context", ID: testID(1), Provider: provider})
		assertHonestRendering(t, a)
		if !reflect.DeepEqual(a.Records, offline.Records) || !reflect.DeepEqual(a.Preset.Closure, offline.Preset.Closure) ||
			!reflect.DeepEqual(a.Preset.Attention, offline.Preset.Attention) {
			t.Fatal("a provider changed a deterministic part of the answer")
		}
		if len(provider.asked) != 1 || provider.asked[0].Operation != OperationRetrieve || provider.asked[0].Refs[0] != testRef(1, 1) {
			t.Fatalf("the provider must be asked one scoped retrieve question, got %+v", provider.asked)
		}
	}
	if prop := presetAnswer(t, p, Request{Command: "context", ID: testID(1), Provider: eager}).Preset.Proposals; prop.Availability != "known" ||
		prop.Provider != "laya" || prop.Thresholds["min"] != "0.7" || len(prop.Candidates) != 1 || !strings.Contains(prop.Provenance, "not a judgment") {
		t.Fatalf("an attributed proposal must keep its provider, version, thresholds and provenance, got %+v", prop)
	}
	for _, provider := range []*fakeProvider{failing, anonymous} {
		if prop := presetAnswer(t, p, Request{Command: "context", ID: testID(1), Provider: provider}).Preset.Proposals; prop.Availability != "UNKNOWN" || len(prop.Candidates) != 0 {
			t.Fatalf("a failing or unattributed provider must be UNKNOWN with no candidates, got %+v", prop)
		}
	}
}

func TestProposalTypesCarryNoStatusOrAcceptance(t *testing.T) {
	forbidden := []string{"status", "accept", "disposition", "judgment", "outcome", "admit", "approved"}
	for _, typ := range []reflect.Type{reflect.TypeOf(Proposal{}), reflect.TypeOf(Candidate{}), reflect.TypeOf(Question{})} {
		for i := 0; i < typ.NumField(); i++ {
			name := strings.ToLower(typ.Field(i).Name + " " + typ.Field(i).Tag.Get("json"))
			for _, word := range forbidden {
				if strings.Contains(name, word) {
					t.Fatalf("%s.%s lets a provider return a %s; a provider only proposes", typ.Name(), typ.Field(i).Name, word)
				}
			}
		}
	}
}
