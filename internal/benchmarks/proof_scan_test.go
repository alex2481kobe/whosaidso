// This file measures proof admission against retained intake at 1,000 and
// 10,000 bundles, with one and three distinct proofs in one admission, so the
// pending-intake scan is visible per admission rather than per proof. Fixture
// construction and other command benchmarks live elsewhere.
package benchmarks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"datum/internal/model"
	"datum/internal/store"
	"datum/internal/write"
)

// Each proof re-admits a different three-run family's proof already in the
// ledger; the fixture's bundle i*10+7 is cycle i's proof. Capture and the
// baseline restoration after each admission are excluded from the timing.
// Run with -benchtime=1x at 10,000 bundles.
func BenchmarkProofScan(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("N%d", n), func(b *testing.B) {
			f := getFixture(b, n)
			prefix, err := store.ReadPrefix(f.Project)
			must(b, err)
			for _, count := range []int{1, 3} {
				b.Run(fmt.Sprintf("Proofs%d", count), func(b *testing.B) {
					events := []model.TypedEvent{}
					for i := 0; i < count; i++ {
						event, err := model.DecodeEvent(prefix[i*10+7].Events[0])
						must(b, err)
						if _, ok := event.(*model.ProofAdmit); !ok {
							b.Fatalf("bundle %d is not a proof", i*10+7)
						}
						events = append(events, event)
					}
					packet := f.capture(nil, events...)
					defer removePacket(b, f, packet.CommandID)
					request := f.request(packet)
					b.ReportAllocs()
					b.ResetTimer()
					measured(b, "ProofScan", func() {
						for i := 0; i < b.N; i++ {
							bundle, err := write.Admit(context.Background(), f.Project, request)
							must(b, err)
							if bundle.Sequence != uint64(n+1) {
								b.Fatal("admission measured a retry or changed baseline")
							}
							b.StopTimer()
							name, err := model.BundleName(bundle.Sequence, bundle.CommandID)
							must(b, err)
							must(b, os.Remove(filepath.Join(f.Project.Ledger, name)))
							b.StartTimer()
						}
					})
				})
			}
		})
	}
}
