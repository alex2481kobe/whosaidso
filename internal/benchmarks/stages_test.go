// This file isolates measured costs and output limits through exported APIs.
// It does not implement production optimizations or weaken admission checks.
package benchmarks

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"datum/internal/model"
	"datum/internal/query"
	"datum/internal/reduce"
	"datum/internal/store"
)

var stageSink any

// Set -benchtime=1x for the largest fixture: some current projection paths are
// quadratic. Repeat count=8 for inexpensive stages; do not silently average
// fixture construction, profile collection or child-process allocations into them.
func BenchmarkStages(b *testing.B) {
	for _, n := range []int{10, 1000, 10000} {
		b.Run(fmt.Sprintf("N%d", n), func(b *testing.B) {
			f := getFixture(b, n)
			prefix, err := store.ReadPrefix(f.Project)
			must(b, err)
			s, err := reduce.Replay(prefix)
			must(b, err)
			stages := []struct {
				name string
				fn   func()
			}{
				{"ReadPrefix", func() { v, e := store.ReadPrefix(f.Project); must(b, e); stageSink = v }},
				{"Replay", func() { v, e := reduce.Replay(prefix); must(b, e); stageSink = v }},
				{"ReadIntake", func() { v, e := store.ReadVerifiedIntake(f.Project, nil); must(b, e); stageSink = v }},
				{"Records", func() { stageSink = s.Records() }},
				{"Current", func() {
					v, ok := s.Current(reduce.Ident{Project: f.Project.ID, ID: f.Task.RecordID})
					if !ok {
						b.Fatal("missing task")
					}
					stageSink = v
				}},
				{"Tasks", func() { stageSink = s.Tasks() }},
				{"SupportOne", func() {
					v, ok := s.Support(f.Claim)
					if !ok {
						b.Fatal("missing claim")
					}
					stageSink = v
				}},
				{"Supersessions", func() { stageSink = s.Supersessions() }},
				{"ClaimOne", func() {
					v, ok := s.ClaimAt(f.Claim)
					if !ok {
						b.Fatal("missing claim")
					}
					stageSink = v
				}},
				{"InvocationCopies", func() { stageSink = s.Invocations() }},
			}
			for _, stage := range stages {
				b.Run(stage.name, func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					measured(b, stage.name, func() {
						for i := 0; i < b.N; i++ {
							stage.fn()
						}
					})
				})
			}
		})
	}
}

// Show whether the limit actually bounds the answer. JSON/text sizes are exact
// byte counts, not token estimates. Run opt-in; the 10k continue is expensive.
func TestOutputLimits(t *testing.T) {
	if os.Getenv("DATUM_BENCH_OUTPUT") == "" {
		t.Skip("opt-in large read measurement")
	}
	f := getFixture(t, 10000)
	t.Logf("fixture: bundles=%d events=%d ledger_bytes=%d intake_packets=%d blob_bytes=%d", f.Bundles, f.Events, f.LedgerBytes, f.IntakePackets, f.BlobBytes)
	for _, request := range []query.ViewRequest{
		{View: "todo", Limit: 1},
		{View: "continue", ID: f.Task.RecordID, Limit: 1},
	} {
		a, err := query.ReadView(f.Project, request)
		must(t, err)
		var jsonSize, textSize byteCounter
		must(t, query.RenderViewJSON(&jsonSize, a))
		must(t, query.RenderViewBrief(&textSize, a))
		t.Logf("%s limit=%d json_bytes=%d text_bytes=%d", request.View, request.Limit, jsonSize, textSize)
	}
}

