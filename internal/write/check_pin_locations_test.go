package write

// Unreachable pins report only local branch and remote-tracking containment,
// once per offending commit, while remaining problems.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestLedgerPinLocationsShareOneLocalQueryPerCommit(t *testing.T) {
	f := ledgerFixture(t)
	root := f.project.Root
	main := identityGit(t, root, "branch", "--show-current")
	identityGit(t, root, "switch", "-q", "-c", "side")
	for _, path := range []string{"a.txt", "b.txt"} {
		proofPut(t, root, path, path)
	}
	identityGit(t, root, "add", "a.txt", "b.txt")
	identityGit(t, root, "commit", "-q", "-m", "side")
	side := identityGit(t, root, "rev-parse", "HEAD")
	identityGit(t, root, "branch", "side-alias")
	identityGit(t, root, "update-ref", "refs/remotes/origin/side", side)
	identityGit(t, root, "tag", "side-tag")
	identityGit(t, root, "switch", "-q", main)
	for order, path := range []string{"a.txt", "b.txt"} {
		ref := model.ArtifactRef{Kind: "git", Git: &model.GitPin{ObjectFormat: "sha1", Commit: side, Path: path}, Selector: model.Selector{Kind: "whole"}}
		f.accept(f.capture([][]byte{[]byte(path)}, &model.SourceIntake{SourceID: f.id(), SourceRef: ref, OriginalDigest: model.HashBytes([]byte(path)),
			Length: uint64(len(path)), Speaker: model.Actor{ID: "owner"}, Order: uint64(order), Referents: []model.RecordRef{}}))
	}
	identityGit(t, root, "add", ".whosaidso/events")
	identityGit(t, root, "commit", "-q", "-m", "cites side")
	trace := filepath.Join(t.TempDir(), "trace.jsonl")
	t.Setenv("GIT_TRACE2_EVENT", trace)
	before := identityGit(t, root, "status", "--porcelain")
	a := ledgerAnswer(t, f)
	pins := a["git_pins"].([]any)
	if c := a["classes"].(map[string]any)["git_pins"].(map[string]any); c["count"] != float64(2) {
		t.Fatalf("two pin identities: %v", c)
	}
	if a["clean"] != false || len(pins) != 2 {
		t.Fatalf("pins must remain problems: %v", a)
	}
	locations, ok := a["git_pin_locations"].([]any)
	if !ok || len(locations) != 1 {
		t.Fatalf("one location result per commit: %v", a)
	}
	location := locations[0].(map[string]any)
	want := []any{"refs/heads/side", "refs/heads/side-alias", "refs/remotes/origin/side"}
	if location["commit"] != side || !reflect.DeepEqual(location["refs"], want) {
		t.Fatalf("local and remote-tracking branches only: %v", location)
	}
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	queries, ancestry := 0, 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e struct {
			Event string
			Argv  []string
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		if e.Event != "start" {
			continue
		}
		args := strings.Join(e.Argv, " ")
		if strings.Contains(args, "for-each-ref") {
			queries++
		}
		if strings.Contains(args, "merge-base --is-ancestor") {
			ancestry++
		}
	}
	if queries != 1 || ancestry != 1 {
		t.Fatalf("two paths at one commit: ref queries=%d ancestry=%d, want one each", queries, ancestry)
	}
	// Reading locations must not consult a home or leave any Git changes.
	if out := identityGit(t, root, "status", "--porcelain"); out != before {
		t.Fatalf("check wrote checkout: %s", out)
	}
	_, err = CheckLedger(context.Background(), f.project)
	if err != nil {
		t.Fatal(err)
	}
}
