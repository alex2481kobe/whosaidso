package reduce

// An act citing the owner's words stands while the words do
// (authority_words.go): across a merge that sets the words aside, the act is
// set aside with them; on one line, or across a merge that keeps the words,
// it applies. The receipt citation is attacked end to end in
// internal/acceptance/multiwriter_review_test.go.

import (
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestAnActCitingSetAsideWordsIsSetAsideWithThem(t *testing.T) {
	words := func(order uint64) *model.SourceIntake {
		const blob = "owner-ruling" // what rulingAuthority cites
		return &model.SourceIntake{SourceID: newID("SRCW"), OriginalDigest: newDigest(blob), Length: uint64(len(blob)),
			Speaker: model.Actor{ID: "owner"}, Order: order, SourceRef: blobRef(blob), Referents: []model.RecordRef{}}
	}
	auth := rulingAuthority("owner")
	acts := map[string]func(l *ledgerBuilder){
		"a waiver": func(l *ledgerBuilder) {
			l.add(t, consumer(withPrerequisite("task-success", ref(newID("TSKA"), 1), "allow-with-authority", &auth)))
		},
		"a closure": func(l *ledgerBuilder) {
			l.add(t, consumer())
			l.add(t, closeWith(model.ClosureCancelled))
		},
	}
	for name, act := range acts {
		t.Run(name, func(t *testing.T) {
			line := goodLedger(t)
			line.add(t, words(7))
			act(line)
			if got := mustReplay(t, line.bundles()).SetAside(); len(got) != 0 {
				t.Fatalf("control: on one line the act on the words applies: %+v", got)
			}
			merge := func(other uint64) Snapshot {
				base := goodLedger(t)
				a, b := base.branch(100), base.branch(200)
				a.add(t, ownerSource("SRCA", other))
				b.add(t, words(7))
				act(b)
				return mustReplay(t, merged(a, b))
			}
			if got := merge(8).SetAside(); len(got) != 0 {
				t.Fatalf("control: a merge that keeps the words keeps the act: %+v", got)
			}
			base := goodLedger(t)
			a, b := base.branch(100), base.branch(200)
			a.add(t, ownerSource("SRCA", 8))
			b.add(t, &model.TaskCreate{Provenance: provenance("agent-b"), ID: newID("TSKB"), Spec: taskSpec()})
			b.add(t, closeWith(model.ClosureCancelled), words(7))
			if got := mustReplay(t, merged(a, b)).SetAside(); len(got) != 0 {
				t.Fatalf("control: words carried later in the act's own bundle count: %+v", got)
			}
			got := merge(7).SetAside()
			codes := []string{}
			for _, v := range got {
				codes = append(codes, v.Code)
			}
			if len(codes) != 2 || codes[0] != CodeDuplicateSourceOrder || codes[1] != CodeAuthorityUnavailable {
				t.Fatalf("the words set aside take the act citing them along, got %v", codes)
			}
		})
	}
}
