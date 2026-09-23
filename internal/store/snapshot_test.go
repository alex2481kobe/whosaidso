package store

// Tests for the validated snapshot loader (snapshot.go) and its cache files
// (cache.go). Every refusal and recovery case opens with a control, and every
// answer is compared with a full replay of the same ledger, whole state.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/reduce"
)

// cacheTask proposes one valid task, so the ledger folds under the reducer.
func cacheTask(n int) func([]model.Bundle) (model.Bundle, error) {
	return func([]model.Bundle) (model.Bundle, error) {
		actor := model.Actor{ID: "cache-test"}
		e, err := model.EncodeEvent(&model.TaskCreate{ID: admissionID(500000 + n), Provenance: model.Provenance{Author: actor, SourceRefs: []model.ArtifactRef{}},
			Spec: model.TaskSpec{Intent: fmt.Sprintf("cache task %d", n), Subject: "snapshot cache",
				Scope:    model.Scope{SourcePaths: []string{"internal/store"}, ContextRefs: []model.RecordRef{}, AppliesWhen: "tests", Limitations: "synthetic"},
				NonGoals: []string{"production use"}, AcceptanceCriteria: []model.AcceptanceCriterion{{ID: admissionID(600000 + n), Revision: 1, Criterion: "same answer as replay"}},
				ContextRefs: []model.RecordRef{}, ConstraintRefs: []model.RecordRef{}, Prerequisites: []model.Prerequisite{}, NextActor: actor}})
		if err != nil {
			return model.Bundle{}, err
		}
		return model.Bundle{Admitter: actor, Packets: []model.PacketRef{}, Events: []model.Event{e}}, nil
	}
}

func appendTasks(t testing.TB, p Project, from, count int) {
	t.Helper()
	for n := from; n < from+count; n++ {
		if _, err := Transact(context.Background(), p, admissionID(n), digestFor(n), cacheTask(n)); err != nil {
			t.Fatalf("control task %d: %v", n, err)
		}
	}
}

// requireReplayed loads through the cache and checks the whole state, and the
// raw bundles, against a full replay of the same ledger.
func requireReplayed(t testing.TB, p Project, restored int) State {
	t.Helper()
	got, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want, err := Replayed(p)
	if err != nil {
		t.Fatalf("replay control: %v", err)
	}
	if !reflect.DeepEqual(got.Snapshot(), want.Snapshot()) {
		t.Fatalf("the cached path's state differs from full replay at sequence %d", want.Snapshot().Watermark().Sequence)
	}
	bundles, err := got.Bundles()
	if err != nil || !reflect.DeepEqual(bundles, mustBundles(t, want)) {
		t.Fatalf("the cached path's bundles differ from the ledger's: %v", err)
	}
	if got.Restored() != restored {
		t.Fatalf("restored %d bundles from the cache, want %d", got.Restored(), restored)
	}
	return got
}

