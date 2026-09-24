package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"whosaidso/internal/model"
)

// errLockBusy means another writer holds the admission lock right now. It is
// not a failure: it is the single-writer rule working.
var errLockBusy = errors.New("admission lock is held")

// UncertainAck reports a bundle that IS published and whose durability was not
// confirmed.
//
// This is not a failed admission and the bundle must not be republished. The
// file exists under its final name, a restart will replay it, and a second
// transaction carrying the same events would put those events in the ledger
// twice. Retrying the identical admission id inspects the existing publication
// and reflushes it, which is why the command id travels with the diagnostic
// instead of only appearing in its text.
type UncertainAck struct {
	*model.Fault
	CommandID model.ID
}

func (u *UncertainAck) Unwrap() error { return u.Fault }

func uncertainPublication(id model.ID, path string, err error) error {
	return &UncertainAck{
		CommandID: id,
		Fault: storeFault("uncertain-ack", path, fmt.Sprintf(
			"admission %s is published and its durability was not confirmed, so retry the identical admission to inspect it rather than publishing again: %v", id, err)),
	}
}

// publishIO is the durability boundary the crash and failure tests drive. There
// is no global hook: each transaction carries its own, so one test can fail a
// rename without reaching into another writer.
type publishIO struct {
	create func(string) (*os.File, error)
	write  func(*os.File, []byte) (int, error)
	sync   func(*os.File) error
	rename func(string, string) error
	// beforeLock runs between the first home check and taking the lock; only
	// tests set it, to rebind inside that window.
	beforeLock func()
}

func systemPublishIO() publishIO {
	return publishIO{
		create: func(path string) (*os.File, error) {
			// Exclusive creation. If this name already exists the previous
			// publication was interrupted, and recovery, not a silent
			// truncation, is what deals with that.
			return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		},
		write:  (*os.File).Write,
		sync:   (*os.File).Sync,
		rename: os.Rename,
	}
}

// Transact runs one admission as the single writer.
//
// The lock is held from the moment the tail is selected through the final
// directory flush, so the prefix the callback validates against is the prefix
// the bundle is appended to. The callback cannot pass a tail in: it receives the
// one read here, under the lock, and a proposal that fills in its own sequence
// or predecessor is refused. A tail read before the lock is a tail another
// writer may already have overtaken, and believing one is how the ledger forks.
//
// Transact checks for a prior admission under this id before calling the
// callback, so a retry after a lost acknowledgement returns the existing bundle
// rather than publishing a second transaction. A callback that fails writes
// nothing.
//
// What Transact acknowledges is durable filesystem publication and nothing
// else. It runs no Git command and it cannot make a commit exist. The
// coordinator commits admitted bundles with the project at its next serialized
// commit window, and if commits are denied the durable ledger stays exactly as
// it is, with the uncommitted delivery condition recorded as a fact somewhere
// else. Adding a commit step here would let a refused commit look like a
// refused admission, or worse, like an admission that never happened.
func Transact(ctx context.Context, project Project, admissionID model.ID, requestDigest model.Digest, propose func(prefix []model.Bundle) (model.Bundle, error)) (model.Bundle, error) {
	return transact(ctx, project, admissionID, requestDigest, propose, systemPublishIO())
}

// Admission is one admission whose request digest is known only under the lock.
//
// RetryDigest runs when a bundle is already published under ID. It answers
// what digest this request would carry against that bundle, from the bundle
// alone, so an identical retry is recognised without rereading anything the
// original admission consumed, such as machine-local intake. Propose runs only
// when nothing is published under ID, receives the prefix selected under the
// lock (its snapshot, and its bundles on demand) and returns the request digest
// together with the proposal.
type Admission struct {
	ID          model.ID
	RetryDigest func(published model.Bundle) (model.Digest, error)
	Propose     func(prefix State) (model.Digest, model.Bundle, error)
}

// TransactAdmission is Transact for a caller that must read its inputs under
// the admission lock before it can name its request digest.
func TransactAdmission(ctx context.Context, project Project, admission Admission) (model.Bundle, error) {
	return transactAdmission(ctx, project, admission, systemPublishIO())
}

