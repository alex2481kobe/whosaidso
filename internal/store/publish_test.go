package store

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"datum/internal/model"
)

// ---- the single writer ---------------------------------------------------

func TestTransactAssignsTheClericalFields(t *testing.T) {
	p := ledgerProject(t)
	first := admitControl(t, p, 1)
	if first.Sequence != 1 || first.Predecessor != "" || first.Project != p.ID || first.Version != model.WireVersion {
		t.Fatalf("genesis bundle: %+v", first)
	}
	second := admitControl(t, p, 2)
	if second.Sequence != 2 || second.Predecessor != first.CommandID {
		t.Fatalf("second bundle: %+v", second)
	}
	if second.RecordedAt.IsZero() || second.RecordedAt.Location() != time.UTC {
		t.Fatalf("recorded at %v", second.RecordedAt)
	}
	// The published bytes, not just the returned value.
	readControl(t, p, 2)
	if _, err := os.Lstat(ledgerPath(t, p, 2, admissionID(2))); err != nil {
		t.Fatalf("published bundle is not on disk: %v", err)
	}
}

func TestConcurrentAdmissionsCannotFork(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1)
	// Forty one writers against tail one: the contract's own counterexample,
	// where two coordinator calls both read tail 41, both validate and both
	// publish under different filenames so neither overwrite fails.
	const writers = 41
	var wg sync.WaitGroup
	start := make(chan struct{})
	bundles := make([]model.Bundle, writers)
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			n := i + 2
			<-start
			bundles[i], errs[i] = Transact(context.Background(), p, admissionID(n), digestFor(n), proposeFor(n))
		}(i)
	}
	close(start)
	wg.Wait()
	holder := map[uint64]model.ID{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
		if prior, taken := holder[bundles[i].Sequence]; taken {
			t.Fatalf("sequence %d handed to both %s and %s", bundles[i].Sequence, prior, bundles[i].CommandID)
		}
		holder[bundles[i].Sequence] = bundles[i].CommandID
	}
	published := readControl(t, p, writers+1)
	seen := map[model.ID]bool{}
	for _, bundle := range published {
		if seen[bundle.CommandID] {
			t.Fatalf("admission %s published twice", bundle.CommandID)
		}
		seen[bundle.CommandID] = true
	}
	for n := 1; n <= writers+1; n++ {
		if !seen[admissionID(n)] {
			t.Fatalf("admission %s is missing from the ledger", admissionID(n))
		}
	}
}

func TestConcurrentProcessesCannotFork(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1)
	// Goroutines share one process. An OS lock has to hold between processes,
	// which is where the real coordinator and a real lane meet.
	const writers = 5
	children := make([]*exec.Cmd, 0, writers)
	for n := 2; n <= writers+1; n++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestLedgerAdmitChild$")
		cmd.Env = append(os.Environ(), "DATUM_LEDGER_ADMIT="+strconv.Itoa(n), "DATUM_LEDGER_ROOT="+p.Root)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		children = append(children, cmd)
	}
	for i, cmd := range children {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("child %d: %v", i, err)
		}
	}
	published := readControl(t, p, writers+1)
	for n := 1; n <= writers+1; n++ {
		found := false
		for _, bundle := range published {
			found = found || bundle.CommandID == admissionID(n)
		}
		if !found {
			t.Fatalf("admission %s is missing from the ledger", admissionID(n))
		}
	}
}

func TestLedgerAdmitChild(t *testing.T) {
	n, err := strconv.Atoi(os.Getenv("DATUM_LEDGER_ADMIT"))
	if err != nil {
		return
	}
	p := childProject()
	if _, err := Transact(context.Background(), p, admissionID(n), digestFor(n), proposeFor(n)); err != nil {
		t.Fatalf("child admission %d: %v", n, err)
	}
}

func childProject() Project {
	root := os.Getenv("DATUM_LEDGER_ROOT")
	return Project{ID: "team/project", Root: root, Ledger: filepath.Join(root, "record", "events")}
}

// ---- idempotency and the lost acknowledgement ----------------------------

