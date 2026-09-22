//go:build darwin || linux

package store

import (
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
func lockAcquire(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_CLOEXEC, 0o644)
	if err != nil {
		return nil, storeFault("io", path, err.Error())
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