func transact(ctx context.Context, project Project, admissionID model.ID, requestDigest model.Digest, propose func([]model.Bundle) (model.Bundle, error), disk publishIO) (model.Bundle, error) {
	if !model.ValidDigest(requestDigest) {
		// The digest is what makes a retry recognisable as the same request, so
		// an admission without one cannot be idempotent.
		return model.Bundle{}, storeFault("invalid-field", "admission.request_digest", "not lowercase sha-256 hex")
	}
	admission := Admission{ID: admissionID, RetryDigest: func(model.Bundle) (model.Digest, error) { return requestDigest, nil }}
	if propose != nil {
		admission.Propose = func(prefix State) (model.Digest, model.Bundle, error) {
			bundles, err := prefix.Bundles()
			if err != nil {
				return "", model.Bundle{}, err
			}
			// The callback gets its own copy. What it does to that slice cannot
			// change the prefix this transaction assigns its sequence from.
			proposed, err := propose(append([]model.Bundle(nil), bundles...))
			return requestDigest, proposed, err
		}
	}
	return transactAdmission(ctx, project, admission, disk)
}

// transactAdmission publishes under the lock, then, with the lock released,
// refreshes the snapshot cache to the state including the new bundle. The
// ledger is published and durable first; the cache refresh is best effort and
// its failure is never the admission's, so a crash or error between the two
// leaves an image one bundle behind, which the next command catches up.
func transactAdmission(ctx context.Context, project Project, admission Admission, disk publishIO) (model.Bundle, error) {
	bundle, after, err := admitLocked(ctx, project, admission, disk)
	if err == nil && after != nil {
		_ = saveState(*after)
	}
	return bundle, err
}

func admitLocked(ctx context.Context, project Project, admission Admission, disk publishIO) (model.Bundle, *State, error) {
	var zero model.Bundle
	admissionID := admission.ID
	if err := publicationDurability(); err != nil {
		return zero, nil, err
	}
	if err := ctx.Err(); err != nil {
		return zero, nil, err
	}
	if project.ID == "" {
		return zero, nil, storeFault("invalid-field", "project.id", "an admission needs the declared project id")
	}
	if project.Ledger == "" {
		return zero, nil, storeFault("invalid-field", "project.ledger", "empty ledger path")
	}
	if !model.ValidID(admissionID) {
		return zero, nil, storeFault("invalid-field", "admission.command_id", "not a ULID")
	}
	if admission.Propose == nil || admission.RetryDigest == nil {
		return zero, nil, storeFault("invalid-field", "admission.propose", "no transaction callback")
	}
	// A registered home is checked before its ledger directory is touched, so
	// a deleted home is refused rather than recreated empty, and again under
	// the lock, which is what serializes admission with rebinding.
	if err := recheckHome(project); err != nil {
		return zero, nil, err
	}
	if err := ensureLedgerDir(project.Ledger, disk); err != nil {
		return zero, nil, err
	}
	if disk.beforeLock != nil {
		disk.beforeLock()
	}
	// An invalid cache setting is refused before anything else, on every path:
	// a retry that finds its bundle already published must not skip the check
	// every read makes.
	if _, err := cacheOff(); err != nil {
		return zero, nil, err
	}
	lock, err := holdAdmissionLock(ctx, project.Ledger)
	if err != nil {
		return zero, nil, err
	}
	defer lockRelease(lock)
	if err := recheckHome(project); err != nil {
		return zero, nil, err
	}

	// Recovery first, while the lock guarantees no other publisher is mid-write.
	if err := recoverLedger(project.Ledger); err != nil {
		return zero, nil, err
	}
	// The prefix selected under the lock, through the same validated loader
	// every read uses: the cache can shorten the fold, never the checks.
	prefix, err := Load(project)
	if err != nil {
		// A ledger that reads but does not fold is still this transaction's
		// to sequence: the store checks the chain, the caller the semantics.
		bundles, readErr := readLedger(project)
		if readErr != nil {
			return zero, nil, readErr
		}
		prefix = State{project: project, bundles: bundles, foldErr: err}
	}
	if bundle, published, err := prefix.published(admissionID); err != nil {
		return zero, nil, err
	} else if published {
		requestDigest, err := admission.RetryDigest(bundle)
		if err != nil {
			return zero, nil, err
		}
		if bundle.RequestDigest != requestDigest {
			return zero, nil, storeFault("conflict", filepath.Join(project.Ledger, bundleFileName(bundle)),
				"this admission id already published different content")
		}
		// The identical admission, already published. The previous attempt may
		// have died between the rename and the flush, so reflush before
		// promising durability this time, and publish nothing.
		if err := syncLedgerPath(project.Ledger, disk); err != nil {
			return zero, nil, uncertainPublication(admissionID, filepath.Join(project.Ledger, bundleFileName(bundle)), err)
		}
		return bundle, nil, nil
	}
	requestDigest, proposed, err := admission.Propose(prefix)
	if err != nil {
		return zero, nil, err
	}
	if !model.ValidDigest(requestDigest) {
		return zero, nil, storeFault("invalid-field", "admission.request_digest", "not lowercase sha-256 hex")
	}
	bundle, err := sealBundle(project, prefix.head(), admissionID, requestDigest, proposed)
	if err != nil {
		return zero, nil, err
	}
	if err := publishBundle(ctx, project.Ledger, bundle, disk); err != nil {
		return zero, nil, err
	}
	return bundle, prefix.extended(bundle), nil
}

