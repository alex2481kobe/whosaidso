package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"whosaidso/internal/model"
)

// ---- shared fixtures -----------------------------------------------------

func ledgerProject(t *testing.T) Project {
	t.Helper()
	root := t.TempDir()
	// Intake shares this package. Point HOME at a temporary too, so a test that
	// checks the ledger never reaches a real inbox.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(HomeEnv, "") // HOME alone places the WhoSaidSo home here
	return Project{ID: "team/project", Root: root, Ledger: filepath.Join(root, ".whosaidso", "events")}
}

// admissionID is a readable, deterministic ULID: 26 digits, first below '8'.
func admissionID(n int) model.ID { return model.ID(fmt.Sprintf("%026d", n)) }

func digestFor(n int) model.Digest {
	return model.HashBytes([]byte(fmt.Sprintf("request-%d", n)))
}

// proposeFor is the callback an admission supplies: authored content only. The
// clerical fields belong to the transaction.
func proposeFor(n int) func([]model.Bundle) (model.Bundle, error) {
	return func(prefix []model.Bundle) (model.Bundle, error) {
		return model.Bundle{
			Admitter: model.Actor{ID: "coordinator"},
			Packets:  []model.PacketRef{{CommandID: admissionID(1000 + n), Digest: digestFor(1000 + n)}},
			Events: []model.Event{{
				Type: "review.admit",
				Data: json.RawMessage(fmt.Sprintf(`{"note":"admission %d"}`, n)),
			}},
		}, nil
	}
}

// admitControl is the good control every refusal test opens with. A publisher
// that refuses everything passes every negative case while being broken, so the
// positive path is proven in the same test that proves the refusal.
func admitControl(t *testing.T, p Project, n int) model.Bundle {
	t.Helper()
	bundle, err := Transact(context.Background(), p, admissionID(n), digestFor(n), proposeFor(n))
	if err != nil {
		t.Fatalf("good admission control %d: %v", n, err)
	}
	if bundle.CommandID != admissionID(n) || bundle.RequestDigest != digestFor(n) {
		t.Fatalf("good admission control %d returned %+v", n, bundle)
	}
	return bundle
}

func admitSeries(t *testing.T, p Project, count int) []model.Bundle {
	t.Helper()
	bundles := make([]model.Bundle, 0, count)
	for n := 1; n <= count; n++ {
		bundles = append(bundles, admitControl(t, p, n))
	}
	return bundles
}

func readControl(t *testing.T, p Project, want int) []model.Bundle {
	t.Helper()
	bundles, err := ReadPrefix(p)
	if err != nil {
		t.Fatalf("good read control: %v", err)
	}
	if len(bundles) != want {
		t.Fatalf("good read control: got %d bundles, want %d", len(bundles), want)
	}
	requireChain(t, bundles)
	return bundles
}

func requireChain(t *testing.T, bundles []model.Bundle) {
	t.Helper()
	for i, bundle := range bundles {
		if bundle.Sequence != uint64(i)+1 {
			t.Fatalf("bundle %d carries sequence %d", i, bundle.Sequence)
		}
		if i == 0 {
			if bundle.Predecessor != "" {
				t.Fatalf("genesis bundle names predecessor %q", bundle.Predecessor)
			}
			continue
		}
		if bundle.Predecessor != bundles[i-1].CommandID {
			t.Fatalf("bundle %d names predecessor %q, previous is %q", i+1, bundle.Predecessor, bundles[i-1].CommandID)
		}
	}
}

func ledgerPath(t *testing.T, p Project, sequence uint64, command model.ID) string {
	t.Helper()
	name, err := model.BundleName(sequence, command)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(p.Ledger, name)
}