func mustBundles(t testing.TB, s State) []model.Bundle {
	t.Helper()
	b, err := s.Bundles()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func imagePath(p Project) string { return filepath.Join(p.CacheDir(), cacheFile) }

func imageBytes(t testing.TB, p Project) []byte {
	t.Helper()
	data, err := os.ReadFile(imagePath(p))
	if err != nil {
		t.Fatalf("the cache image: %v", err)
	}
	return data
}

// warmProject has four tasks and a published image of them.
func warmProject(t *testing.T) Project {
	t.Helper()
	p := ledgerProject(t)
	appendTasks(t, p, 1, 4)
	requireReplayed(t, p, 0)
	requireReplayed(t, p, 4)
	return p
}

func TestLoadIsFullReplayColdWarmAndAfterAppends(t *testing.T) {
	p := ledgerProject(t)
	if s := requireReplayed(t, p, 0); s.Snapshot().Watermark().Sequence != 0 {
		t.Fatal("an absent ledger is the empty snapshot")
	}
	if _, err := os.Lstat(p.CacheDir()); !os.IsNotExist(err) {
		t.Fatalf("a project with no ledger got a cache folder: %v", err)
	}
	appendTasks(t, p, 1, 4)
	requireReplayed(t, p, 0) // cold: rebuilt and published
	image := imageBytes(t, p)
	info, _ := os.Stat(imagePath(p))
	requireReplayed(t, p, 4) // warm: restored, nothing folded
	after, _ := os.Stat(imagePath(p))
	if !bytes.Equal(image, imageBytes(t, p)) || !after.ModTime().Equal(info.ModTime()) {
		t.Fatal("a warm hit with no new bundle rewrote the image")
	}
	appendTasks(t, p, 5, 1)
	requireReplayed(t, p, 4) // +1: restored 4, folded 1, republished
	requireReplayed(t, p, 5)
	appendTasks(t, p, 6, 100)
	requireReplayed(t, p, 5) // +100
	requireReplayed(t, p, 105)
	if err := os.Remove(imagePath(p)); err != nil {
		t.Fatal(err)
	}
	requireReplayed(t, p, 0) // deleted: rebuilt
	requireReplayed(t, p, 105)
}

// The head-only defect: an earlier bundle changes while the head file, its
// name and its bytes stay identical. The image must not be reused.
func TestAHistoricalEditUnderAnUnchangedHeadForcesARebuild(t *testing.T) {
	p := warmProject(t)
	first := ledgerPath(t, p, 1, admissionID(1))
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(data, []byte("cache task 1"), []byte("cache task X"), 1)
	if bytes.Equal(changed, data) {
		t.Fatal("control: the edit must change the bundle")
	}
	if err := os.WriteFile(first, changed, 0o644); err != nil {
		t.Fatal(err)
	}
	got := requireReplayed(t, p, 0)
	task, ok := got.Snapshot().Current(reduce.Ident{Project: p.ID, ID: admissionID(500001)})
	if !ok || task.Task.Intent != "cache task X" {
		t.Fatalf("the answer must be the edited ledger's, got %+v", task.Task)
	}
	requireReplayed(t, p, 4)
}

func TestDamagedOrForeignImagesAreRebuiltNeverServed(t *testing.T) {
	cases := map[string]func(t *testing.T, p Project){
		"deleted":   func(t *testing.T, p Project) { must(t, os.Remove(imagePath(p))) },
		"truncated": func(t *testing.T, p Project) { must(t, os.WriteFile(imagePath(p), imageBytes(t, p)[:100], 0o600)) },
		"empty":     func(t *testing.T, p Project) { must(t, os.WriteFile(imagePath(p), nil, 0o600)) },
		"flipped": func(t *testing.T, p Project) {
			data := imageBytes(t, p)
			data[len(data)-10] ^= 1
			must(t, os.WriteFile(imagePath(p), data, 0o600))
		},
		"other version": func(t *testing.T, p Project) { rewriteImage(t, p, func(im *cacheImage) { im.version += "-old" }) },
		"other project": func(t *testing.T, p Project) { rewriteImage(t, p, func(im *cacheImage) { im.project = "team/other" }) },
		"longer than the ledger": func(t *testing.T, p Project) {
			rewriteImage(t, p, func(im *cacheImage) { im.count++ })
		},
		"wrong prefix digest": func(t *testing.T, p Project) { rewriteImage(t, p, func(im *cacheImage) { im.chain[0] ^= 1 }) },
		"undecodable state":   func(t *testing.T, p Project) { rewriteImage(t, p, func(im *cacheImage) { im.state = []byte{1, 2, 3} }) },
		"state at another head": func(t *testing.T, p Project) {
			older, err := ReadPrefix(p)
			must(t, err)
			s, err := reduce.Replay(older[:3])
			must(t, err)
			rewriteImage(t, p, func(im *cacheImage) { im.state, _ = reduce.EncodeSnapshot(s) })
		},
		"image is a directory": func(t *testing.T, p Project) {
			must(t, os.Remove(imagePath(p)))
			must(t, os.Mkdir(imagePath(p), 0o755))
		},
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			p := warmProject(t)
			damage(t, p)
			requireReplayed(t, p, 0)
			if name != "image is a directory" {
				requireReplayed(t, p, 4) // the rebuild republished a good image
			}
		})
	}
}

