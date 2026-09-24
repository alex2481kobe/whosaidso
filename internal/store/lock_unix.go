//go:build darwin || linux

package store

import (
	"errors"
	"os"
	"syscall"
)

// lockAcquire takes the exclusive admission lock without blocking, returning
// errLockBusy when another writer holds it.
//
// The descriptor is close-on-exec. A lock that a wrapped instrument process
// inherited would outlive this admission and hold every other writer hostage
// for as long as that child ran, so the child must never receive it. Go opens
// every descriptor close-on-exec already, and the flag is written out here
// because this file is the reason it matters.
//
// flock is associated with the open file description, so the kernel drops it
// when this descriptor closes, including when the process dies. That is the
// whole recovery story for the lock itself: no lease timer, no age test, no PID
// test, and nothing that can decide another writer is dead while it is working.
//
// The lock is the one file WhoSaidSo opens by a fixed name without O_EXCL, so it is
// the one a planted symlink could redirect: O_CREATE follows a dangling link
// and creates its target, wherever that is. O_NOFOLLOW refuses a link as the
// final component, and the open file must be a regular file, so admission
// never creates or locks anything outside the ledger directory.
func lockAcquire(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o644)
	if errors.Is(err, syscall.ELOOP) {
		return nil, unsafeLock(path)
	}
	if err != nil {
		return nil, storeFault("io", path, err.Error())
	}
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, unsafeLock(path)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if err == syscall.EWOULDBLOCK || err == syscall.EINTR {
			return nil, errLockBusy
		}
		return nil, storeFault("io", path, err.Error())
	}
	return f, nil
}

// lockRelease drops the admission lock. Closing alone would release it, and the
// explicit unlock keeps the release visible in a trace rather than implied.
func lockRelease(f *os.File) error {
	unlockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	closeErr := f.Close()
	if unlockErr != nil {
		return storeFault("io", f.Name(), unlockErr.Error())
	}
	if closeErr != nil {
		return storeFault("io", f.Name(), closeErr.Error())
	}
	return nil
}