func TestLostAcknowledgementKeepsTheBundleAdmitted(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1)
	disk := systemPublishIO()
	realSync := disk.sync
	disk.sync = func(f *os.File) error {
		if f.Name() == p.Ledger {
			return errors.New("simulated directory flush failure")
		}
		return realSync(f)
	}
	_, err := transact(context.Background(), p, admissionID(2), digestFor(2), proposeFor(2), disk)
	requireFault(t, err, "uncertain-ack")
	var uncertain *UncertainAck
	if !errors.As(err, &uncertain) {
		t.Fatalf("lost acknowledgement is not a typed uncertain ack: %#v", err)
	}
	if uncertain.CommandID != admissionID(2) {
		t.Fatalf("uncertain ack carries command %q", uncertain.CommandID)
	}
	// Published. An acknowledgement nobody received is not an admission that
	// did not happen.
	published := readControl(t, p, 2)
	if published[1].CommandID != admissionID(2) {
		t.Fatalf("second bundle is %q", published[1].CommandID)
	}
	// Retrying while the flush still fails inspects the publication again. It
	// must not publish a second transaction carrying the same events.
	_, err = transact(context.Background(), p, admissionID(2), digestFor(2), refuseToPropose(t), disk)
	requireFault(t, err, "uncertain-ack")
	readControl(t, p, 2)
	// Once the flush works, the retry returns the bundle that is already there.
	retry, err := Transact(context.Background(), p, admissionID(2), digestFor(2), refuseToPropose(t))
	if err != nil {
		t.Fatalf("retry after a lost acknowledgement: %v", err)
	}
	if retry.CommandID != published[1].CommandID || retry.Sequence != 2 || retry.RecordedAt != published[1].RecordedAt {
		t.Fatalf("retry returned a different bundle: %+v", retry)
	}
	readControl(t, p, 2)
}

// refuseToPropose fails the test if the transaction callback runs. A retry that
// reaches the callback is a retry that is about to publish a second time.
func refuseToPropose(t *testing.T) func([]model.Bundle) (model.Bundle, error) {
	return func([]model.Bundle) (model.Bundle, error) {
		t.Helper()
		t.Fatal("the transaction callback ran on a retry of an already published admission")
		return model.Bundle{}, nil
	}
}

func TestRetryWithDifferentContentIsRefused(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1)
	// Control: the identical request under the identical id returns the bundle.
	again, err := Transact(context.Background(), p, admissionID(1), digestFor(1), refuseToPropose(t))
	if err != nil || again.Sequence != 1 {
		t.Fatalf("identical retry control: %+v, %v", again, err)
	}
	_, err = Transact(context.Background(), p, admissionID(1), digestFor(99), proposeFor(99))
	requireFault(t, err, "conflict")
	readControl(t, p, 1)
}

// ---- what the callback may and may not do --------------------------------

func TestCallbackFailureWritesNoBundle(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1)
	refusal := errors.New("the gate refused this transaction")
	_, err := Transact(context.Background(), p, admissionID(2), digestFor(2), func([]model.Bundle) (model.Bundle, error) {
		return model.Bundle{}, refusal
	})
	if !errors.Is(err, refusal) {
		t.Fatalf("callback refusal was not returned: %v", err)
	}
	readControl(t, p, 1)
	requireNoTemporaries(t, p)
	// The lock was released, so the next writer is not blocked by the refusal.
	admitControl(t, p, 2)
}

func TestCallerCannotSupplyATailAsAuthority(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1)
	// Control: a proposal that carries only authored content is published.
	admitControl(t, p, 2)
	stale := []model.Bundle{
		{Sequence: 2},
		{Predecessor: admissionID(1)},
		{CommandID: admissionID(3)},
		{RequestDigest: digestFor(3)},
		{Project: p.ID},
		{Version: model.WireVersion},
		{RecordedAt: time.Now().UTC()},
	}
	for i, carried := range stale {
		proposal := carried
		proposal.Admitter = model.Actor{ID: "coordinator"}
		proposal.Events = authoredEvents(t)
		_, err := Transact(context.Background(), p, admissionID(3), digestFor(3), func([]model.Bundle) (model.Bundle, error) {
			return proposal, nil
		})
		requireFault(t, err, "invalid-field")
		if len(readControl(t, p, 2)) != 2 {
			t.Fatalf("stale proposal %d published anyway", i)
		}
	}
	admitControl(t, p, 3)
}

