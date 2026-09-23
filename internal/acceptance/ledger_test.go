package acceptance_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"datum/internal/model"
	"datum/internal/store"
)

func laneELedgerProject(t *testing.T) store.Project {
	t.Helper()
	root := t.TempDir()
	return store.Project{ID: "datum/lane-e-ledger", Root: root, Ledger: filepath.Join(root, ".datum", "events")}
}

func laneELedgerID(n int) model.ID { return model.ID(fmt.Sprintf("%026d", n)) }

func laneELedgerDigest(n int) model.Digest {
	return model.HashBytes([]byte(fmt.Sprintf("lane-e-ledger-request-%d", n)))
}

func laneELedgerProposal(n int) func([]model.Bundle) (model.Bundle, error) {
	return func([]model.Bundle) (model.Bundle, error) {
		packets := []model.PacketRef{{CommandID: laneELedgerID(n + 1000), Digest: laneELedgerDigest(n + 1000)}}
		event, err := model.EncodeEvent(&model.ReviewAdmit{
			Packets: packets, Outcome: "accepted", Actor: model.Actor{ID: "coordinator"},
			Reason: fmt.Sprintf("packet %d was independently checked", n),
			// R18.2: every review carries authors, captured_at and event_packets.
			Authors:    map[model.ID]model.Actor{packets[0].CommandID: {ID: "author"}},
			CapturedAt: map[model.ID]model.Availability[time.Time]{packets[0].CommandID: {State: model.Unknown, Reason: "not recorded"}}, EventPackets: []model.ID{},
		})
		return model.Bundle{Admitter: model.Actor{ID: "coordinator"}, Packets: packets, Events: []model.Event{event}}, err
	}
}

func laneELedgerAdmit(t *testing.T, p store.Project, n int) model.Bundle {
	t.Helper()
	b, err := store.Transact(context.Background(), p, laneELedgerID(n), laneELedgerDigest(n), laneELedgerProposal(n))
	if err != nil {
		t.Fatalf("valid admission %d must succeed: %v", n, err)
	}
	return b
}

func laneELedgerRead(t *testing.T, p store.Project, count int) []model.Bundle {
	t.Helper()
	bundles, err := store.ReadPrefix(p)
	if err != nil || len(bundles) != count {
		t.Fatalf("valid ledger must contain %d bundles, got %d: %v", count, len(bundles), err)
	}
	for i, b := range bundles {
		if b.Sequence != uint64(i+1) || (i == 0 && b.Predecessor != "") || (i > 0 && b.Predecessor != bundles[i-1].CommandID) {
			t.Fatalf("bundle at position %d is not a complete prefix: %+v", i, b)
		}
	}
	return bundles
}

func laneELedgerPath(t *testing.T, p store.Project, sequence uint64, id model.ID) string {
	t.Helper()
	name, err := model.BundleName(sequence, id)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(p.Ledger, name)
}

func laneELedgerWrite(t *testing.T, path string, b model.Bundle) {
	t.Helper()
	data, err := model.Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.DecodeBundle(data); err != nil {
		t.Fatalf("attack must use an individually valid bundle envelope: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLedgerOneCommandIDCannotNameTwoAdmittedBundles(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprintf("changed_request_%t", changed), func(t *testing.T) {
			p := laneELedgerProject(t)
			first := laneELedgerAdmit(t, p, 1)
			second := laneELedgerAdmit(t, p, 2)
			laneELedgerRead(t, p, 2)
			called := false
			retry := func([]model.Bundle) (model.Bundle, error) {
				called = true
				return model.Bundle{}, errors.New("a published command must not be proposed again")
			}
			if b, err := store.Transact(context.Background(), p, first.CommandID, first.RequestDigest, retry); err != nil || b.Sequence != 1 || called {
				t.Fatalf("control retry did not recover the original admission: %+v, %v, callback=%t", b, err, called)
			}

			// A then B then A is contiguous and every predecessor names the prior
			// bundle, but A now identifies two separate admission positions.
			repeated := first
			repeated.Sequence, repeated.Predecessor = 3, second.CommandID
			if changed {
				replacement, err := laneELedgerProposal(99)(nil)
				if err != nil {
					t.Fatal(err)
				}
				repeated.Events, repeated.Packets = replacement.Events, replacement.Packets
				repeated.RequestDigest = laneELedgerDigest(99)
			}
			laneELedgerWrite(t, laneELedgerPath(t, p, 3, repeated.CommandID), repeated)
			if prefix, err := store.ReadPrefix(p); err == nil {
				t.Errorf("one command id was accepted at sequences 1 and 3 in a %d-bundle prefix, changed request=%t", len(prefix), changed)
			}
			if b, err := store.Transact(context.Background(), p, first.CommandID, first.RequestDigest, retry); err == nil {
				t.Errorf("retry selected sequence %d for an identity also admitted at sequence 3 instead of refusing the ambiguous ledger", b.Sequence)
			}
		})
	}
}

