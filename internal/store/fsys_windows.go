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

// sharingRetries bounds how long a rename, or a read of a bundle, waits out
// another process that has the file open. Indexers and virus scanners open new files
// briefly and without FILE_SHARE_DELETE, which makes a rename fail with a
// sharing violation that clears on its own a moment later. The bound is what
// keeps a handle held for good from turning into a hang: about a second and a
// half of waiting, then the error is returned as it is.
var sharingRetries = []time.Duration{
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
	user, foreign, inherits, err := foreignGrants(path)
	if err != nil && !errors.Is(err, errNotOwnerOnly) {
		return storeFault("io", path, err.Error())
	}
	// A directory whose own entry is not inherited passes nothing owner-only
	// down: what is created inside gets the token's default list.
	if err == nil && len(foreign) == 0 && (inherits || !directory) {
		return nil
	}
	return insecurePermissions(path, restoreCommand(path, user, directory))
}

// restoreShell names the shell restoreCommand is written for.
const restoreShell = " in PowerShell"

// restoreCommand is the pair of icacls invocations that puts path back to
// owner-only whatever widened it. /reset drops every explicit entry, including
// one for a SID no account maps to, which icacls cannot name to remove; the
// second drops inherited entries and sets the user's own grant, inheritable
// below a directory as at creation.
func restoreCommand(path, user string, directory bool) string {
	grant := "F"
	if directory {
		grant = "(OI)(CI)F"
	}
	quoted := powershellLiteral(path)
	return "icacls " + quoted + " /reset followed by icacls " + quoted +
		" /inheritance:r /grant:r " + powershellLiteral("*"+user+":"+grant)
}

// powershellLiteral quotes s as a PowerShell single-quoted string, inside
// which nothing expands: $(...), a backtick and %VAR% stay as typed. The only
// special characters are the single quotes themselves, which PowerShell also
// recognises in their typographic forms; each is written twice. The quoting
// also keeps the grant's parentheses from being read as an expression.
func powershellLiteral(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		if strings.ContainsRune("'\u2018\u2019\u201a\u201b", r) {
			b.WriteRune(r)
		}
		b.WriteRune(r)
	}
	b.WriteByte('\'')
	return b.String()
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
	return waitOutSharing(func() error {
		ok, _, callErr := procMoveFileExW.Call(uintptr(unsafe.Pointer(f)), uintptr(unsafe.Pointer(t)), moveFileWriteThrough)
		if ok != 0 {
			return nil
		}
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: callErr}
	})
}

// renameReplace replaces to with from, for the one entry that is rewritten
// in place (a home's binding). A reader holding to open without delete
// sharing makes the replace fail for a moment, so it waits that out too.
// A damaged entry may be a link, which POSIX rename replaces in place but
// Windows refuses to rename over when it is a directory link, so the link
// itself (a symlink, or a junction, which Go reports as irregular), never
// what it points at, is removed first; a real directory stays and the rename
// refuses it. The caller's lock keeps writers out of the gap;
// a reader there finds no entry, as it would before the first write.
func renameReplace(from, to string) error {
	if info, err := os.Lstat(to); err == nil && info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		if err := os.Remove(to); err != nil {
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
		}
	}
	return waitOutSharing(func() error { return os.Rename(from, to) })
}

// readBundle reads a published ledger file. A reader takes no lock, and while
// a publisher's rename is in flight the kernel holds the renamed file open
// for deletion without sharing, so an open that lands in that window fails
// with a sharing violation that clears a moment later. The wait is bounded
// by the same schedule as a rename's.
func readBundle(path string) ([]byte, error) {
	var data []byte
	err := waitOutSharing(func() (err error) {
		data, err = os.ReadFile(path)
		return err
	})
	return data, err
}

// waitOutSharing runs op until it succeeds, fails for a reason other than
// another process's open handle, or the sharingRetries schedule runs out; the
// last error is returned as it is.
func waitOutSharing(op func() error) error {
	return retryWhile(op, transientSharing)
}

// waitOutSharingViolation retries only a sharing violation, for an operation
// (listing a packet directory) that no scanner turns into access denied; a
// refusal there is real and returns at once.
func waitOutSharingViolation(op func() error) error {
	return retryWhile(op, func(err error) bool { return err == errorSharingViolation })
}

func retryWhile(op func() error, transient func(error) bool) error {
	for attempt := 0; ; attempt++ {
		err := op()
		var errno syscall.Errno
		if err == nil || !errors.As(err, &errno) || !transient(errno) || attempt >= len(sharingRetries) {
			return err
		}
		time.Sleep(sharingRetries[attempt])
	}
}

// transientSharing names the errors another process's open handle produces.
// ERROR_ACCESS_DENIED is among them because a file a scanner has open for
// deletion, or without delete sharing, reports it; a real permission refusal
// costs the bounded wait and then fails exactly as it would have.
func transientSharing(err error) bool {
	return err == errorSharingViolation || err == errorLockViolation || err == syscall.ERROR_ACCESS_DENIED
}