// authoredEvents is the content half of a proposal, without the clerical
// fields the transaction assigns.
func authoredEvents(t *testing.T) []model.Event {
	t.Helper()
	bundle, err := proposeFor(1)(nil)
	if err != nil {
		t.Fatal(err)
	}
	return bundle.Events
}

func TestCallbackSeesTheTailItWillBeAppendedTo(t *testing.T) {
	p := ledgerProject(t)
	admitSeries(t, p, 3)
	var seen []model.Bundle
	published, err := Transact(context.Background(), p, admissionID(4), digestFor(4), func(prefix []model.Bundle) (model.Bundle, error) {
		seen = prefix
		// Mutating the callback's copy must not reach the transaction.
		if len(prefix) > 0 {
			prefix[len(prefix)-1].CommandID = admissionID(88)
		}
		bundle, _ := proposeFor(4)(nil)
		return bundle, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 {
		t.Fatalf("callback saw %d bundles", len(seen))
	}
	if published.Predecessor != admissionID(3) {
		t.Fatalf("a mutated prefix reached the published predecessor: %q", published.Predecessor)
	}
	readControl(t, p, 4)
}

// ---- publication ---------------------------------------------------------

func TestPublishRefusesToOverwriteAnAdmittedBundle(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1)
	planted := []byte("bytes that were already admitted\n")
	_, err := Transact(context.Background(), p, admissionID(2), digestFor(2), func([]model.Bundle) (model.Bundle, error) {
		// Publish the destination out from under the transaction while it holds
		// the lock. Nothing should be able to do this, which is exactly why the
		// publisher checks instead of trusting.
		if err := os.WriteFile(ledgerPath(t, p, 2, admissionID(2)), planted, 0o644); err != nil {
			return model.Bundle{}, err
		}
		bundle, _ := proposeFor(2)(nil)
		return bundle, nil
	})
	requireFault(t, err, "ledger-fork")
	kept, readErr := os.ReadFile(ledgerPath(t, p, 2, admissionID(2)))
	if readErr != nil || string(kept) != string(planted) {
		t.Fatalf("the existing bundle was overwritten: %q, %v", kept, readErr)
	}
	requireNoTemporaries(t, p)
}

func TestInjectedFailuresPublishNothing(t *testing.T) {
	cases := map[string]func(publishIO) publishIO{
		"create": func(disk publishIO) publishIO {
			disk.create = func(string) (*os.File, error) { return nil, errors.New("simulated create failure") }
			return disk
		},
		"write": func(disk publishIO) publishIO {
			disk.write = func(*os.File, []byte) (int, error) { return 0, errors.New("simulated write failure") }
			return disk
		},
		"short-write": func(disk publishIO) publishIO {
			real := disk.write
			disk.write = func(f *os.File, data []byte) (int, error) { return real(f, data[:len(data)/2]) }
			return disk
		},
		"file-flush": func(disk publishIO) publishIO {
			real := disk.sync
			disk.sync = func(f *os.File) error {
				if strings.HasSuffix(f.Name(), publicationSuffix) {
					return errors.New("simulated file flush failure")
				}
				return real(f)
			}
			return disk
		},
		"rename": func(disk publishIO) publishIO {
			disk.rename = func(string, string) error { return errors.New("simulated rename failure") }
			return disk
		},
	}
	for name, inject := range cases {
		t.Run(name, func(t *testing.T) {
			p := ledgerProject(t)
			admitControl(t, p, 1)
			_, err := transact(context.Background(), p, admissionID(2), digestFor(2), proposeFor(2), inject(systemPublishIO()))
			requireFault(t, err, "io")
			// Before the rename there is no new canonical bundle, whatever is
			// on disk.
			readControl(t, p, 1)
			requireNoTemporaries(t, p)
			admitControl(t, p, 2)
		})
	}
}

// ---- recovery ------------------------------------------------------------

func TestRecoveryRemovesOnlyPublicationTemporaries(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1)
	// A live producer's intake temporary, in the tree recovery must never enter.
	inbox, err := IntakeDir(p)
	if err != nil {
		t.Fatal(err)
	}
	producing := filepath.Join(inbox, string(admissionID(50))+"-live.tmp")
	if err := os.MkdirAll(producing, 0o700); err != nil {
		t.Fatal(err)
	}
	// This publisher's own interrupted work, at the name the next admission
	// will want to use.
	name, err := model.BundleName(2, admissionID(2))
	if err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(p.Ledger, name+publicationSuffix)
	if err := os.WriteFile(stale, []byte("half a bundle"), 0o644); err != nil {
		t.Fatal(err)
	}
	admitControl(t, p, 2)
	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Fatalf("recovery left its own interrupted publication: %v", err)
	}
	readControl(t, p, 2)
	if _, err := os.Lstat(producing); err != nil {
		t.Fatalf("recovery reached into a live producer's intake temporary: %v", err)
	}
}