func rewriteImage(t *testing.T, p Project, edit func(*cacheImage)) {
	t.Helper()
	im, err := decodeImage(imageBytes(t, p))
	must(t, err)
	edit(&im)
	must(t, os.WriteFile(imagePath(p), encodeImage(im), 0o600))
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// A bad ledger is the ledger's error whether or not an image exists: the
// cache can never answer for, or hide, a broken prefix.
func TestABrokenLedgerFailsExactlyAsFullReplayDoes(t *testing.T) {
	cases := map[string]func(t *testing.T, p Project){
		"gap": func(t *testing.T, p Project) { must(t, os.Remove(ledgerPath(t, p, 2, admissionID(2)))) },
		"fork": func(t *testing.T, p Project) {
			writeLedgerFile(t, p, mustName(t, 4, admissionID(99)), rawBundle(t, p, 4, admissionID(99), admissionID(3)))
		},
		"junk": func(t *testing.T, p Project) { writeLedgerFile(t, p, "notes.txt", []byte("x")) },
		"corrupt prefix": func(t *testing.T, p Project) {
			must(t, os.WriteFile(ledgerPath(t, p, 1, admissionID(1)), []byte("{"), 0o644))
		},
		"corrupt tail": func(t *testing.T, p Project) {
			writeLedgerFile(t, p, mustName(t, 5, admissionID(5)), []byte("{"))
		},
		"tail breaks the chain": func(t *testing.T, p Project) {
			writeLedgerFile(t, p, mustName(t, 5, admissionID(5)), rawBundle(t, p, 5, admissionID(5), admissionID(1)))
		},
		"tail repeats a command": func(t *testing.T, p Project) {
			data, err := os.ReadFile(ledgerPath(t, p, 4, admissionID(4)))
			must(t, err)
			b, err := model.DecodeBundle(data)
			must(t, err)
			b.Sequence, b.Predecessor, b.CommandID = 5, admissionID(4), admissionID(1)
			again, err := model.Encode(b)
			must(t, err)
			writeLedgerFile(t, p, mustName(t, 5, admissionID(1)), again)
		},
		"tail fails the reducer": func(t *testing.T, p Project) {
			writeLedgerFile(t, p, mustName(t, 5, admissionID(5)), rawBundle(t, p, 5, admissionID(5), admissionID(4)))
		},
	}
	for name, breakLedger := range cases {
		t.Run(name, func(t *testing.T) {
			p := warmProject(t)
			breakLedger(t, p)
			_, want := Replayed(p)
			if want == nil {
				t.Fatal("control: the broken ledger must fail a full replay")
			}
			if _, err := Load(p); err == nil || err.Error() != want.Error() {
				t.Fatalf("the cached path answered %v, full replay %v", err, want)
			}
		})
	}
}

func mustName(t *testing.T, sequence uint64, command model.ID) string {
	t.Helper()
	name, err := model.BundleName(sequence, command)
	must(t, err)
	return name
}

func TestTheCacheNeverResurrectsARemovedLedger(t *testing.T) {
	p := warmProject(t)
	must(t, os.Remove(ledgerPath(t, p, 4, admissionID(4))))
	requireReplayed(t, p, 0) // rolled back: the image folded a bundle that is gone
	must(t, os.RemoveAll(p.Ledger))
	if s := requireReplayed(t, p, 0); s.Snapshot().Watermark().Sequence != 0 {
		t.Fatal("a deleted ledger answered from its cache")
	}
}

// Moving or copying a checkout keeps the ledger's identity, so its image is
// still reused; the image names no absolute path.
func TestAMovedProjectReusesItsImage(t *testing.T) {
	p := warmProject(t)
	moved := ledgerProject(t)
	must(t, os.RemoveAll(moved.Root))
	must(t, os.Rename(p.Root, moved.Root))
	requireReplayed(t, moved, 4)
}

// A late publisher may put an older image back; that costs catch-up and never
// moves an answer backwards.
func TestAStalePublisherCostsCatchUpNotAnswers(t *testing.T) {
	p := warmProject(t)
	old := imageBytes(t, p)
	appendTasks(t, p, 5, 3)
	requireReplayed(t, p, 4)
	requireReplayed(t, p, 7)
	must(t, os.WriteFile(imagePath(p), old, 0o600))
	requireReplayed(t, p, 4)
}

func TestInterruptedAndFailedPublicationsChangeNoAnswer(t *testing.T) {
	p := warmProject(t)
	// A crash mid-publication leaves a temporary; it is never read, and an old
	// one is cleared by the next publisher.
	stale := filepath.Join(p.CacheDir(), cacheTemp+"crashed.tmp")
	must(t, os.WriteFile(stale, []byte("partial"), 0o600))
	requireReplayed(t, p, 4)
	past := time.Now().Add(-2 * staleCacheTemp)
	must(t, os.Chtimes(stale, past, past))
	appendTasks(t, p, 5, 1)
	requireReplayed(t, p, 4)
	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Fatalf("a stale temporary survived the next publication: %v", err)
	}
	// A publication that fails answers correctly and leaves the old image.
	publishImage = func(Project, cacheImage) error { return fmt.Errorf("disk full") }
	defer func() { publishImage = writeImage }()
	appendTasks(t, p, 6, 1)
	requireReplayed(t, p, 5)
	requireReplayed(t, p, 5)
	publishImage = writeImage
	requireReplayed(t, p, 5)
	requireReplayed(t, p, 6)
}

