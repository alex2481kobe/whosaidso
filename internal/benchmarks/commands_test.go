// This file measures the public command pipelines, including JSON rendering and
// real durable writes. Fixture construction, profiling stages and assertions do not belong here.
package benchmarks

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/query"
	"datum/internal/reduce"
	"datum/internal/store"
	"datum/internal/write"
)

type readCase struct {
	name    string
	request query.Request
}

func readCases(f *fixture) []readCase {
	return []readCase{
		{"Show", query.Request{Command: "show"}},
		{"ShowOne", query.Request{Command: "show", ID: f.Task.RecordID}},
		{"HistoryOne", query.Request{Command: "history", ID: f.Task.RecordID}},
		{"Todo", query.Request{Command: "todo"}},
		{"Now", query.Request{Command: "now"}},
		{"Context", query.Request{Command: "context"}},
		{"ContextOne", query.Request{Command: "context", ID: f.Task.RecordID}},
		{"Continue", query.Request{Command: "continue", ID: f.Task.RecordID}},
		{"Instruments", query.Request{Command: "instruments"}},
	}
}

type byteCounter int64

func (c *byteCounter) Write(p []byte) (int, error) { *c += byteCounter(len(p)); return len(p), nil }

// Observations mirror cmd/datum/read.go, including fresh subprocesses. Fixture
// roots are outside git: HEAD/dirty remain UNKNOWN, with the observed reasons.
func observe(root string) query.Observation {
	git := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"--no-optional-locks", "-C", root}, args...)...).Output()
		return strings.TrimSpace(string(out)), err
	}
	o := query.Observation{ObservedAt: known(time.Now().UTC()), Head: unknown[model.GitHead](), Dirty: unknown[bool]()}
	head, err := git("rev-parse", "--verify", "HEAD")
	format, formatErr := git("rev-parse", "--show-object-format")
	if err == nil && formatErr == nil {
		o.Head = known(model.GitHead{ObjectFormat: format, Commit: head})
	}
	if status, err := git("status", "--porcelain"); err == nil {
		o.Dirty = known(status != "")
	}
	return o
}

// Profiles contain fixture setup too. Filter them with
// go tool pprof -tagfocus=phase=operation -top -cum <profile>.
// Timings/allocations already exclude setup, cleanup and the text-size measurement.
func measured(b *testing.B, name string, fn func()) {
	b.Helper()
	pprof.Do(context.Background(), pprof.Labels("phase", "operation", "operation", name), func(context.Context) { fn() })
}

func BenchmarkCommands(b *testing.B) {
	for _, n := range []int{10, 1000, 10000} {
		b.Run(fmt.Sprintf("N%d", n), func(b *testing.B) {
			f := getFixture(b, n)
			for _, c := range readCases(f) {
				b.Run(c.name, func(b *testing.B) {
					var answer query.Answer
					var size byteCounter
					b.ReportAllocs()
					b.ResetTimer()
					measured(b, c.name, func() {
						for i := 0; i < b.N; i++ {
							project, err := store.Discover(f.Project.Root)
							must(b, err)
							request := c.request
							if request.Command == "continue" {
								o := observe(project.Root)
								request.Observed = &o
							}
							answer, err = query.Read(project, request)
							must(b, err)
							size = 0
							must(b, query.RenderJSON(&size, answer))
						}
					})
					b.StopTimer()
					b.ReportMetric(float64(size), "json-B/op")
					var textSize byteCounter
					must(b, query.RenderText(&textSize, answer))
					b.ReportMetric(float64(textSize), "text-B/op")
				})
			}
			b.Run("Capture", func(b *testing.B) { benchCapture(b, f) })
			b.Run("AdmitOrdinary", func(b *testing.B) { benchAdmit(b, f, false) })
			b.Run("AdmitProof3", func(b *testing.B) { benchAdmit(b, f, true) })
			b.Run("Run", func(b *testing.B) { benchRun(b, f) })
		})
	}
}

func removePacket(t testing.TB, f *fixture, id model.ID) {
	t.Helper()
	dir, err := store.IntakeDir(f.Project)
	must(t, err)
	must(t, os.RemoveAll(filepath.Join(dir, string(id))))
}

func benchCapture(b *testing.B, f *fixture) {
	event, err := model.EncodeEvent(f.task())
	must(b, err)
	id := f.id()
	b.ReportAllocs()
	b.ResetTimer()
	measured(b, "Capture", func() {
		for i := 0; i < b.N; i++ {
			ref, err := store.WriteIntake(context.Background(), f.Project, store.IntakeRequest{CommandID: id, Author: author, Events: []model.Event{event}, Blobs: []io.Reader{bytes.NewReader(resultBody)}})
			must(b, err)
			b.StopTimer()
			removePacket(b, f, ref.CommandID)
			b.StartTimer()
		}
	})
}

func benchAdmit(b *testing.B, f *fixture, proof bool) {
	var event model.TypedEvent = f.task()
	name := "AdmitOrdinary"
	if proof {
		event = &f.Proof
		name = "AdmitProof3"
	}
	packet := f.capture(nil, event)
	defer removePacket(b, f, packet.CommandID)
	request := f.request(packet)
	b.ReportAllocs()
	b.ResetTimer()
	measured(b, name, func() {
		for i := 0; i < b.N; i++ {
			bundle, err := write.Admit(context.Background(), f.Project, request)
			must(b, err)
			if bundle.Sequence != uint64(f.Bundles+1) {
				b.Fatal("admission measured a retry or changed baseline")
			}
			b.StopTimer()
			name, err := model.BundleName(bundle.Sequence, bundle.CommandID)
			must(b, err)
			must(b, os.Remove(filepath.Join(f.Project.Ledger, name)))
			b.StartTimer()
		}
	})
}

// Run includes the CLI's prefix replay, instrument projection, fresh execution
// identity, actual child execution, two captures, and result encoding. Only flag
// parsing and starting the datum executable itself are omitted. /usr/bin/printf
// is a tiny measurement producer; its workload is deliberately constant.
func benchRun(b *testing.B, f *fixture) {
	b.ReportAllocs()
	b.ResetTimer()
	measured(b, "Run", func() {
		for i := 0; i < b.N; i++ {
			project, err := store.Discover(f.Project.Root)
			must(b, err)
			prefix, err := store.ReadPrefix(project)
			must(b, err)
			s, err := reduce.Replay(prefix)
			must(b, err)
			instrument, ok := s.Instrument(reduce.Ident{Project: project.ID, ID: f.Instrument.RecordID})
			if !ok {
				b.Fatal("fixture instrument missing")
			}
			result, err := write.Run(context.Background(), project, write.RunRequest{Author: author, AttemptID: f.Attempt, InstrumentRef: f.Instrument, Instrument: *instrument.Spec,
				CriterionRef: unknown[model.CriterionRef](), ExecutionSourceIdentity: write.RunExecutionIdentity(context.Background(), project), Argv: []string{"/usr/bin/printf", "{\"measured\":true}\n"}})
			must(b, err)
			_, err = model.Encode(result)
			must(b, err)
			if result.SealPacket.CommandID == "" {
				b.Fatal("run did not capture a seal")
			}
			b.StopTimer()
			removePacket(b, f, result.StartPacket.CommandID)
			removePacket(b, f, result.SealPacket.CommandID)
			must(b, os.RemoveAll(result.ArtifactDir))
			b.StartTimer()
		}
	})
}