func TestRecoveryLeavesTemporariesItDidNotWrite(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1)
	foreign := filepath.Join(p.Ledger, "editor-swap.tmp")
	if err := os.WriteFile(foreign, []byte("not ours"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Not ours to delete and not a record, so admission stops rather than
	// tidying up somebody else's file or reading past it.
	_, err := Transact(context.Background(), p, admissionID(2), digestFor(2), proposeFor(2))
	requireFault(t, err, "ledger-corrupt")
	if _, err := os.Lstat(foreign); err != nil {
		t.Fatalf("recovery removed a file it did not write: %v", err)
	}
	if err := os.Remove(foreign); err != nil {
		t.Fatal(err)
	}
	admitControl(t, p, 2)
}

func requireNoTemporaries(t *testing.T, p Project) {
	t.Helper()
	entries, err := os.ReadDir(p.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), publicationSuffix) {
			t.Fatalf("a temporary survived: %s", entry.Name())
		}
	}
}

// ---- crash schedule ------------------------------------------------------

// crashPublishIO stops the process at one point in the publication order. Each
// point has one classified restart outcome, asserted by TestCrashSchedule.
func crashPublishIO(point, dir string) publishIO {
	disk := systemPublishIO()
	realCreate, realWrite, realSync, realRename := disk.create, disk.write, disk.sync, disk.rename
	disk.create = func(path string) (*os.File, error) {
		f, err := realCreate(path)
		if err == nil && point == "after-create" {
			os.Exit(73)
		}
		return f, err
	}
	disk.write = func(f *os.File, data []byte) (int, error) {
		if point == "after-partial-write" {
			n, err := realWrite(f, data[:len(data)/2])
			if err == nil {
				err = realSync(f)
			}
			if err != nil {
				return n, err
			}
			os.Exit(73)
		}
		return realWrite(f, data)
	}
	disk.sync = func(f *os.File) error {
		if err := realSync(f); err != nil {
			return err
		}
		if point == "after-flush" && strings.HasSuffix(f.Name(), publicationSuffix) {
			os.Exit(73)
		}
		if point == "after-directory-flush" && f.Name() == dir {
			os.Exit(73)
		}
		return nil
	}
	disk.rename = func(from, to string) error {
		if point == "before-rename" {
			os.Exit(73)
		}
		if err := realRename(from, to); err != nil {
			return err
		}
		if point == "after-rename" {
			os.Exit(73)
		}
		return nil
	}
	return disk
}

