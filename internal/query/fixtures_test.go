package query

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whosaidso/internal/model"
	"whosaidso/internal/reduce"
	"whosaidso/internal/store"
	"whosaidso/internal/write"
)

const projectID = model.ProjectID("example/query-tests")

func testID(n int) model.ID { return model.ID(fmt.Sprintf("%026d", n)) }
func testRef(n int, revision model.Revision) model.RecordRef {
	return model.RecordRef{Project: projectID, RecordID: testID(n), Revision: revision}
}
func testProject(t *testing.T) store.Project {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	return store.Project{ID: projectID, Root: root, Ledger: filepath.Join(root, ".whosaidso", "events")}
}
func testScope() model.Scope {
	return model.Scope{SourcePaths: []string{"internal/query/query.go"}, ContextRefs: []model.RecordRef{},
		AppliesWhen: "the U09 fixture runs", Limitations: "does not certify production completion"}
}
func testTask(n int) *model.TaskCreate {
	return &model.TaskCreate{ID: testID(n), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}},
		Spec: model.TaskSpec{Intent: "Build U09 of WhoSaidSo: the first usable read slice", Subject: "WhoSaidSo",
			Scope: testScope(), NonGoals: []string{"change canonical state during reads"},
			AcceptanceCriteria: []model.AcceptanceCriterion{{ID: testID(90), Revision: 1, Criterion: "text and JSON agree"}},
			ContextRefs:        []model.RecordRef{}, ConstraintRefs: []model.RecordRef{}, Prerequisites: []model.Prerequisite{},
			NextActor: model.Actor{ID: "acceptance-owner"}}}
}
func testArtifact() model.ArtifactRef {
	return model.ArtifactRef{Kind: "content", Content: &model.ContentPin{SHA256: model.HashBytes([]byte("U09")), Length: 3,
		MediaType: "text/plain", Locators: []model.Locator{}}, Selector: model.Selector{Kind: "whole"}}
}
func appendEvents(t *testing.T, project store.Project, n int, events ...model.TypedEvent) model.Bundle {
	t.Helper()
	raw := []model.Event{}
	for _, event := range events {
		encoded, err := model.EncodeEvent(event)
		if err != nil {
			t.Fatalf("control event must pass the strict codec: %v", err)
		}
		raw = append(raw, encoded)
	}
	bundle, err := store.Transact(context.Background(), project, testID(n), model.HashBytes([]byte(fmt.Sprint(n))),
		func(prefix []model.Bundle) (model.Bundle, error) {
			return model.Bundle{Admitter: model.Actor{ID: "reviewer"}, Packets: []model.PacketRef{}, Events: raw}, nil
		})
	if err != nil {
		t.Fatalf("control ledger publication must succeed: %v", err)
	}
	return bundle
}
func showOf(t *testing.T, p store.Project, id model.ID) *ShowAnswer {
	t.Helper()
	return view_(t, p, ViewRequest{View: "show", ID: id}).(*ShowAnswer)
}
func historyOf(t *testing.T, p store.Project, id model.ID) *HistoryAnswer {
	t.Helper()
	return view_(t, p, ViewRequest{View: "history", ID: id}).(*HistoryAnswer)
}
func todoOf(t *testing.T, p store.Project) *TodoAnswer {
	t.Helper()
	return view_(t, p, ViewRequest{View: "todo"}).(*TodoAnswer)
}
func readyControl(t *testing.T, p store.Project) {
	t.Helper()
	appendEvents(t, p, 100, testTask(1))
	a := showOf(t, p, testID(1))
	if len(a.Records) != 1 || a.Records[0].Task.Status != reduce.StatusReady || a.Watermark.Sequence != 1 {
		t.Fatalf("control admitted task must read READY at sequence 1, got %+v", a)
	}
}
func capturePacket(t *testing.T, p store.Project, n int) model.PacketRef {
	t.Helper()
	event, err := model.EncodeEvent(testTask(n))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.WriteIntake(context.Background(), p, store.IntakeRequest{CommandID: testID(n + 1000),
		Author: model.Actor{ID: "author"}, Events: []model.Event{event}})
	if err != nil {
		t.Fatalf("control capture must preserve a valid packet: %v", err)
	}
	return ref
}
func reviewPacket(t *testing.T, p store.Project, n int, packet model.PacketRef, outcome string) {
	t.Helper()
	_, err := write.Admit(context.Background(), p, write.AdmitRequest{CommandID: testID(n), PacketIDs: []model.ID{packet.CommandID},
		Admitter: model.Actor{ID: "reviewer"}, Outcome: outcome, Reason: "reviewed for this test"})
	if err != nil {
		t.Fatalf("control %s disposition must be admitted: %v", outcome, err)
	}
}

// treeBytes fingerprints every file under root except the disposable
// snapshot cache's image and its temporaries: a read may refresh the cache,
// which changes no answer, and must write nothing else.
func treeBytes(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		cached := filepath.Base(filepath.Dir(path)) == "cache" && (name == "snapshot" || strings.HasPrefix(name, ".snapshot-"))
		if !entry.IsDir() && !cached {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[path] = string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