// Decoder and projection checks are exercised with a passing control before
// malformed fixture mutations. These protect the experiment from timing a
// shortcut that silently drops history or a member of the proof family.
func TestFixtureMutations(t *testing.T) {
	f := getFixture(t, 10)
	prefix, err := store.ReadPrefix(f.Project)
	must(t, err)
	_, err = reduce.Replay(prefix)
	must(t, err)
	for _, mutation := range []string{"gap", "proof-member", "uncaptured-start"} {
		t.Run(mutation, func(t *testing.T) {
			copyOf := append([]model.Bundle(nil), prefix...)
			switch mutation {
			case "gap":
				copyOf = copyOf[1:]
			case "proof-member":
				copyOf[7].Events = append([]model.Event(nil), copyOf[7].Events...)
				p := f.Proof
				p.Evidence = append([]model.ObservationDisposition(nil), p.Evidence[:2]...)
				raw, e := model.EncodeEvent(&p)
				must(t, e)
				copyOf[7].Events[0] = raw
			case "uncaptured-start":
				copyOf[3].Events = append([]model.Event(nil), copyOf[3].Events...)
				raw, e := model.DecodeEvent(copyOf[3].Events[1])
				must(t, e)
				review := raw.(*model.ReviewAdmit)
				review.CapturedAt = nil
				copyOf[3].Events[1], e = model.EncodeEvent(review)
				must(t, e)
			}
			if _, err := reduce.Replay(copyOf); err == nil {
				t.Fatalf("mutation %s escaped", mutation)
			} else {
				t.Logf("caught %s: %v", mutation, err)
			}
		})
	}
}

func BenchmarkCodec(b *testing.B) {
	f := getFixture(b, 10)
	prefix, err := store.ReadPrefix(f.Project)
	must(b, err)
	data, err := model.Encode(prefix[5])
	must(b, err)
	typed := make([]model.TypedEvent, len(prefix[5].Events))
	for i, event := range prefix[5].Events {
		typed[i], err = model.DecodeEvent(event)
		must(b, err)
	}
	for _, name := range []string{"DecodeBundle", "DecodeEvents", "EncodeBundle", "ReferenceWalk"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			measured(b, name, func() {
				for i := 0; i < b.N; i++ {
					switch name {
					case "DecodeBundle":
						v, e := model.DecodeBundle(data)
						must(b, e)
						stageSink = v
					case "DecodeEvents":
						for _, event := range prefix[5].Events {
							v, e := model.DecodeEvent(event)
							must(b, e)
							stageSink = v
						}
					case "EncodeBundle":
						v, e := model.Encode(prefix[5])
						must(b, e)
						stageSink = v
					case "ReferenceWalk":
						for _, event := range typed {
							v, e := model.SameProjectReferences(event, f.Project.ID)
							must(b, e)
							stageSink = v
						}
					}
				}
			})
		})
	}
}

// This is a different intake workload, not a proposed skipped integrity check.
// It isolates the cost of retaining already reviewed packets. A moved project's
// ledger remains valid without them; the pending scan still runs normally.
func BenchmarkProofWithoutRetainedIntake(b *testing.B) {
	f := getFixture(b, 10000)
	dir, err := store.IntakeDir(f.Project)
	must(b, err)
	saved := dir + "-astraeff-saved"
	must(b, os.Rename(dir, saved))
	defer func() { must(b, os.RemoveAll(dir)); must(b, os.Rename(saved, dir)) }()
	benchAdmit(b, f, true)
}

func TestFixtureWireControl(t *testing.T) {
	f := getFixture(t, 10)
	prefix, err := store.ReadPrefix(f.Project)
	must(t, err)
	name, err := model.BundleName(1, prefix[0].CommandID)
	must(t, err)
	data, err := os.ReadFile(filepath.Join(f.Project.Ledger, name))
	must(t, err)
	_, err = model.DecodeBundle(data)
	must(t, err)
	// A duplicate key must fail, not become a cheaper last-key-wins read.
	bad := bytes.Replace(data, []byte(`"version": 1`), []byte(`"version": 1, "version": 1`), 1)
	if bytes.Equal(data, bad) {
		t.Fatal("mutation did not change bytes")
	}
	if _, err = model.DecodeBundle(bad); err == nil {
		t.Fatal("duplicate wire key escaped")
	}
}