func TestCrashSchedule(t *testing.T) {
	for _, tc := range []struct {
		point     string
		published bool
	}{
		{"after-create", false},
		{"after-partial-write", false},
		{"after-flush", false},
		{"before-rename", false},
		{"after-rename", true},
		{"after-directory-flush", true},
	} {
		t.Run(tc.point, func(t *testing.T) {
			p := ledgerProject(t)
			admitControl(t, p, 1)
			cmd := exec.Command(os.Args[0], "-test.run=^TestLedgerCrashChild$")
			cmd.Env = append(os.Environ(), "DATUM_LEDGER_CRASH="+tc.point, "DATUM_LEDGER_ROOT="+p.Root)
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 73 {
				t.Fatalf("child did not stop at %s: %s, %v", tc.point, output, err)
			}
			temporary := ledgerPath(t, p, 2, admissionID(2)) + publicationSuffix
			_, temporaryErr := os.Lstat(temporary)
			if tc.published {
				// After the rename the bundle is admitted, acknowledged or not.
				published := readControl(t, p, 2)
				if published[1].CommandID != admissionID(2) {
					t.Fatalf("published bundle is %q", published[1].CommandID)
				}
				if temporaryErr == nil {
					t.Fatal("the temporary survived a completed rename")
				}
				retry, err := Transact(context.Background(), p, admissionID(2), digestFor(2), refuseToPropose(t))
				if err != nil {
					t.Fatalf("retry after %s: %v", tc.point, err)
				}
				if retry.Sequence != 2 || retry.CommandID != admissionID(2) {
					t.Fatalf("retry returned %+v", retry)
				}
				readControl(t, p, 2)
				return
			}
			// Before the rename nothing was admitted, and the interrupted
			// publication is still on disk under its temporary name.
			readControl(t, p, 1)
			if temporaryErr != nil {
				t.Fatalf("crash at %s left no temporary: %v", tc.point, temporaryErr)
			}
			// The same admission has never been published, so retrying it
			// publishes it now. Recovery has to clear the temporary first or
			// the exclusive create would fail on its own leftovers.
			retry := admitControl(t, p, 2)
			if retry.Sequence != 2 {
				t.Fatalf("retry after %s landed at sequence %d", tc.point, retry.Sequence)
			}
			if _, err := os.Lstat(temporary); !os.IsNotExist(err) {
				t.Fatalf("recovery left the interrupted publication: %v", err)
			}
			readControl(t, p, 2)
		})
	}
}

func TestLedgerCrashChild(t *testing.T) {
	point := os.Getenv("DATUM_LEDGER_CRASH")
	if point == "" {
		return
	}
	p := childProject()
	if _, err := transact(context.Background(), p, admissionID(2), digestFor(2), proposeFor(2), crashPublishIO(point, p.Ledger)); err != nil {
		t.Fatalf("child transaction: %v", err)
	}
	// Reaching this line means the crash point was never hit. Exit differently
	// so the parent sees a missed crash rather than a satisfied expectation.
	os.Exit(74)
}

// ---- the lock ------------------------------------------------------------

func TestAdmissionLockIsNotInheritedByChildren(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1)
	// Control: lockAcquire really detects a held lock. Without this, a lock
	// that always succeeded would pass the rest of this test.
	held, err := lockAcquire(filepath.Join(p.Ledger, lockName))
	if err != nil {
		t.Fatalf("good lock control: %v", err)
	}
	if _, err := lockAcquire(filepath.Join(p.Ledger, lockName)); err != errLockBusy {
		t.Fatalf("expected a busy lock, got %v", err)
	}
	if err := lockRelease(held); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	cmd := exec.Command(os.Args[0], "-test.run=^TestLedgerLockChild$")
	cmd.Env = append(os.Environ(), "DATUM_LEDGER_LOCK_CHILD=1", "DATUM_LEDGER_ROOT="+p.Root, "DATUM_LEDGER_PIDFILE="+pidFile)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("lock child: %s, %v", output, err)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("grandchild never started: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	grandchild, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	defer grandchild.Kill()
	if err := grandchild.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("the grandchild is already gone, so this proves nothing: %v", err)
	}
	// The writer that held the lock is dead. Its long running grandchild is
	// not, and must not be holding admission hostage.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	lock, err := holdAdmissionLock(ctx, p.Ledger)
	if err != nil {
		t.Fatalf("a wrapped child process inherited the admission lock: %v", err)
	}
	if err := lockRelease(lock); err != nil {
		t.Fatal(err)
	}
	admitControl(t, p, 2)
}

