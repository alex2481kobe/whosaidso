package write

// The artifact-store completeness rule lives here for home binding and ledger
// checks. Admission materialization and ledger comparison do not.

import (
	"context"
	"fmt"
	"github.com/alex2481kobe/whosaidso/internal/evidence"
	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
	"slices"
	"strings"
)

// HomeHoldsKeptArtifacts refuses a home whose artifact store does not hold
// every artifact its admitted ledger keeps there. Admission keeps the verified
// bytes of every content artifact an admitted event cites (materializeAdmission),
// so each must resolve from the store alone, locators set aside. Two exceptions,
// both from the ledger itself: a git artifact resolves from the repository, and
// a recorded disposal says those bytes may be gone. Each refusal names the digest,
// every admitted event citing it (bundle, event index, type) and the resolver's
// reason, which tells missing bytes from different ones.
func HomeHoldsKeptArtifacts(ctx context.Context, home store.Project, state store.State) error {
	problems, err := keptArtifactProblems(ctx, home, state)
	if err != nil {
		return err
	}
	return keptArtifactsError(home, problems)
}

type keptArtifactProblem struct {
	digest model.Digest
	citers []string
	reason string
}

// keptArtifactProblems measures one problem per content digest, preserving
// every citer and the original resolver reason for the existing diagnostic.
func keptArtifactProblems(ctx context.Context, home store.Project, state store.State) ([]keptArtifactProblem, error) {
	bundles, err := state.Bundles()
	if err != nil {
		return nil, err
	}
	snapshot := state.Snapshot()
	resolver := evidence.NewResolverAt(home.Root, home.ArtifactDir())
	reasons := map[model.Digest]string{}
	citers := map[model.Digest][]string{}
	var missing []model.Digest
	for _, bundle := range bundles {
		for i, raw := range bundle.Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				return nil, err
			}
			citer := fmt.Sprintf("bundle %d event %d (%s)", bundle.Sequence, i, raw.Type)
			for _, ref := range admissionArtifacts(event) {
				if ref.Kind != "content" || disposed(snapshot, ref) {
					continue
				}
				digest := ref.Content.SHA256
				if _, checked := reasons[digest]; !checked {
					kept := model.ArtifactRef{Kind: "content", Selector: model.Selector{Kind: "whole"}, Content: &model.ContentPin{
						SHA256: digest, Length: ref.Content.Length, MediaType: ref.Content.MediaType, Locators: []model.Locator{}}}
					reasons[digest] = ""
					if _, err := resolver.Resolve(ctx, kept); err != nil {
						reasons[digest] = err.Error()
						missing = append(missing, digest)
					}
				}
				if reasons[digest] != "" && !slices.Contains(citers[digest], citer) {
					citers[digest] = append(citers[digest], citer)
				}
			}
		}
	}
	problems := make([]keptArtifactProblem, 0, len(missing))
	for _, digest := range missing {
		problems = append(problems, keptArtifactProblem{digest, citers[digest], reasons[digest]})
	}
	return problems, nil
}

func keptArtifactsError(home store.Project, problems []keptArtifactProblem) error {
	if len(problems) == 0 {
		return nil
	}
	lines := make([]string, len(problems))
	for i, p := range problems {
		lines[i] = fmt.Sprintf("sha256 %s cited by %s: %s", p.digest, strings.Join(p.citers, ", "), p.reason)
	}
	return admissionFault("home-evidence-missing", home.ArtifactDir(), fmt.Sprintf(
		"%s does not hold %d artifact(s) its admitted records cite; commit the whole .whosaidso folder (events and artifacts) together:\n  %s",
		home.Root, len(problems), strings.Join(lines, "\n  ")))
}
