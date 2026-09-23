// This file measures batching and candidate application costs. It does not own
// synthetic history construction, general command benchmarks or production admission.
package benchmarks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
	"datum/internal/write"
)

// Each proof names a different three-run family already admitted to the ledger.
// Captures and baseline restoration are excluded. A one-admission batch makes
// repeated pending-intake verification visible without repeating prefix replay.
func BenchmarkProofBatch(b *testing.B) {
	f := getFixture(b, 1000)
	prefix, err := store.ReadPrefix(f.Project)
	must(b, err)
	for _, count := range []int{1, 3} {
		b.Run(fmt.Sprintf("Proofs%d", count), func(b *testing.B) {
			events := []model.TypedEvent{}
			for i := 0; i < count; i++ {
				event, err := model.DecodeEvent(prefix[i*10+7].Events[0])
				must(b, err)
				events = append(events, event)
			}
			packet := f.capture(nil, events...)
			defer removePacket(b, f, packet.CommandID)
			request := f.request(packet)
			restore := keepCache(b, f)
			b.ReportAllocs()
			b.ResetTimer()
			measured(b, "ProofBatch", func() {
				for i := 0; i < b.N; i++ {
					bundle, err := write.Admit(context.Background(), f.Project, request)
					must(b, err)
					if bundle.Sequence != 1001 {
						b.Fatal("batch did not append to the fixed prefix")
					}
					b.StopTimer()
					name, err := model.BundleName(bundle.Sequence, bundle.CommandID)
					must(b, err)
					must(b, os.Remove(filepath.Join(f.Project.Ledger, name)))
					restore()
					b.StartTimer()
				}
			})
		})
	}
}

func BenchmarkApply(b *testing.B) {
	for _, n := range []int{10, 1000, 10000} {
		b.Run(fmt.Sprintf("N%d", n), func(b *testing.B) {
			f := getFixture(b, n)
			prefix, err := store.ReadPrefix(f.Project)
			must(b, err)
			s, err := reduce.Replay(prefix)
			must(b, err)
			event, err := model.EncodeEvent(f.task())
			must(b, err)
			candidate := model.Bundle{Version: model.WireVersion, Project: f.Project.ID,
				Sequence: uint64(n + 1), Predecessor: s.Watermark().CommandID, CommandID: f.id(),
				RequestDigest: model.HashBytes([]byte("candidate application only")), Admitter: reviewer, RecordedAt: time.Now().UTC(), Events: []model.Event{event}}
			b.ReportAllocs()
			b.ResetTimer()
			measured(b, "Apply", func() {
				for i := 0; i < b.N; i++ {
					next, err := reduce.Apply(s, candidate)
					must(b, err)
					stageSink = next
				}
			})
		})
	}
}
