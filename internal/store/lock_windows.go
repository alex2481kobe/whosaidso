//go:build windows

package store

import (
	"os"
	"syscall"
	"unsafe"
)

// LockFileEx and UnlockFileEx are bound here because the standard syscall
// package does not export them for Windows. Binding two kernel32 entry points
// through the standard library is not a dependency.
var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

// LockFileEx flags. Exclusive plus fail-immediately gives the same
// non-blocking, never-broken lock the Unix side provides.
const (
	lockfileFailImmediately = 0x00000001
	lockfileExclusiveLock   = 0x00000002
	errorLockViolation      = syscall.Errno(33)
)

// lockAcquire takes the exclusive admission lock without blocking, returning
// errLockBusy when another writer holds it.
//
// O_CLOEXEC is what makes the handle non-inheritable here: Go's Windows open
// attaches an inheritable security attribute unless that flag is set, and an
// inherited handle would keep the lock alive inside a wrapped instrument
// process long after this admission finished.
//
// Windows releases a LockFileEx range when its handle closes, including on
// process death, so a crashed writer needs no lease timer, age test or PID test
// to be cleaned up after.
//
// This file compiles and is believed correct, and no crash test has run against
// it on Windows. publicationDurability refuses publication here for that reason,
// so nothing in this build depends on an untested claim.
//
// Windows has no O_NOFOLLOW, so a symlink or reparse point is refused by Lstat
// before the open. That leaves a race the Unix side does not have, which is
// acceptable only because publication is refused on this platform.
func lockAcquire(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, unsafeLock(path)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_CLOEXEC, 0o644)
	if err != nil {
		return nil, storeFault("io", path, err.Error())
	}
	// Lock one byte rather than the whole range: the lock is a rendezvous, and
	// nothing ever reads this file's contents.
	var overlapped syscall.Overlapped
	ok, _, callErr := procLockFileEx.Call(f.Fd(),
		uintptr(lockfileExclusiveLock|lockfileFailImmediately), 0, 1, 0,
		uintptr(unsafe.Pointer(&overlapped)))
	if ok == 0 {
		f.Close()
		if callErr == errorLockViolation {
			return nil, errLockBusy
		}
		return nil, storeFault("io", path, callErr.Error())
	}
	return f, nil
}

// lockRelease drops the admission lock. Closing alone would release it, and the
// explicit unlock keeps the release visible in a trace rather than implied.
func lockRelease(f *os.File) error {
	var overlapped syscall.Overlapped
	ok, _, callErr := procUnlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	closeErr := f.Close()
	if ok == 0 {
		return storeFault("io", f.Name(), callErr.Error())
	}
	if closeErr != nil {
		return storeFault("io", f.Name(), closeErr.Error())
	}
	return nil
}