// sealBundle assigns the clerical fields from the prefix read under the lock.
//
// A proposal that already carries a sequence, a predecessor, a transaction id
// or a request digest carries them from somewhere this transaction cannot
// vouch for, which in practice means a tail read before the lock. Refusing is
// what makes "the caller cannot supply a tail as authority" a property of the
// code rather than a rule callers are asked to remember.
func sealBundle(project Project, head selectedHead, admissionID model.ID, requestDigest model.Digest, proposed model.Bundle) (model.Bundle, error) {
	assigned := ""
	switch {
	case proposed.Version != 0:
		assigned = "version"
	case proposed.Project != "":
		assigned = "project"
	case proposed.Sequence != 0:
		assigned = "sequence"
	case proposed.CommandID != "":
		assigned = "command_id"
	case proposed.Predecessor != "":
		assigned = "predecessor"
	case proposed.RequestDigest != "":
		assigned = "request_digest"
	case !proposed.RecordedAt.IsZero():
		assigned = "recorded_at"
	}
	if assigned != "" {
		return model.Bundle{}, storeFault("invalid-field", "bundle."+assigned,
			"the transaction assigns this field from the prefix it read under the lock, so a proposal cannot carry its own")
	}
	bundle := proposed
	bundle.Version = model.WireVersion
	bundle.Project = project.ID
	bundle.Sequence = head.sequence + 1
	bundle.CommandID = admissionID
	bundle.Predecessor = head.command
	bundle.RequestDigest = requestDigest
	bundle.RecordedAt = time.Now().UTC()
	return bundle, nil
}

