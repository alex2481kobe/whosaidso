// A missing local partial-clone blob makes that pin unknown while preserving
// the ledger check's other measured results. No fixture contacts a network.
package write

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func TestLedgerCheckKeepsMeasurementsWhenAPartialCloneBlobIsMissing(t *testing.T) {
	f := ledgerFixture(t)
	root := f.project.Root
	t.Log(identityGit(t, root, "--version"))
	proofPut(t, root, "body.txt", "pinned bytes")
	identityGit(t, root, "add", "body.txt")
	identityGit(t, root, "commit", "-q", "-m", "content")
	head := identityGit(t, root, "rev-parse", "HEAD")
	ref := model.ArtifactRef{Kind: "git", Git: &model.GitPin{ObjectFormat: "sha1", Commit: head, Path: "body.txt"}, Selector: model.Selector{Kind: "whole"}}
	f.accept(f.capture([][]byte{[]byte("pinned bytes")}, &model.SourceIntake{SourceID: f.id(), SourceRef: ref, OriginalDigest: model.HashBytes([]byte("pinned bytes")), Length: 12, Speaker: model.Actor{ID: "contributor"}, Order: 1, Referents: []model.RecordRef{}}))
	identityGit(t, root, "add", ".whosaidso/events")
	identityGit(t, root, "commit", "-q", "-m", "ledger")
	control, err := CheckLedger(context.Background(), f.project)
	if err != nil || !control.Clean {
		t.Fatalf("complete control: %+v %v", control, err)
	}
	identityGit(t, root, "config", "uploadpack.allowFilter", "true")
	clone := filepath.Join(t.TempDir(), "partial")
	sourcePath := filepath.ToSlash(root)
	if !strings.HasPrefix(sourcePath, "/") {
		sourcePath = "/" + sourcePath
	}
	identityGit(t, root, "clone", "--quiet", "--filter=blob:none", "--no-checkout", (&url.URL{Scheme: "file", Path: sourcePath}).String(), clone)
	// Restore only the fixture's ledger and config; the pinned blob stays absent.
	for _, dir := range []string{".", ".whosaidso/events"} {
		files, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if file.IsDir() || (dir == "." && file.Name() != "whosaidso.toml") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(root, dir, file.Name()))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(clone, dir, file.Name())
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	p, err := store.Discover(clone)
	if err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(t.TempDir(), "trace.jsonl")
	t.Setenv("GIT_TRACE2_EVENT", trace)
	answer, err := CheckLedger(context.Background(), p)
	if err != nil || answer == nil {
		t.Fatalf("missing pin discarded ledger answer: %+v %v", answer, err)
	}
	if answer.Clean || len(answer.GitPins) != 1 || !strings.Contains(answer.GitPins[0], "UNKNOWN") || answer.Classes["not_committed"].State != "measured" || answer.Classes["replay_error"].State != "measured" || answer.Classes["artifacts_missing"].State != "measured" {
		t.Fatalf("missing pin must not erase other measurements: %+v", answer)
	}
	b, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"fetch"`) {
		t.Fatal("ledger check attempted a lazy fetch")
	}
}