// The cache folder must not carry a read or a write out of the datum root.
func TestACacheFolderSymlinkIsNeitherReadNorWritten(t *testing.T) {
	p := warmProject(t)
	outside := t.TempDir()
	must(t, os.Rename(p.CacheDir(), filepath.Join(outside, "cache")))
	must(t, os.Symlink(filepath.Join(outside, "cache"), p.CacheDir()))
	requireReplayed(t, p, 0)
	appendTasks(t, p, 5, 1)
	before := imageBytes(t, Project{Ledger: filepath.Join(outside, "events")})
	requireReplayed(t, p, 0)
	if !bytes.Equal(before, imageBytes(t, Project{Ledger: filepath.Join(outside, "events")})) {
		t.Fatal("a publication followed the cache folder's symlink out of the root")
	}
}

// Readers and writers race: every answer must be exactly the replay of the
// prefix it names, whichever image each reader found.
func TestConcurrentWritersAndReadersNeverSeeAStaleOrMixedState(t *testing.T) {
	p := warmProject(t)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 5; n < 25; n++ {
			if _, err := Transact(context.Background(), p, admissionID(n), digestFor(n), cacheTask(n)); err != nil {
				errs <- err
			}
		}
	}()
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			last := uint64(0)
			for i := 0; i < 15; i++ {
				s, err := Load(p)
				if err != nil {
					errs <- err
					return
				}
				bundles, err := s.Bundles()
				if err != nil {
					errs <- err
					return
				}
				want, err := reduce.Replay(bundles)
				if err != nil || !reflect.DeepEqual(s.Snapshot(), want) {
					errs <- fmt.Errorf("a concurrent load's state is not the replay of its own prefix: %v", err)
					return
				}
				if w := s.Snapshot().Watermark().Sequence; w < last {
					errs <- fmt.Errorf("an answer moved backwards from %d to %d", last, w)
					return
				} else {
					last = w
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	// The last image to land may be an older one; one catch-up repairs it.
	if _, err := Load(p); err != nil {
		t.Fatal(err)
	}
	requireReplayed(t, p, 24)
}

// A restored state's raw bundles are re-read on demand. They are the selected
// prefix or an error, never a later ledger's bytes under the old watermark.
func TestBundlesAreTheSelectedPrefixOrAnError(t *testing.T) {
	p := warmProject(t)
	s := requireReplayed(t, p, 4)
	if _, err := s.Bundles(); err != nil {
		t.Fatalf("control: an unchanged ledger's bundles read back: %v", err)
	}
	first := ledgerPath(t, p, 1, admissionID(1))
	data, err := os.ReadFile(first)
	must(t, err)
	must(t, os.WriteFile(first, bytes.Replace(data, []byte("cache task 1"), []byte("cache task Y"), 1), 0o644))
	if _, err := s.Bundles(); err == nil {
		t.Fatal("bundles changed after selection were returned under the old selection")
	}
}
