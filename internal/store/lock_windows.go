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
// Windows releases a LockFileEx range when its handle closes, including on
// process death, so a crashed writer needs no lease timer, age test or PID test
// to be cleaned up after.
//
// The Lstat check gives a link or directory at the lock path its own refusal;
// openLock then refuses one substituted after that check.
func lockAcquire(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, unsafeLock(path)
	}
	f, err := openLock(path)
	if err != nil {
		return nil, err
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

// openLock opens or creates the lock file without following a link, as the
// Unix side's O_NOFOLLOW does: FILE_FLAG_OPEN_REPARSE_POINT opens a symlink or
// junction itself, and the handle is then refused unless it is a regular file.
// Nil security attributes make the handle non-inheritable, so a wrapped
// instrument process cannot keep the lock alive after this admission. The
// file is shared for reading and writing but not deletion, so the lock file
// cannot be renamed or removed while it is held.
func openLock(path string) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, storeFault("io", path, err.Error())
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
		nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, storeFault("io", path, err.Error())
	}
	f := os.NewFile(uintptr(h), path)
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &info); err != nil ||
		info.FileAttributes&(syscall.FILE_ATTRIBUTE_DIRECTORY|syscall.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		f.Close()
		return nil, unsafeLock(path)
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
