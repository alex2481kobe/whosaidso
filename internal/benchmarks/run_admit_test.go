// This file measures a real run followed by admission of its start and seal:
// the path that stages, captures and then publishes a run's outputs into the
// committed artifact store. Read pipelines and other writes live in commands_test.go.
package benchmarks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
	"datum/internal/write"
)

// Run with -run '^$' -bench BenchmarkRunAdmit -benchmem. Timed: the CLI's
// prefix load and instrument projection, the child run with both captures, and
// admission of both packets including output publication. Untimed: removing the
// bundle, the packets, the published outputs and any staging, so every
// iteration publishes into the same baseline store.
func BenchmarkRunAdmit(b *testing.B) {
	for _, n := range []int{10, 1000} {
		b.Run(fmt.Sprintf("N%d", n), func(b *testing.B) {
			f := getFixture(b, n)
			restore := keepCache(b, f)
			storeDir := filepath.Join(f.Project.Root, filepath.FromSlash(f.Project.ArtifactDir()))
			baseline := map[string]bool{}
			entries, err := os.ReadDir(storeDir)
			must(b, err)
			for _, e := range entries {
				baseline[e.Name()] = true
			}
			b.ReportAllocs()
			b.ResetTimer()
			measured(b, "RunAdmit", func() {
				for i := 0; i < b.N; i++ {
					project, err := store.Discover(f.Project.Root)
					must(b, err)
					loaded, err := store.Load(project)
					must(b, err)
					instrument, ok := loaded.Snapshot().Instrument(reduce.Ident{Project: project.ID, ID: f.Instrument.RecordID})
					if !ok {
						b.Fatal("fixture instrument missing")
					}
					result, err := write.Run(context.Background(), project, write.RunRequest{Author: author, AttemptID: f.Attempt, InstrumentRef: f.Instrument, Instrument: *instrument.Spec,
						CriterionRef: unknown[model.CriterionRef](), ExecutionSourceIdentity: write.RunExecutionIdentity(context.Background(), project), Argv: []string{"/usr/bin/printf", "{\"measured\":true}\n"}})
					must(b, err)
					bundle, err := write.Admit(context.Background(), project, write.AdmitRequest{CommandID: f.id(),
						PacketIDs: []model.ID{result.StartPacket.CommandID, result.SealPacket.CommandID},
						Admitter:  reviewer, Outcome: "accepted", Reason: "reviewed the run and its stated limitations"})
					must(b, err)
					if bundle.Sequence != uint64(f.Bundles+1) {
						b.Fatal("admission measured a retry or changed baseline")
					}
					b.StopTimer()
					name, err := model.BundleName(bundle.Sequence, bundle.CommandID)
					must(b, err)
					must(b, os.Remove(filepath.Join(f.Project.Ledger, name)))
					restore()
					removePacket(b, f, result.StartPacket.CommandID)
					removePacket(b, f, result.SealPacket.CommandID)
					if result.ArtifactDir != "" {
						must(b, os.RemoveAll(result.ArtifactDir))
					}
					entries, err := os.ReadDir(storeDir)
					must(b, err)
					for _, e := range entries {
						if !baseline[e.Name()] {
							must(b, os.RemoveAll(filepath.Join(storeDir, e.Name())))
						}
					}
					b.StartTimer()
				}
			})
		})
	}
}
