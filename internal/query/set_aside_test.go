package query

// Tests that a bundle set aside after a merge is never hidden: todo counts and
// lists it with its fault and what to do, history marks its events, and a
// ledger that never clashed answers without any of it.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func amendIntent(n int, revision model.Revision, intent string) *model.TaskAmend {
	spec := testTask(n).Spec
	spec.Intent = intent
	return &model.TaskAmend{Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}}, Target: testRef(n, revision), Replacement: spec}
}

// publishOtherBranch writes the bundle another branch admitted after genesis,
// as a merge leaves it in the folder.
func publishOtherBranch(t *testing.T, p store.Project, genesis model.Bundle, command model.ID, events ...model.TypedEvent) model.Bundle {
	t.Helper()
	raw := []model.Event{}
	for _, e := range events {
		enc, err := model.EncodeEvent(e)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, enc)
	}
	b := model.Bundle{Version: model.WireVersion, Project: p.ID, Sequence: 2, CommandID: command, Predecessor: genesis.CommandID,
		RequestDigest: model.HashBytes([]byte(command)), Admitter: model.Actor{ID: "reviewer"}, RecordedAt: genesis.RecordedAt,
		Packets: []model.PacketRef{}, Events: raw}
	data, err := model.Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	name, err := model.BundleName(b.Sequence, b.CommandID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Ledger, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return b
}

var reRecordWords = regexp.MustCompile(`(?i)re-?record`)

func TestSetAsideBundlesAreCountedListedAndMarked(t *testing.T) {
	p := testProject(t)
	genesis := appendEvents(t, p, 100, testTask(1))
	for _, view := range []string{"todo", "history"} {
		if linear := jsonOf(t, p, ViewRequest{View: view}); bytes.Contains(linear, []byte("set_aside")) {
			t.Fatalf("a ledger that never clashed must answer %s exactly as before: %s", view, linear)
		}
	}
	appendEvents(t, p, 101, amendIntent(1, 1, "amended on this branch"))
	other := publishOtherBranch(t, p, genesis, testID(900), amendIntent(1, 1, "amended on the other branch"))

	todo := todoOf(t, p)
	if todo.Totals.SetAside != 1 || len(todo.SetAside) != 1 {
		t.Fatalf("todo must count and list the set-aside bundle: %+v %+v", todo.Totals, todo.SetAside)
	}
	got := todo.SetAside[0]
	if got.CommandID != other.CommandID || got.Sequence != 2 || got.Code != "revision-conflict" || got.Reason == "" || !reRecordWords.MatchString(got.Next) {
		t.Fatalf("set-aside entry must name the bundle, its fault and how to re-record: %+v", got)
	}
	text := briefOf(t, p, ViewRequest{View: "todo"})
	for _, want := range []string{"set aside", string(other.CommandID), "revision-conflict"} {
		if !strings.Contains(text, want) {
			t.Errorf("todo text must show %q:\n%s", want, text)
		}
	}
	if !reRecordWords.MatchString(text) {
		t.Errorf("todo text must say to re-record:\n%s", text)
	}

	history := viewAnswerOf(t, p, ViewRequest{View: "history"}).(*HistoryAnswer)
	marked := 0
	for _, e := range history.Events {
		if (e.SetAside != nil) != (e.CommandID == other.CommandID) {
			t.Fatalf("exactly the set-aside bundle's events are marked: %+v", e)
		}
		if e.SetAside != nil {
			marked++
		}
	}
	if marked != 1 {
		t.Fatalf("history lists the set-aside event once, marked; got %d", marked)
	}
	if text := briefOf(t, p, ViewRequest{View: "history"}); !strings.Contains(text, "SET ASIDE") {
		t.Fatalf("history text must mark it:\n%s", text)
	}
	if show := showOf(t, p, testID(1)); show.Records[0].Ref.Revision != 2 || show.Records[0].Task == nil {
		t.Fatalf("the applied branch's revision stands: %+v", show.Records[0].Ref)
	}
}

func jsonOf(t *testing.T, p store.Project, r ViewRequest) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := RenderViewJSON(&out, viewAnswerOf(t, p, r)); err != nil {
		t.Fatal(err)
	}
	var check map[string]any
	if err := json.Unmarshal(out.Bytes(), &check); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func briefOf(t *testing.T, p store.Project, r ViewRequest) string {
	t.Helper()
	var out bytes.Buffer
	if err := RenderViewBrief(&out, viewAnswerOf(t, p, r)); err != nil {
		t.Fatal(err)
	}
	return out.String()
}