// rawBundle builds ledger bytes directly, which is the only way to construct
// the corruption the publisher will not produce.
func rawBundle(t *testing.T, p Project, sequence uint64, command, predecessor model.ID) []byte {
	t.Helper()
	data, err := model.Encode(model.Bundle{
		Version:       model.WireVersion,
		Project:       p.ID,
		Sequence:      sequence,
		CommandID:     command,
		Predecessor:   predecessor,
		RequestDigest: digestFor(int(sequence)),
		Admitter:      model.Actor{ID: "coordinator"},
		Events:        []model.Event{{Type: "review.admit", Data: json.RawMessage(`{"note":"raw"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeLedgerFile(t *testing.T, p Project, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(p.Ledger, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Ledger, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---- reading -------------------------------------------------------------

func TestReadPrefixEmptyLedgerIsEmptyNotBroken(t *testing.T) {
	p := ledgerProject(t)
	bundles, err := ReadPrefix(p)
	if err != nil || len(bundles) != 0 {
		t.Fatalf("missing ledger directory: %v, %d bundles", err, len(bundles))
	}
	if err := os.MkdirAll(p.Ledger, 0o755); err != nil {
		t.Fatal(err)
	}
	bundles, err = ReadPrefix(p)
	if err != nil || len(bundles) != 0 {
		t.Fatalf("empty ledger directory: %v, %d bundles", err, len(bundles))
	}
}

func TestDuplicateCommandIdentityRefusesReadsRetriesAndAdmissions(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprintf("changed_request_%t", changed), func(t *testing.T) {
			p := ledgerProject(t)
			original := admitControl(t, p, 1)
			second := admitControl(t, p, 2)
			readControl(t, p, 2)
			repeated := original
			repeated.Sequence, repeated.Predecessor = 3, second.CommandID
			requests := "matching request digests"
			if changed {
				repeated.RequestDigest = digestFor(99)
				requests = "different request digests"
			}
			data, err := model.Encode(repeated)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := model.DecodeBundle(data); err != nil {
				t.Fatalf("duplicate must be an individually valid bundle: %v", err)
			}
			path := ledgerPath(t, p, 3, repeated.CommandID)
			writeLedgerFile(t, p, filepath.Base(path), data)
			checkFault := func(err error) {
				t.Helper()
				requireFault(t, err, "ledger-corrupt")
				var fault *model.Fault
				if !errors.As(err, &fault) || fault.Path != path ||
					!strings.Contains(fault.Detail, string(original.CommandID)) ||
					!strings.Contains(fault.Detail, "sequences 1 and 3") ||
					!strings.Contains(fault.Detail, requests) {
					t.Fatalf("fault must locate and distinguish duplicate identities: %v", err)
				}
			}
			prefix, err := ReadPrefix(p)
			checkFault(err)
			if len(prefix) != 0 {
				t.Fatalf("ambiguous ledger returned %d bundles", len(prefix))
			}
			for _, request := range []struct {
				id     model.ID
				digest model.Digest
			}{
				{original.CommandID, original.RequestDigest},
				{repeated.CommandID, repeated.RequestDigest},
				{admissionID(4), digestFor(4)},
			} {
				bundle, err := Transact(context.Background(), p, request.id, request.digest, func([]model.Bundle) (model.Bundle, error) {
					t.Fatal("ambiguous ledger reached the proposal callback")
					return model.Bundle{}, nil
				})
				checkFault(err)
				if bundle.CommandID != "" {
					t.Fatalf("ambiguous ledger selected bundle %+v", bundle)
				}
			}
			requireNoTemporaries(t, p)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			readControl(t, p, 2)
			// Refusal released the lock and did not consume the next sequence.
			admitControl(t, p, 3)
		})
	}
}

func TestReadPrefixSelectsSequenceOrderNotEnumerationOrder(t *testing.T) {
	p := ledgerProject(t)
	admitSeries(t, p, 12)
	bundles := readControl(t, p, 12)
	for i, bundle := range bundles {
		if bundle.CommandID != admissionID(i+1) {
			t.Fatalf("position %d holds admission %q", i, bundle.CommandID)
		}
	}
	// Rebuild the same ledger writing the files in reverse, so creation order
	// disagrees with sequence order on any filesystem that preserves it.
	other := ledgerProject(t)
	for n := 12; n >= 1; n-- {
		name, err := model.BundleName(uint64(n), admissionID(n))
		if err != nil {
			t.Fatal(err)
		}
		predecessor := model.ID("")
		if n > 1 {
			predecessor = admissionID(n - 1)
		}
		writeLedgerFile(t, other, name, rawBundle(t, other, uint64(n), admissionID(n), predecessor))
	}
	reversed := readControl(t, other, 12)
	for i, bundle := range reversed {
		if bundle.CommandID != admissionID(i+1) {
			t.Fatalf("reverse-written ledger position %d holds admission %q", i, bundle.CommandID)
		}
	}
}

func TestReadPrefixRefusesUnpaddedSequenceName(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1)
	readControl(t, p, 1)
	// Zero padding to eight digits is what makes filename order and numeric
	// order agree. A name with a different width would sort as text against
	// every other name, so it is refused rather than interpreted.
	writeLedgerFile(t, p, fmt.Sprintf("%07d-%s.json", 2, admissionID(2)), rawBundle(t, p, 2, admissionID(2), admissionID(1)))
	_, err := ReadPrefix(p)
	requireFault(t, err, "ledger-corrupt")
}

func TestReadPrefixRefusesDuplicateSequence(t *testing.T) {
	p := ledgerProject(t)
	admitSeries(t, p, 2)
	readControl(t, p, 2)
	// The fork the lock exists to prevent, planted directly: two writers that
	// both read tail 1 and both published under their own transaction id.
	forked, err := model.BundleName(2, admissionID(99))
	if err != nil {
		t.Fatal(err)
	}
	writeLedgerFile(t, p, forked, rawBundle(t, p, 2, admissionID(99), admissionID(1)))
	_, err = ReadPrefix(p)
	requireFault(t, err, "ledger-fork")
	if !strings.Contains(err.Error(), forked) && !strings.Contains(err.Error(), string(admissionID(2))) {
		t.Fatalf("fork diagnostic names neither competing file: %v", err)
	}
	// And admission refuses too, rather than appending to a forked ledger.
	_, err = Transact(context.Background(), p, admissionID(3), digestFor(3), proposeFor(3))
	requireFault(t, err, "ledger-fork")
}

func TestReadPrefixRefusesFilenameBodyDisagreement(t *testing.T) {
	p := ledgerProject(t)
	admitSeries(t, p, 2)
	readControl(t, p, 2)
	// Same bytes, renamed. The filename now claims an identity the body denies.
	from := ledgerPath(t, p, 2, admissionID(2))
	to := ledgerPath(t, p, 2, admissionID(98))
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
	_, err := ReadPrefix(p)
	requireFault(t, err, "ledger-corrupt")
}

func TestReadPrefixRefusesBrokenPredecessor(t *testing.T) {
	p := ledgerProject(t)
	for n := 1; n <= 3; n++ {
		predecessor := model.ID("")
		if n > 1 {
			predecessor = admissionID(n - 1)
		}
		name, err := model.BundleName(uint64(n), admissionID(n))
		if err != nil {
			t.Fatal(err)
		}
		writeLedgerFile(t, p, name, rawBundle(t, p, uint64(n), admissionID(n), predecessor))
	}
	readControl(t, p, 3)
	// Sequence numbers still run 1, 2, 3. Only the chain disagrees, which is
	// what catches a bundle appended to a tail that is not the one before it.
	name, err := model.BundleName(3, admissionID(3))
	if err != nil {
		t.Fatal(err)
	}
	writeLedgerFile(t, p, name, rawBundle(t, p, 3, admissionID(3), admissionID(77)))
	_, err = ReadPrefix(p)
	requireFault(t, err, "ledger-discontinuity")
}

func TestReadPrefixRefusesGapRatherThanTruncating(t *testing.T) {
	p := ledgerProject(t)
	admitSeries(t, p, 3)
	readControl(t, p, 3)
	if err := os.Remove(ledgerPath(t, p, 2, admissionID(2))); err != nil {
		t.Fatal(err)
	}
	// Returning bundle 1 here would be a true count of the wrong thing: a
	// replay that silently drops two admitted transactions and calls itself
	// complete.
	_, err := ReadPrefix(p)
	requireFault(t, err, "ledger-discontinuity")
}

func TestReadPrefixRefusesForeignEntriesAndIgnoresDotfiles(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1)
	readControl(t, p, 1)
	// A dotfile cannot be a bundle, so it is ignored. The lock itself is one.
	writeLedgerFile(t, p, ".DS_Store", []byte("finder"))
	readControl(t, p, 1)
	for _, name := range []string{"notes.txt", "00000002-lowercase.json", "bundle.json"} {
		writeLedgerFile(t, p, name, []byte("{}"))
		_, err := ReadPrefix(p)
		requireFault(t, err, "ledger-corrupt")
		if err := os.Remove(filepath.Join(p.Ledger, name)); err != nil {
			t.Fatal(err)
		}
	}
	readControl(t, p, 1)
	// A directory shaped like a bundle is not a bundle either.
	if err := os.Mkdir(ledgerPath(t, p, 2, admissionID(2)), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := ReadPrefix(p)
	requireFault(t, err, "ledger-corrupt")
}

func TestReadPrefixIgnoresPublicationTemporaries(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1)
	name, err := model.BundleName(2, admissionID(2))
	if err != nil {
		t.Fatal(err)
	}
	// A crash between the temporary and the rename leaves this behind. It is
	// not a record, and a read must not answer as though it were.
	writeLedgerFile(t, p, name+publicationSuffix, rawBundle(t, p, 2, admissionID(2), admissionID(1)))
	readControl(t, p, 1)
	// A temporary with any other name is not this publisher's, so it is neither
	// read nor silently ignored.
	writeLedgerFile(t, p, "editor-swap.tmp", []byte("{}"))
	_, err = ReadPrefix(p)
	requireFault(t, err, "ledger-corrupt")
}

func TestReadPrefixRefusesAnotherProjectsBundle(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1)
	readControl(t, p, 1)
	other := Project{ID: "team/other", Root: p.Root, Ledger: p.Ledger}
	name, err := model.BundleName(2, admissionID(2))
	if err != nil {
		t.Fatal(err)
	}
	writeLedgerFile(t, p, name, rawBundle(t, other, 2, admissionID(2), admissionID(1)))
	_, err = ReadPrefix(p)
	requireFault(t, err, "ledger-corrupt")
}

func TestReadPrefixRefusesMalformedBody(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1)
	readControl(t, p, 1)
	name, err := model.BundleName(2, admissionID(2))
	if err != nil {
		t.Fatal(err)
	}
	// Truncated bytes: a publication that was read from disk half written would
	// look like this, and skipping it would lose a transaction.
	valid := rawBundle(t, p, 2, admissionID(2), admissionID(1))
	writeLedgerFile(t, p, name, valid[:len(valid)/2])
	_, err = ReadPrefix(p)
	requireFault(t, err, "ledger-corrupt")
}

func TestReadPrefixNeedsItsProjectIdentity(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1)
	readControl(t, p, 1)
	// Without the declared id a read cannot tell whose bundles it is reading.
	_, err := ReadPrefix(Project{Root: p.Root, Ledger: p.Ledger})
	requireFault(t, err, "invalid-field")
	_, err = ReadPrefix(Project{ID: p.ID, Root: p.Root})
	requireFault(t, err, "invalid-field")
}

func TestReadPrefixHoldsNoAdmissionLock(t *testing.T) {
	p := ledgerProject(t)
	admitSeries(t, p, 2)
	held, err := lockAcquire(filepath.Join(p.Ledger, lockName))
	if err != nil {
		t.Fatalf("good lock control: %v", err)
	}
	defer lockRelease(held)
	// The control that makes the next assertion mean something: the lock really
	// is held, so a second writer cannot take it.
	if _, err := lockAcquire(filepath.Join(p.Ledger, lockName)); err != errLockBusy {
		t.Fatalf("expected a busy lock, got %v", err)
	}
	// A read takes nothing and therefore waits for nothing.
	done := make(chan []model.Bundle, 1)
	go func() {
		bundles, err := ReadPrefix(p)
		if err != nil {
			done <- nil
			return
		}
		done <- bundles
	}()
	select {
	case bundles := <-done:
		if len(bundles) != 2 {
			t.Fatalf("read under a held lock returned %d bundles", len(bundles))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReadPrefix waited for the admission lock")
	}
	// And an admission does wait, which is what the lock is for.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := Transact(ctx, p, admissionID(3), digestFor(3), proposeFor(3)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("admission did not wait for the held lock: %v", err)
	}
	readControl(t, p, 2)
}

func TestReadPrefixDuringPublicationSeesOnlyWholeLedgers(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1)
	// A reader takes no lock, so it enumerates the directory while a publisher
	// is renaming into it. Publication is one rename of one immutable file, so
	// every listing a reader can observe is a whole ledger, never a partial one.
	stop := make(chan struct{})
	failures := make(chan error, 1)
	var reading sync.WaitGroup
	reading.Add(1)
	go func() {
		defer reading.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			bundles, err := ReadPrefix(p)
			if err != nil {
				select {
				case failures <- err:
				default:
				}
				return
			}
			for i, bundle := range bundles {
				if bundle.Sequence != uint64(i)+1 {
					select {
					case failures <- fmt.Errorf("position %d holds sequence %d", i, bundle.Sequence):
					default:
					}
					return
				}
				if i > 0 && bundle.Predecessor != bundles[i-1].CommandID {
					select {
					case failures <- fmt.Errorf("position %d breaks the chain", i):
					default:
					}
					return
				}
			}
		}
	}()
	for n := 2; n <= 11; n++ {
		admitControl(t, p, n)
	}
	close(stop)
	reading.Wait()
	select {
	case err := <-failures:
		t.Fatalf("a read during publication saw a broken ledger: %v", err)
	default:
	}
	readControl(t, p, 11)
}
