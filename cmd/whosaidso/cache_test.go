package main

// End-to-end tests of the snapshot cache through fresh CLI processes: every
// read answers byte for byte as a full replay does, whether the cache is
// cold, warm, deleted, damaged or stale, and concurrent processes never see a
// stale answer. The loader's own cases are in internal/store.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/query"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func cacheReads(output model.ArtifactRef, records []model.RecordRef) [][]string {
	// The four views and the disposal check are the reads.
	reads := [][]string{{"show"}, {"history"}, {"todo"}, {"show", "--kind", "instrument"}, {"show", "--kind", "claim"},
		{"check", "disposal", "--digest", string(output.Content.SHA256)}}
	for _, r := range records {
		reads = append(reads, []string{"show", string(r.RecordID)}, []string{"history", string(r.RecordID)})
	}
	var all [][]string
	for _, args := range reads {
		at := 1
		if args[0] == "check" {
			at = 2
		}
		all = append(all, args, append(append(append([]string{}, args[:at]...), "--json"), args[at:]...))
	}
	return all
}

// answers runs every read as a fresh process and returns their exact output.
func answers(t *testing.T, root string, reads [][]string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, args := range reads {
		out[strings.Join(args, " ")] = string(readProcess(t, root, nil, args...))
	}
	return out
}

func sameAnswers(t *testing.T, state string, want, got map[string]string) {
	t.Helper()
	for command, answer := range got {
		if want[command] != answer {
			t.Errorf("whosaidso %s with the cache %s differs from full replay:\n%s\nwant\n%s", command, state, answer, want[command])
		}
	}
}

func cacheImage(t *testing.T, root string) string {
	t.Helper()
	project, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(project.CacheDir(), "snapshot")
}

