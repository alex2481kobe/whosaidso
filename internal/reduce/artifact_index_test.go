package reduce

// The artifact-citation index only narrows which events are checked for citing
// a disposal's bytes; these tests hold it to the definition (citesDisposed)
// applied to every event, including the shapes where a shared pin must not
// count.

import (
	"reflect"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func citedSource(id string, order uint64, ref model.ArtifactRef) *model.SourceIntake {
	e := ownerSource(id, order)
	e.SourceRef = ref
	if ref.Content != nil {
		e.OriginalDigest, e.Length = ref.Content.SHA256, ref.Content.Length
	}
	return e
}

func withGit(ref model.ArtifactRef, pin model.GitPin) model.ArtifactRef {
	ref.Git = &pin
	return ref
}

func citationFixture(t *testing.T) (Snapshot, map[string]Origin, model.GitPin) {
	t.Helper()
	pin := model.GitPin{ObjectFormat: "sha1", Commit: "0123456789abcdef0123456789abcdef01234567", Path: "docs/plan.md"}
	l := goodLedger(t)
	gitOnly := model.ArtifactRef{Kind: "git", Git: &pin, Selector: model.Selector{Kind: "whole"}}
	events := map[string]*model.SourceIntake{
		"content-a":     citedSource("SRCA", 1, blobRef("a")),
		"git-only":      citedSource("SRCB", 2, gitOnly),
		"content-b+git": citedSource("SRCC", 3, withGit(blobRef("b"), pin)),
		"content-c+git": citedSource("SRCD", 4, withGit(blobRef("c"), pin)),
	}
	for _, name := range []string{"content-a", "git-only", "content-b+git", "content-c+git"} {
		l.add(t, events[name])
	}
	s := mustReplay(t, l.out)
	origins := map[string]Origin{}
	for _, o := range s.inner().log.all {
		if e, ok := s.inner().events[o].(*model.SourceIntake); ok {
			for name, want := range events {
				if e.SourceID == want.SourceID {
					origins[name] = o
				}
			}
		}
	}
	if len(origins) != len(events) {
		t.Fatalf("fixture events not found: %v", origins)
	}
	return s, origins, pin
}

func TestDisposalCitationsMatchTheDefinitionOverEveryEvent(t *testing.T) {
	s, at, pin := citationFixture(t)
	gitOnly := model.ArtifactRef{Kind: "git", Git: &pin, Selector: model.Selector{Kind: "whole"}}
	cases := []struct {
		name string
		e    model.ArtifactDispose
		want []Origin
	}{
		{"content pin", model.ArtifactDispose{Artifact: blobRef("a"), Digest: blobRef("a").Content.SHA256}, []Origin{at["content-a"]}},
		// A git pin matches every event carrying it when the disposal names no content.
		{"git pin alone", model.ArtifactDispose{Artifact: gitOnly, Digest: newDigest("unrelated")},
			[]Origin{at["git-only"], at["content-b+git"], at["content-c+git"]}},
		// Both sides carry content, so the git pin is corroboration only: the
		// event whose content differs does not cite the disposed bytes.
		{"content and git", model.ArtifactDispose{Artifact: withGit(blobRef("b"), pin), Digest: blobRef("b").Content.SHA256},
			[]Origin{at["git-only"], at["content-b+git"]}},
		// The digest alone cites any event whose content pin has it.
		{"digest alone", model.ArtifactDispose{Artifact: blobRef("never-cited"), Digest: blobRef("c").Content.SHA256}, []Origin{at["content-c+git"]}},
		{"uncited", model.ArtifactDispose{Artifact: blobRef("never-cited"), Digest: newDigest("never-cited")}, []Origin{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := []Origin{}
			for _, c := range s.DisposalCitations(tc.e) {
				got = append(got, c.Origin)
			}
			brute := []Origin{}
			for _, o := range s.inner().log.all {
				if citesDisposed(s.inner().events[o], &tc.e) {
					brute = append(brute, o)
				}
			}
			if !reflect.DeepEqual(got, brute) {
				t.Fatalf("index %v differs from the definition applied to every event %v", got, brute)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("cites %v, want %v", got, tc.want)
			}
		})
	}
}

// An index that was not carried through a snapshot image would answer from the
// events that came after it only.
func TestCitationIndexSurvivesAnImageAndAForkedApply(t *testing.T) {
	s, _, pin := citationFixture(t)
	gitOnly := model.ArtifactRef{Kind: "git", Git: &pin, Selector: model.Selector{Kind: "whole"}}
	e := model.ArtifactDispose{Artifact: gitOnly, Digest: newDigest("unrelated")}
	want := s.DisposalCitations(e)
	restored, err := RestoreSnapshot(mustEncode(t, s), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.DisposalCitations(e); !reflect.DeepEqual(got, want) || len(got) != 3 {
		t.Fatalf("restored image cites %v, want %v", got, want)
	}
	l := goodLedger(t)
	l.add(t, citedSource("SRCA", 1, blobRef("a")))
	base := mustReplay(t, l.out)
	l.add(t, citedSource("SRCB", 2, gitOnly))
	next, err := Apply(base, l.out[len(l.out)-1])
	if err != nil {
		t.Fatal(err)
	}
	if got := next.DisposalCitations(e); len(got) != 1 {
		t.Fatalf("applied bundle cites %v, want 1", got)
	}
	if got := base.DisposalCitations(e); len(got) != 0 {
		t.Fatalf("the snapshot Apply started from changed: %v", got)
	}
}
