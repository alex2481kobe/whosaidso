package reduce

// Events are decoded ahead of the fold on several goroutines (prepare.go).
// That must change neither the state a ledger folds to nor which refusal a bad
// ledger gets, including across the chunks the preparation works in.

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func foldOneAtATime(bundles []model.Bundle) (Snapshot, error) {
	s := Snapshot{}
	for _, b := range bundles {
		next, err := Apply(s, b)
		if err != nil {
			return Snapshot{}, err
		}
		s = next
	}
	return s, nil
}

func TestReplayFoldsToWhatOneBundleAtATimeFoldsTo(t *testing.T) {
	for name, bundles := range restoreFixtures(t) {
		want, err := foldOneAtATime(bundles)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := mustReplay(t, bundles); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: Replay differs from folding one bundle at a time", name)
		}
	}
}

func longLedger(t *testing.T, bundles int) *ledgerBuilder {
	t.Helper()
	l := goodLedger(t)
	for i := 0; i < bundles; i++ {
		l.add(t, createTask(fmt.Sprintf("TB%d", i)))
	}
	return l
}

func TestReplayRefusesAtTheSameBundleWithTheSameFaultAsAnySequentialFold(t *testing.T) {
	const length = 700 // several preparation chunks
	for _, at := range []int{3, replayChunk - 1, replayChunk, replayChunk + 1, 2*replayChunk + 5, length} {
		for _, poison := range []string{"unknown event type", "unknown reference", "two bad bundles"} {
			l := longLedger(t, length)
			switch poison {
			case "unknown event type":
				l.out[at].Events[0].Type = "task.bogus"
			case "unknown reference":
				raw, err := model.EncodeEvent(amendTask("ZZZZ", 1, "amends nothing"))
				if err != nil {
					t.Fatal(err)
				}
				l.out[at].Events = []model.Event{raw}
			case "two bad bundles":
				l.out[at].Events[0].Type = "task.bogus"
				l.out[len(l.out)-1].Events[0].Type = "task.other"
			}
			_, want := foldOneAtATime(l.out)
			_, got := Replay(l.out)
			if want == nil || got == nil || got.Error() != want.Error() {
				t.Fatalf("%s at bundle %d: Replay says %v, a sequential fold says %v", poison, at, got, want)
			}
		}
	}
}