func TestLedgerRefusesBrokenRelationshipsBeforeReturningOrAppendingAPrefix(t *testing.T) {
	cases := []struct {
		name        string
		code        string
		breakLedger func(*testing.T, store.Project, []model.Bundle)
	}{
		{"filename claims another command", "ledger-corrupt", func(t *testing.T, p store.Project, b []model.Bundle) {
			if err := os.Rename(laneELedgerPath(t, p, 2, b[1].CommandID), laneELedgerPath(t, p, 2, laneELedgerID(99))); err != nil {
				t.Fatal(err)
			}
		}},
		{"filename claims another sequence", "ledger-corrupt", func(t *testing.T, p store.Project, b []model.Bundle) {
			b[1].Sequence = 3
			laneELedgerWrite(t, laneELedgerPath(t, p, 2, b[1].CommandID), b[1])
		}},
		{"two commands claim one sequence", "ledger-fork", func(t *testing.T, p store.Project, b []model.Bundle) {
			b[1].CommandID = laneELedgerID(99)
			laneELedgerWrite(t, laneELedgerPath(t, p, 2, b[1].CommandID), b[1])
		}},
		{"a middle sequence is absent", "ledger-discontinuity", func(t *testing.T, p store.Project, b []model.Bundle) {
			if err := os.Remove(laneELedgerPath(t, p, 2, b[1].CommandID)); err != nil {
				t.Fatal(err)
			}
		}},
		{"predecessor names a real but earlier bundle", "ledger-discontinuity", func(t *testing.T, p store.Project, b []model.Bundle) {
			b[2].Predecessor = b[0].CommandID
			laneELedgerWrite(t, laneELedgerPath(t, p, 3, b[2].CommandID), b[2])
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := laneELedgerProject(t)
			for n := 1; n <= 3; n++ {
				laneELedgerAdmit(t, p, n)
			}
			c.breakLedger(t, p, laneELedgerRead(t, p, 3))
			prefix, err := store.ReadPrefix(p)
			var fault *model.Fault
			if !errors.As(err, &fault) || fault.Code != c.code || len(prefix) != 0 {
				t.Errorf("%s returned %d bundles and %v, want no prefix and %s", c.name, len(prefix), err, c.code)
			}
			called := false
			_, err = store.Transact(context.Background(), p, laneELedgerID(4), laneELedgerDigest(4), func(prefix []model.Bundle) (model.Bundle, error) {
				called = true
				return laneELedgerProposal(4)(prefix)
			})
			if !errors.As(err, &fault) || fault.Code != c.code || called {
				t.Errorf("admission reached callback=%t over %s and returned %v", called, c.name, err)
			}
		})
	}
}

func TestLedgerCreationOrderAndReturnedSliceMutationCannotChangeTheNextRead(t *testing.T) {
	p := laneELedgerProject(t)
	for n := 1; n <= 6; n++ {
		laneELedgerAdmit(t, p, n)
	}
	want := laneELedgerRead(t, p, 6)
	other := laneELedgerProject(t)
	for _, i := range []int{5, 1, 3, 0, 4, 2} {
		laneELedgerWrite(t, laneELedgerPath(t, other, want[i].Sequence, want[i].CommandID), want[i])
	}
	got := laneELedgerRead(t, other, 6)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("recreating identical bundles in a different file order changed the selected prefix")
	}
	got[0].CommandID = laneELedgerID(999)
	got[0].Packets[0].CommandID = laneELedgerID(999)
	got[0].Events[0].Data[0] = '!'
	if next := laneELedgerRead(t, other, 6); !reflect.DeepEqual(next, want) {
		t.Fatal("mutating a returned bundle changed the snapshot seen by a later reader")
	}
}

