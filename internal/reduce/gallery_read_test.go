// Single-entry gallery reads bound copying and preserve snapshot isolation.
// These synthetic ledgers contain no machine files or local availability.
package reduce

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestGalleryEntryReadCopiesOnlyItsOwnPayload(t *testing.T) {
	allocations := func(extra int) float64 {
		l, add, req := galleryLedger(t)
		wanted := *add
		wanted.EntryID = newID("READ")
		wanted.Tasks = []model.RecordRef{req.Task}
		galleryFact(t, l, wanted.Actor, &wanted)
		for i := 0; i < extra; i++ {
			other := *add
			other.EntryID = newID(fmt.Sprintf("G%d", i))
			galleryFact(t, l, other.Actor, &other)
		}
		s := mustReplay(t, l.bundles())
		var entry GalleryEntry
		count := testing.AllocsPerRun(3, func() {
			var ok bool
			entry, ok = s.GalleryEntry(wanted.EntryID)
			if !ok {
				t.Fatal("entry missing")
			}
		})
		if !reflect.DeepEqual(entry.Add, wanted) {
			t.Fatal("selected payload changed")
		}
		entry.Add.Tasks[0].RecordID = newID("BAD")
		again, ok := s.GalleryEntry(wanted.EntryID)
		if !ok || again.Add.Tasks[0] != req.Task {
			t.Fatal("entry exposed mutable snapshot data")
		}
		if _, ok := s.GalleryEntry(newID("ABSENT")); ok {
			t.Fatal("unknown entry matched")
		}
		return count
	}
	small, large := allocations(0), allocations(80)
	if large > small*1.1+2 {
		t.Fatalf("single-entry copying grew with unrelated gallery payloads: %.0f -> %.0f allocations", small, large)
	}
}

func TestGalleryForksKeepIndependentVerdictsAndRetractions(t *testing.T) {
	for prior := 0; prior < 6; prior++ {
		l, add, _ := galleryLedger(t)
		for i := 0; i < prior; i++ {
			other := *add
			other.EntryID = newID(fmt.Sprintf("P%d", i))
			galleryFact(t, l, other.Actor, &other)
		}
		base := mustReplay(t, l.bundles())
		one, two := *l, *l
		one.out = append([]model.Bundle(nil), l.out...)
		two.out = append([]model.Bundle(nil), l.out...)
		by := model.Actor{ID: "reviewer"}
		approved := galleryFact(t, &one, by, &model.GalleryApprove{EntryID: add.EntryID, SHA256: add.File.SHA256, Actor: by})
		retracted := galleryFact(t, &two, by, &model.ClaimAssert{ID: newID("C999"), Provenance: provenance("contributor"), Spec: claimSpec()}, &model.GalleryRetract{EntryID: add.EntryID})
		first, err := Apply(base, approved)
		if err != nil {
			t.Fatal(err)
		}
		second, err := Apply(base, retracted)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := first.GalleryEntry(add.EntryID)
		b, _ := second.GalleryEntry(add.EntryID)
		original, _ := base.GalleryEntry(add.EntryID)
		if a.Verdict != "approved" || a.Retracted || !b.Retracted || b.Verdict != "unreviewed" || original.Retracted || original.Verdict != "unreviewed" {
			t.Fatalf("gallery forks share transitions: %+v %+v %+v", a, b, original)
		}
		data, err := EncodeSnapshot(first)
		if err != nil {
			t.Fatal(err)
		}
		restored, err := RestoreSnapshot(data, nil)
		if err != nil {
			t.Fatal(err)
		}
		result, ok := restored.GalleryEntry(add.EntryID)
		if !ok || !reflect.DeepEqual(a, result) {
			t.Fatal("snapshot restore lost a gallery transition")
		}
	}
}

// Single-item work must depend on gallery events, rather than every task and
// record. Relative timings leave wide room for host load without hiding a scan.
func TestGalleryEntryReadCostDoesNotGrowWithUnrelatedRecords(t *testing.T) {
	cost := func(extra int) int64 {
		l, add, _ := galleryLedger(t)
		events := make([]model.TypedEvent, extra)
		for i := range events {
			events[i] = &model.ClaimAssert{ID: newID(fmt.Sprintf("CM%d", i)), Provenance: provenance("agent-a"), Spec: claimSpec()}
		}
		if len(events) > 0 {
			l.add(t, events...)
		}
		s := mustReplay(t, l.bundles())
		result := testing.Benchmark(func(b *testing.B) {
			for range b.N {
				if _, ok := s.GalleryEntry(add.EntryID); !ok {
					b.Fatal("missing gallery entry")
				}
			}
		})
		return result.NsPerOp()
	}
	small, large := cost(0), cost(10000)
	t.Logf("single-entry ns/op: %d without unrelated records, %d with 10000", small, large)
	if large > small*20 {
		t.Fatalf("gallery lookup scans unrelated records: %d -> %d ns/op", small, large)
	}
}
