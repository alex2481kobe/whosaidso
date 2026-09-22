package query

// Tests for the disposal-loss read at the query seam: a git-pinned citation
// is planned only for the same git pin, a content-pinned one by digest, and
// the artifact identity belongs to disposal-loss alone. The fresh-process
// property (printed list admitted, any omission refused) is in cmd/datum.

import (
	"strings"
	"testing"

	"datum/internal/model"
)

func TestDisposalLossPlansGitPinnedCitationsOnlyForTheSamePin(t *testing.T) {
	p := testProject(t)
	pin := &model.GitPin{ObjectFormat: "sha1", Commit: strings.Repeat("a", 40), Path: "docs/source.md"}
	git := model.ArtifactRef{Kind: "git", Git: pin, Selector: model.Selector{Kind: "whole"}}
	byGit, byContent := testTask(1), testTask(2)
	byGit.Provenance.SourceRefs = []model.ArtifactRef{git}
	byContent.Provenance.SourceRefs = []model.ArtifactRef{testArtifact()}
	appendEvents(t, p, 100, byGit, byContent)
	digest := testArtifact().Content.SHA256
	plan := func(target DisposalTarget) *DisposalLoss {
		t.Helper()
		a, err := Read(p, Request{Command: "disposal-loss", Disposal: &target})
		if err != nil || a.Preset == nil || a.Preset.Disposal == nil || a.Watermark.Sequence != 1 {
			t.Fatalf("control disposal-loss read must answer at sequence 1: %+v %v", a, err)
		}
		return a.Preset.Disposal
	}
	content := plan(DisposalTarget{Digest: digest})
	if len(content.SupportLoss) != 1 || content.SupportLoss[0] != testRef(2, 1) || len(content.CitedBy) != 1 {
		t.Fatalf("a content disposal must lose exactly the content citer: %+v", content)
	}
	withPin := plan(DisposalTarget{Digest: digest, Git: pin})
	if len(withPin.SupportLoss) != 2 || len(withPin.CitedBy) != 2 {
		t.Fatalf("a disposal naming the git pin must also lose the git citer: %+v", withPin)
	}
	other := *pin
	other.Path = "docs/other.md"
	if got := plan(DisposalTarget{Digest: model.HashBytes([]byte("unrelated")), Git: &other}); len(got.SupportLoss) != 0 || len(got.CitedBy) != 0 {
		t.Fatalf("a different pin and digest must lose nothing: %+v", got)
	}
}

func TestDisposalLossIdentityBelongsOnlyToDisposalLoss(t *testing.T) {
	p := testProject(t)
	readyControl(t, p)
	good := &DisposalTarget{Digest: testArtifact().Content.SHA256}
	for want, request := range map[string]Request{
		"requires one":   {Command: "disposal-loss"},
		"belongs to":     {Command: "state", Disposal: good},
		"digest must be": {Command: "disposal-loss", Disposal: &DisposalTarget{Digest: "abc"}},
		"object format":  {Command: "disposal-loss", Disposal: &DisposalTarget{Digest: good.Digest, Git: &model.GitPin{ObjectFormat: "md5", Commit: "a", Path: "x"}}},
	} {
		if _, err := Read(p, request); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%+v must be refused with %q, got %v", request, want, err)
		}
	}
}
