package reduce

// Reverse references: they are exactly each event's walked references, and
// snapshot isolation holds for their amortized lists: applying to a
// snapshot, or further mutating what Apply returned, never changes the input
// snapshot or a sibling Apply result.

import (
	"fmt"
	"reflect"
	"testing"

	"datum/internal/model"
)

// citing asserts a new claim whose scope cites target, so the event lands in
// target's reverse-reference list.
func citing(id string, target model.RecordRef) *model.ClaimAssert {
	spec := claimSpec()
	spec.Scope.ContextRefs = []model.RecordRef{target}
	return &model.ClaimAssert{ID: newID(id), Provenance: provenance("lane-a"), Spec: spec}
}

func allReferrers(s Snapshot) map[model.RecordRef][]Referrer {
	out := map[model.RecordRef][]Referrer{}
	for _, r := range s.Records() {
		ref := asRef(r.Key)
		out[ref] = s.RecordReferrers(ref)
	}
	return out
}

func TestMutatingAnApplyResultNeverChangesItsInput(t *testing.T) {
	claim := ref(newID("CMA1"), 1)
	// Prior citations vary the list's length, so some base list has spare
	// capacity an unguarded append would write into.
	for prior := 0; prior < 6; prior++ {
		t.Run(fmt.Sprintf("prior%d", prior), func(t *testing.T) {
			l := proofLedger(t, true)
			for i := 0; i < prior; i++ {
				l.add(t, citing(fmt.Sprintf("PRA%d", i), claim))
			}
			base := mustReplay(t, l.out)
			baseBefore, rendered := allReferrers(base), render(base)

			one, two := *l, *l
			one.out = append([]model.Bundle(nil), l.out...)
			two.out = append([]model.Bundle(nil), l.out...)
			// The citations sit at different event indexes, so a shared slot
			// would show up as the wrong origin.
			r1, err := Apply(base, one.add(t, &model.ClaimAssert{ID: newID("FAA"), Provenance: provenance("lane-a"), Spec: claimSpec()}, citing("FAB", claim)))
			if err != nil {
				t.Fatal(err)
			}
			r2, err := Apply(base, two.add(t, citing("FBA", claim)))
			if err != nil {
				t.Fatal(err)
			}
			r2Before := allReferrers(r2)
			// Mutate the first result further, as a replay owning it would.
			more := one.add(t, citing("FAC", claim), citing("FAD", claim))
			if err := r1.st.apply(more); err != nil {
				t.Fatal(err)
			}

			if got := allReferrers(base); !reflect.DeepEqual(got, baseBefore) || render(base) != rendered {
				t.Fatalf("mutating Apply results changed the input snapshot's referrers:\n%v\nwant\n%v", got[claim], baseBefore[claim])
			}
			if got := allReferrers(r2); !reflect.DeepEqual(got, r2Before) {
				t.Fatalf("mutating one Apply result changed its sibling:\n%v\nwant\n%v", got[claim], r2Before[claim])
			}
			seq := uint64(len(l.out) + 1)
			want1 := []Origin{{Sequence: seq, EventIndex: 1}, {Sequence: seq + 1, EventIndex: 0}, {Sequence: seq + 1, EventIndex: 1}}
			want2 := []Origin{{Sequence: seq, EventIndex: 0}}
			for name, pair := range map[string]struct {
				got  []Referrer
				want []Origin
			}{"first": {r1.RecordReferrers(claim), want1}, "second": {r2.RecordReferrers(claim), want2}} {
				tail := pair.got[len(pair.got)-len(pair.want):]
				for i, o := range pair.want {
					if tail[i].Origin != o || tail[i].Path != "spec.scope.context_refs[0]" {
						t.Fatalf("%s result's own citations are %+v, want origins %v", name, tail, pair.want)
					}
				}
				if len(pair.got) != len(baseBefore[claim])+len(pair.want) {
					t.Fatalf("%s result has %d referrers, want %d", name, len(pair.got), len(baseBefore[claim])+len(pair.want))
				}
			}
		})
	}
}

// recordReferrers indexes the list checkReferences walked instead of walking
// again; the index must still be exactly every admitted event's same-project
// references, no more and no fewer.
func TestReferrersAreExactlyEachEventsWalkedReferences(t *testing.T) {
	s := mustReplay(t, lossLedger(t).bundles())
	type entry struct {
		origin Origin
		path   string
		target string
	}
	want := map[entry]int{}
	for o, e := range s.st.events {
		refs, err := model.SameProjectReferences(e, s.Project())
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range refs {
			var target any
			switch {
			case r.Record != nil:
				target = recordKey(*r.Record)
			case r.Criterion != nil:
				target = criterionKey(*r.Criterion)
			case r.Invocation != nil:
				target = invocationKey(*r.Invocation)
			case r.Blocker != nil:
				target = blockerKey(*r.Blocker)
			}
			want[entry{o, r.Path, fmt.Sprint(target)}]++
		}
	}
	got := map[entry]int{}
	collect := func(target any, refs []Referrer) {
		for _, r := range refs {
			if s.st.events[r.Origin].EventType() != r.Type {
				t.Fatalf("referrer %+v has the wrong event type", r)
			}
			got[entry{r.Origin, r.Path, fmt.Sprint(target)}]++
		}
	}
	for k, v := range s.st.reverseRecord {
		collect(k, v)
	}
	for k, v := range s.st.reverseCriterion {
		collect(k, v)
	}
	for k, v := range s.st.reverseInvocation {
		collect(k, v)
	}
	for k, v := range s.st.reverseBlocker {
		collect(k, v)
	}
	if len(want) == 0 || !reflect.DeepEqual(got, want) {
		t.Fatalf("reverse references differ from the walked references:\ngot  %v\nwant %v", got, want)
	}
}