func TestCLIAnswersAreByteIdenticalWhateverTheCacheHolds(t *testing.T) {
	root, output, records := disposalWorld(t)
	reads := cacheReads(output, records)
	image := cacheImage(t, root)
	cacheDir := filepath.Dir(image)
	// Reference: the cache folder is a plain file, so nothing can be read from
	// or written to it and every process replays the whole ledger.
	if err := os.RemoveAll(cacheDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacheDir, []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := answers(t, root, reads)
	if err := os.Remove(cacheDir); err != nil {
		t.Fatal(err)
	}
	sameAnswers(t, "cold", want, answers(t, root, reads[:1]))
	if _, err := os.Stat(image); err != nil {
		t.Fatalf("control: a cold read publishes an image: %v", err)
	}
	sameAnswers(t, "warm", want, answers(t, root, reads))
	if err := os.Remove(image); err != nil {
		t.Fatal(err)
	}
	sameAnswers(t, "deleted", want, answers(t, root, reads))
	data, err := os.ReadFile(image)
	if err != nil {
		t.Fatal(err)
	}
	// A same-length edit inside the state: it still decodes, into different
	// facts, so only the checksum can refuse it.
	if !bytes.Contains(data, []byte("pose sweep")) {
		t.Fatal("control: the image carries the fixture's text")
	}
	data = bytes.ReplaceAll(data, []byte("pose sweep"), []byte("pose swEEp"))
	if err := os.WriteFile(image, data, 0o600); err != nil {
		t.Fatal(err)
	}
	sameAnswers(t, "corrupted", want, answers(t, root, reads))
	if err := os.WriteFile(image, data[:len(data)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	sameAnswers(t, "truncated", want, answers(t, root, reads))

	// Stale: an image of an older prefix, as a late publisher leaves it. The
	// reads catch up and answer the new ledger.
	answers(t, root, reads[:1])
	old, err := os.ReadFile(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := e2eAdmitOne(t, root, &model.DecisionOpen{ID: cliID(990), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}},
		Spec: model.DecisionSpec{Question: "keep the cache?", Options: []string{"yes", "no"}, WaitingActor: model.Actor{ID: "owner"},
			Scope: model.Scope{SourcePaths: []string{}, ContextRefs: []model.RecordRef{}, AppliesWhen: "this fixture", Limitations: "not a real ledger"}}}, 991, 992, "agent"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(image); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacheDir+"-off", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(cacheDir, cacheDir+"-kept"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(cacheDir+"-off", cacheDir); err != nil {
		t.Fatal(err)
	}
	want = answers(t, root, reads)
	if err := os.Remove(cacheDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(cacheDir+"-kept", cacheDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(image, old, 0o600); err != nil {
		t.Fatal(err)
	}
	sameAnswers(t, "stale", want, answers(t, root, reads))
}

// A historical bundle edited with the head untouched must change the answer,
// through the CLI, exactly as a full replay does.
func TestCLIAHistoricalEditIsNeverAnsweredFromTheCache(t *testing.T) {
	root, data := cliFixture(t)
	cliControl(t, root, data)
	if err := e2eAdmitOne(t, root, &model.TaskStart{Task: model.RecordRef{Project: "test/cli", RecordID: cliID(1), Revision: 1},
		Actor: model.Actor{ID: "agent"}, AttemptID: cliID(700)}, 701, 702, "agent"); err != nil {
		t.Fatal(err)
	}
	before := string(readProcess(t, root, nil, "show", "--json", string(cliID(1))))
	if !strings.Contains(before, "exercise the CLI") {
		t.Fatalf("control: the task reads with its intent, got %s", before)
	}
	project, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := store.ReadPrefix(project)
	if err != nil {
		t.Fatal(err)
	}
	name, err := model.BundleName(1, prefix[0].CommandID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project.Ledger, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := bytes.Replace(raw, []byte("exercise the CLI"), []byte("rewritten history"), 1)
	if bytes.Equal(raw, edited) {
		t.Fatal("control: the edit must change the first bundle")
	}
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	after := string(readProcess(t, root, nil, "show", "--json", string(cliID(1))))
	if !strings.Contains(after, "rewritten history") || strings.Contains(after, "exercise the CLI") {
		t.Fatalf("an edit under an unchanged head was answered from the cache:\n%s", after)
	}
}

// Fresh processes admit and read at once. Each read's answer must be the
// full replay of the prefix it names, never an older image's.
func TestCLIConcurrentProcessesNeverAnswerStale(t *testing.T) {
	root, data := cliFixture(t)
	cliControl(t, root, data)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 3; i++ {
				n := 800 + w*100 + i*10
				task := &model.TaskCreate{ID: cliID(n), Provenance: model.Provenance{SourceRefs: []model.ArtifactRef{}},
					Spec: model.TaskSpec{Intent: fmt.Sprintf("concurrent task %d", n), Subject: "cache", NonGoals: []string{"none"},
						Scope:              model.Scope{SourcePaths: []string{}, ContextRefs: []model.RecordRef{}, AppliesWhen: "this fixture", Limitations: "synthetic"},
						AcceptanceCriteria: []model.AcceptanceCriterion{{ID: cliID(n + 1), Revision: 1, Criterion: "reads"}}, ContextRefs: []model.RecordRef{},
						ConstraintRefs: []model.RecordRef{}, Prerequisites: []model.Prerequisite{}, NextActor: model.Actor{ID: "agent"}}}
				if err := e2eAdmitOne(t, root, task, n+2, n+3, "agent"); err != nil {
					errs <- err
				}
			}
		}(w)
	}
	for r := 0; r < 3; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 4; i++ {
				out, err := e2eInvoke(t, root, nil, "history", "--json")
				if err != nil {
					errs <- err
					return
				}
				answer := readJSON[query.HistoryAnswer](t, out)
				tasks := strings.Count(string(out), `"type": "task.create"`)
				show, err := e2eInvoke(t, root, nil, "show", "--json")
				if err != nil {
					errs <- err
					return
				}
				later := readJSON[query.ShowAnswer](t, show)
				if later.Watermark.Sequence < answer.Watermark.Sequence {
					errs <- fmt.Errorf("a later read answered at %d after %d", later.Watermark.Sequence, answer.Watermark.Sequence)
				}
				if int(answer.Watermark.Events) != len(answer.Events) || tasks == 0 {
					errs <- fmt.Errorf("history at %d names %d events of %d", answer.Watermark.Sequence, len(answer.Events), answer.Watermark.Events)
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	project, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(project)
	if err != nil {
		t.Fatal(err)
	}
	full, err := store.Replayed(project)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Snapshot().Watermark() != full.Snapshot().Watermark() || full.Snapshot().Watermark().Bundles != 7 {
		t.Fatalf("after the race the cached path is at %+v, the ledger at %+v", loaded.Snapshot().Watermark(), full.Snapshot().Watermark())
	}
}
