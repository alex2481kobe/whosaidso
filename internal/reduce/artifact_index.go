package reduce

// The artifact-citation index lives here: which admitted events cite which
// artifact pins, kept as events are applied so a disposal does not have to
// walk every event's payload to find its citers. How a citation counts as
// citing the disposed bytes (citesDisposed) and what a loss reaches
// (support_graph.go) are not decided here.

import (
	"reflect"
	"sort"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// citeKey names one artifact pin: a content pin by its digest, a git pin by
// its three parts. Kind keeps the two namespaces from aliasing.
type citeKey struct {
	Kind, A, B, C string
}

func contentCite(digest model.Digest) citeKey { return citeKey{Kind: "content", A: string(digest)} }

func gitCite(g model.GitPin) citeKey {
	return citeKey{Kind: "git", A: g.ObjectFormat, B: g.Commit, C: g.Path}
}

// indexCitations records, under every pin the event's payload carries, that
// the event cites it. The index only narrows candidates: citesDisposed stays
// the one definition of a citation.
func (s *state) indexCitations(o Origin, e model.TypedEvent) {
	var refs []model.ArtifactRef
	artifactRefs(reflect.ValueOf(e), &refs)
	r := Referrer{Origin: o, Type: e.EventType()}
	add := func(k citeKey) {
		if list := s.reverseArtifact[k]; len(list) > 0 && list[len(list)-1].Origin == o {
			return // the same event cites this pin twice
		}
		addReferrer(s, s.reverseArtifact, k, r)
	}
	for _, ref := range refs {
		if ref.Content != nil {
			add(contentCite(ref.Content.SHA256))
		}
		if ref.Git != nil {
			add(gitCite(*ref.Git))
		}
	}
}

// citingEvents lists, in ledger order, every admitted event that cites the
// disposal's bytes. Candidates come from the index; each is confirmed by
// citesDisposed, so a pin shared only as corroboration cannot widen the answer.
func (s *state) citingEvents(e *model.ArtifactDispose) []Origin {
	keys := []citeKey{contentCite(e.Digest)}
	if e.Artifact.Content != nil {
		keys = append(keys, contentCite(e.Artifact.Content.SHA256))
	}
	if e.Artifact.Git != nil {
		keys = append(keys, gitCite(*e.Artifact.Git))
	}
	seen := map[Origin]bool{}
	out := []Origin{}
	for _, k := range keys {
		for _, ref := range s.reverseArtifact[k] {
			if seen[ref.Origin] {
				continue
			}
			seen[ref.Origin] = true
			if citesDisposed(s.events[ref.Origin], e) {
				out = append(out, ref.Origin)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].before(out[j]) })
	return out
}