// publishBundle writes the bundle and makes it durable, in the one order that
// leaves every interruption classifiable:
//
//	create exclusive temporary -> write -> flush the file -> confirm the
//	destination is absent -> rename -> flush the directory -> acknowledge.
//
// Before the rename there is no new canonical bundle, whatever is on disk.
// After the rename there is exactly one, and it stays admitted even if the
// acknowledgement never reaches the caller.
func publishBundle(ctx context.Context, dir string, bundle model.Bundle, disk publishIO) error {
	name, err := model.BundleName(bundle.Sequence, bundle.CommandID)
	if err != nil {
		return err
	}
	data, err := model.Encode(bundle)
	if err != nil {
		return err
	}
	// Decode the exact bytes about to become immutable. A bundle that will not
	// parse is a ledger error for every future read, and re-encoding an
	// admitted bundle is never a repair, so the only place to catch it is here.
	decoded, err := model.DecodeBundle(data)
	if err != nil {
		return storeFault("invalid-field", "bundle", err.Error())
	}
	if decoded.Sequence != bundle.Sequence || decoded.CommandID != bundle.CommandID || decoded.Predecessor != bundle.Predecessor {
		return storeFault("invalid-field", "bundle", "the encoded bundle does not decode to the bundle being published")
	}
	// Last point where giving up costs nothing.
	if err := ctx.Err(); err != nil {
		return err
	}
	final := filepath.Join(dir, name)
	tmp := final + publicationSuffix
	f, err := disk.create(tmp)
	if err != nil {
		return storeFault("io", tmp, err.Error())
	}
	// Only this writer's own unpublished temporary, and only while the lock is
	// still held. After a successful rename there is nothing left to remove.
	defer os.Remove(tmp)
	written, err := disk.write(f, data)
	if err == nil && written != len(data) {
		err = fmt.Errorf("short write: %d of %d bytes", written, len(data))
	}
	if err == nil {
		err = disk.sync(f)
	}
	closeErr := f.Close()
	if err != nil {
		return storeFault("io", tmp, err.Error())
	}
	if closeErr != nil {
		return storeFault("io", tmp, closeErr.Error())
	}
	if _, err := os.Lstat(final); err == nil {
		// Under the lock, with the sequence taken from the prefix read under it,
		// this name cannot already exist. If it does, something outside this
		// writer published it, and overwriting an admitted bundle is the one
		// thing that must never happen.
		return storeFault("ledger-fork", final,
			"the next sequence is already published, so this transaction would overwrite an admitted bundle")
	} else if !os.IsNotExist(err) {
		return storeFault("io", final, err.Error())
	}
	if err := disk.rename(tmp, final); err != nil {
		return storeFault("io", final, err.Error())
	}
	// Published. From here a failure is uncertainty about durability, never a
	// reason to withdraw or repeat the transaction.
	if err := syncLedgerPath(dir, disk); err != nil {
		return uncertainPublication(bundle.CommandID, final, err)
	}
	return nil
}

// holdAdmissionLock waits for the single-writer lock.
//
// It waits. There is no age test, no PID test and no break. A lock held by a
// live writer is a writer to wait for, and a lock held by a dead one was
// already released by the OS when that process's descriptor closed. Waiting is
// a poll rather than a blocking flock so that a cancelled command returns
// instead of pinning a thread until some other writer finishes.
func holdAdmissionLock(ctx context.Context, dir string) (*os.File, error) {
	path := filepath.Join(dir, lockName)
	delay := 200 * time.Microsecond
	for {
		f, err := lockAcquire(path)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, errLockBusy) {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		if delay < 10*time.Millisecond {
			delay *= 2
		}
	}
}

// recoverLedger removes interrupted ledger publications.
//
// It runs under the admission lock, so every temporary it can see belongs to a
// publication that is over. It looks only in the ledger directory, never
// recurses, and removes only names this publisher creates. A live producer's
// intake temporary is in another tree entirely and is being written right now
// by a process that took no lock, so nothing here may go looking for one.
func recoverLedger(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return storeFault("io", dir, err.Error())
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		if _, _, ok := parsePublicationTemp(entry.Name()); !ok {
			// Not ours. An unrecognised file is left exactly where it is, and
			// readLedger refuses to read past it rather than guessing.
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return storeFault("io", path, err.Error())
		}
	}
	return nil
}

// ensureLedgerDir creates the ledger directory on first admission.
//
// Permissions are the ordinary 0755 a checkout gets, not intake's owner-only
// 0700: the ledger is committed with the project and read by whoever can read
// the working tree, so requiring owner-only access would refuse a normal
// checkout of a shared project.
func ensureLedgerDir(dir string, disk publishIO) error {
	info, err := os.Lstat(dir)
	if err == nil {
		if !info.IsDir() {
			return storeFault("ledger-corrupt", dir, "the configured ledger path is not a directory")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return storeFault("io", dir, err.Error())
	}
	// Each new directory's NAME lives in its parent, so every parent that gets
	// created has to be flushed too, outermost first.
	missing := []string{dir}
	for parent := filepath.Dir(dir); parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
		if _, err := os.Lstat(parent); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return storeFault("io", parent, err.Error())
		}
		missing = append(missing, parent)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return storeFault("io", dir, err.Error())
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := syncLedgerPath(filepath.Dir(missing[i]), disk); err != nil {
			return err
		}
	}
	return nil
}

// bundleFileName names a bundle already in the ledger, for diagnostics only.
func bundleFileName(bundle model.Bundle) string {
	name, err := model.BundleName(bundle.Sequence, bundle.CommandID)
	if err != nil {
		return string(bundle.CommandID)
	}
	return name
}
