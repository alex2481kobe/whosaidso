//go:build windows

package store

// The Windows side of the four filesystem operations whose meaning differs by
// platform: owner-only creation, the owner-only check, opening a path so it
// can be flushed, and publication by rename. The access-control list itself is
// built and read in acl_windows.go. Everything that decides WHEN to call these
// lives in the portable files.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var procMoveFileExW = kernel32.NewProc("MoveFileExW")

const (
	moveFileWriteThrough  = 0x8
	fileShareAll          = syscall.FILE_SHARE_READ | syscall.FILE_SHARE_WRITE | syscall.FILE_SHARE_DELETE
	errorSharingViolation = syscall.Errno(32)
)

// renameRetries bounds how long a publication waits out another process that
// has one of its files open. Indexers and virus scanners open new files
// briefly and without FILE_SHARE_DELETE, which makes a rename fail with a
// sharing violation that clears on its own a moment later. The bound is what
// keeps a handle held for good from turning into a hang: about a second and a
// half of waiting, then the error is returned as it is.
var renameRetries = []time.Duration{
	5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond,
	80 * time.Millisecond, 160 * time.Millisecond, 320 * time.Millisecond, 400 * time.Millisecond, 400 * time.Millisecond,
}

// mkdirPrivate creates one directory whose access-control list is set at
// creation, not afterwards: a protected list granting only the current user,
// inherited by everything created inside it. Creating first and narrowing
// later would leave a window in which the inherited list (often SYSTEM,
// Administrators and the user) applies.
func mkdirPrivate(path string) error {
	sa, free, err := privateSecurityAttributes()
	if err != nil {
		return &os.PathError{Op: "mkdir", Path: path, Err: err}
	}
	defer free()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return &os.PathError{Op: "mkdir", Path: path, Err: err}
	}
	if err := syscall.CreateDirectory(p, sa); err != nil {
		return &os.PathError{Op: "mkdir", Path: path, Err: err}
	}
	return nil
}

// mkdirAllPrivate creates path and every missing parent owner-only, as
// os.MkdirAll with 0700 does on POSIX. Parents that already exist keep their
// own lists: only what this call creates is set.
func mkdirAllPrivate(path string) error {
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return nil
		}
		return &os.PathError{Op: "mkdir", Path: path, Err: syscall.ENOTDIR}
	}
	if parent := filepath.Dir(path); parent != path {
		if err := mkdirAllPrivate(parent); err != nil {
			return err
		}
	}
	if err := mkdirPrivate(path); err != nil && !os.IsExist(err) {
		return err
	}
	return nil
}

// privateAccessFault reads the path's access-control list back and refuses
// anything that grants access to a principal other than the current user.
// Mode bits say nothing on Windows: Go reports 0777 for every directory.
func privateAccessFault(path string, info os.FileInfo, directory bool) error {
	user, foreign, err := foreignGrants(path)
	if err != nil && !errors.Is(err, errNotOwnerOnly) {
		return storeFault("io", path, err.Error())
	}
	if err == nil && len(foreign) == 0 {
		return nil
	}
	restore := restoreCommand(path, user, foreign, directory)
	if err != nil {
		restore += " (" + err.Error() + ")"
	}
	return insecurePermissions(path, restore)
}

// restoreCommand is the one icacls invocation that puts path back to
// owner-only: inherited entries dropped, each foreign grant removed by SID,
// and the user's own grant replaced, inheritable below a directory as at
// creation. SIDs rather than names keep it exact on any machine and locale.
func restoreCommand(path, user string, foreign []string, directory bool) string {
	command := `icacls "` + path + `" /inheritance:r`
	if len(foreign) > 0 {
		command += " /remove:g *" + strings.Join(foreign, " *")
	}
	grant := "F"
	if directory {
		grant = "(OI)(CI)F"
	}
	return command + " /grant:r *" + user + ":" + grant
}

// openForSync opens path so that (*os.File).Sync, which is FlushFileBuffers
// here, can flush it. FlushFileBuffers refuses a read-only handle, and a
// directory opens only with FILE_FLAG_BACKUP_SEMANTICS; os.Open supplies
// neither. Every sharing mode is granted so that flushing never blocks a
// reader or a rename of the same path.
func openForSync(path string) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, fileShareAll, nil,
		syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// renameNoReplace publishes from under the name to and never replaces an
// existing destination: os.Rename on Windows passes MOVEFILE_REPLACE_EXISTING,
// which would let a second publisher overwrite an admitted bundle. Without that
// flag the kernel refuses an existing name atomically, so the check and the
// rename cannot be separated by another writer. MOVEFILE_WRITE_THROUGH makes
// the call return only once the move is on disk.
func renameNoReplace(from, to string) error {
	f, err := syscall.UTF16PtrFromString(from)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	t, err := syscall.UTF16PtrFromString(to)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	for attempt := 0; ; attempt++ {
		ok, _, callErr := procMoveFileExW.Call(uintptr(unsafe.Pointer(f)), uintptr(unsafe.Pointer(t)), moveFileWriteThrough)
		if ok != 0 {
			return nil
		}
		if !transientSharing(callErr) || attempt >= len(renameRetries) {
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: callErr}
		}
		time.Sleep(renameRetries[attempt])
	}
}

// transientSharing names the errors another process's open handle produces.
// ERROR_ACCESS_DENIED is among them because a file a scanner has open for
// deletion, or without delete sharing, reports it; a real permission refusal
// costs the bounded wait and then fails exactly as it would have.
func transientSharing(err error) bool {
	return err == errorSharingViolation || err == errorLockViolation || err == syscall.ERROR_ACCESS_DENIED
}