func TestLedgerLockChild(t *testing.T) {
	if os.Getenv("DATUM_LEDGER_LOCK_CHILD") == "" {
		return
	}
	p := childProject()
	if _, err := lockAcquire(filepath.Join(p.Ledger, lockName)); err != nil {
		t.Fatalf("child lock: %v", err)
	}
	pidFile := os.Getenv("DATUM_LEDGER_PIDFILE")
	grandchild := exec.Command(os.Args[0], "-test.run=^TestLedgerLockGrandchild$")
	grandchild.Env = append(os.Environ(), "DATUM_LEDGER_GRANDCHILD=1", "DATUM_LEDGER_PIDFILE="+pidFile)
	// No inherited pipes, or the parent's CombinedOutput would wait for the
	// grandchild instead of for this process.
	grandchild.Stdout, grandchild.Stderr = nil, nil
	if err := grandchild.Start(); err != nil {
		t.Fatalf("spawn grandchild: %v", err)
	}
	for i := 0; i < 500; i++ {
		if raw, err := os.ReadFile(pidFile); err == nil && len(strings.TrimSpace(string(raw))) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Exit while still holding the lock. Releasing it is the operating
	// system's job, and the grandchild must not be able to keep it alive.
	os.Exit(0)
}

func TestLedgerLockGrandchild(t *testing.T) {
	if os.Getenv("DATUM_LEDGER_GRANDCHILD") == "" {
		return
	}
	if err := os.WriteFile(os.Getenv("DATUM_LEDGER_PIDFILE"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	// Stand in for a wrapped instrument process that outlives the admission.
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

// ---- arguments and cancellation ------------------------------------------

func TestTransactRefusesUnusableArguments(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1)
	for name, tc := range map[string]struct {
		project Project
		id      model.ID
		digest  model.Digest
		propose func([]model.Bundle) (model.Bundle, error)
		code    string
	}{
		"no project id":  {Project{Ledger: p.Ledger}, admissionID(2), digestFor(2), proposeFor(2), "invalid-field"},
		"no ledger":      {Project{ID: p.ID}, admissionID(2), digestFor(2), proposeFor(2), "invalid-field"},
		"bad admission":  {p, model.ID("not-a-ulid"), digestFor(2), proposeFor(2), "invalid-field"},
		"bad digest":     {p, admissionID(2), model.Digest("nope"), proposeFor(2), "invalid-field"},
		"no callback":    {p, admissionID(2), digestFor(2), nil, "invalid-field"},
		"empty proposal": {p, admissionID(2), digestFor(2), func([]model.Bundle) (model.Bundle, error) { return model.Bundle{}, nil }, "invalid-field"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Transact(context.Background(), tc.project, tc.id, tc.digest, tc.propose)
			requireFault(t, err, tc.code)
			readControl(t, p, 1)
			requireNoTemporaries(t, p)
		})
	}
	admitControl(t, p, 2)
}

func TestTransactStopsOnACancelledContext(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Transact(ctx, p, admissionID(2), digestFor(2), proposeFor(2))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled admission returned %v", err)
	}
	readControl(t, p, 1)
	requireNoTemporaries(t, p)
	admitControl(t, p, 2)
}

func TestPublicationIsRefusedWhereDurabilityIsUntested(t *testing.T) {
	// The refusal and the platforms it names travel together, so a port cannot
	// quietly acquire a durability claim by adding a build tag.
	err := publicationDurability()
	if err != nil {
		requireFault(t, err, "unsupported-platform")
		return
	}
	p := ledgerProject(t)
	admitControl(t, p, 1)
	if _, statErr := os.Lstat(filepath.Join(p.Ledger, lockName)); statErr != nil {
		t.Fatalf("no admission lock file: %v", statErr)
	}
}