func TestLedgerRecoveryPreservesForeignTemporaryBytesAndRemovesOnlyItsOwnNames(t *testing.T) {
	p := laneELedgerProject(t)
	laneELedgerAdmit(t, p, 1)
	laneELedgerRead(t, p, 1)
	own := laneELedgerPath(t, p, 2, laneELedgerID(2)) + ".tmp"
	if err := os.WriteFile(own, []byte("an interrupted publication"), 0600); err != nil {
		t.Fatal(err)
	}
	laneELedgerRead(t, p, 1)
	laneELedgerAdmit(t, p, 2)
	if _, err := os.Stat(own); !os.IsNotExist(err) {
		t.Fatalf("control recovery left its own temporary: %v", err)
	}
	for _, name := range []string{"another-publisher.tmp", "00000003-" + string(laneELedgerID(3)) + ".other.json.tmp"} {
		t.Run(name, func(t *testing.T) {
			foreign := filepath.Join(p.Ledger, name)
			data := []byte("another publisher still owns these exact bytes")
			if err := os.WriteFile(foreign, data, 0600); err != nil {
				t.Fatal(err)
			}
			defer os.Remove(foreign)
			_, err := store.Transact(context.Background(), p, laneELedgerID(3), laneELedgerDigest(3), laneELedgerProposal(3))
			if err == nil {
				t.Error("admission silently passed an unaccountable foreign temporary")
			}
			if got, err := os.ReadFile(foreign); err != nil || string(got) != string(data) {
				t.Fatalf("recovery removed or changed another publisher's temporary: %q, %v", got, err)
			}
		})
	}
	laneELedgerAdmit(t, p, 3)
	laneELedgerRead(t, p, 3)
}

func TestLedgerConcurrentAdmissionsSelectDifferentTailsAndPublishACompleteChain(t *testing.T) {
	p := laneELedgerProject(t)
	laneELedgerAdmit(t, p, 1)
	laneELedgerRead(t, p, 1)
	const writers = 12
	start := make(chan struct{})
	errs := make(chan error, writers)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stopReading := make(chan struct{})
	readDone := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stopReading:
				readDone <- nil
				return
			default:
			}
			prefix, err := store.ReadPrefix(p)
			if err == nil {
				for i, b := range prefix {
					if b.Sequence != uint64(i+1) || (i > 0 && b.Predecessor != prefix[i-1].CommandID) {
						err = fmt.Errorf("concurrent read returned a broken chain at position %d", i)
						break
					}
				}
			}
			if err != nil {
				readDone <- err
				return
			}
		}
	}()
	var group sync.WaitGroup
	for n := 2; n <= writers+1; n++ {
		group.Add(1)
		go func(n int) {
			defer group.Done()
			<-start
			var selected model.ID
			b, err := store.Transact(ctx, p, laneELedgerID(n), laneELedgerDigest(n), func(prefix []model.Bundle) (model.Bundle, error) {
				selected = prefix[len(prefix)-1].CommandID
				return laneELedgerProposal(n)(prefix)
			})
			if err == nil && b.Predecessor != selected {
				err = fmt.Errorf("writer %d validated tail %s but was appended to %s", n, selected, b.Predecessor)
			}
			errs <- err
		}(n)
	}
	close(start)
	group.Wait()
	close(stopReading)
	if err := <-readDone; err != nil {
		t.Errorf("a concurrent reader could not select one complete prefix: %v", err)
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent admission failed: %v", err)
		}
	}
	prefix := laneELedgerRead(t, p, writers+1)
	seen := map[model.ID]bool{}
	for _, b := range prefix {
		if seen[b.CommandID] {
			t.Errorf("concurrent admissions published command %s more than once", b.CommandID)
		}
		seen[b.CommandID] = true
	}
}

func TestLedgerReaderCompletesWhileAnAdmissionHoldsItsRealOSLock(t *testing.T) {
	p := laneELedgerProject(t)
	laneELedgerAdmit(t, p, 1)
	laneELedgerRead(t, p, 1)
	held, release := make(chan struct{}), make(chan struct{})
	writer := make(chan error, 1)
	go func() {
		_, err := store.Transact(context.Background(), p, laneELedgerID(2), laneELedgerDigest(2), func(prefix []model.Bundle) (model.Bundle, error) {
			close(held)
			<-release
			return laneELedgerProposal(2)(prefix)
		})
		writer <- err
	}()
	select {
	case <-held:
	case err := <-writer:
		t.Fatalf("control writer never reached its proposal: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("control writer never acquired the admission lock")
	}
	defer func() {
		close(release)
		if err := <-writer; err != nil {
			t.Errorf("held admission failed after release: %v", err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := store.Transact(ctx, p, laneELedgerID(3), laneELedgerDigest(3), laneELedgerProposal(3)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second writer did not wait for the first writer's actual OS lock: %v", err)
	}
	reader := make(chan error, 1)
	go func() {
		prefix, err := store.ReadPrefix(p)
		if err == nil && (len(prefix) != 1 || prefix[0].CommandID != laneELedgerID(1)) {
			err = fmt.Errorf("unpublished proposal changed the visible prefix: %+v", prefix)
		}
		reader <- err
	}()
	select {
	case err := <-reader:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reader waited for the admission lock instead of returning the immutable prefix")
	}
}
