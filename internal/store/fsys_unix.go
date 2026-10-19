//go:build darwin || linux

package store

// The POSIX side of the four filesystem operations whose meaning differs by
// platform: owner-only creation, the owner-only check, opening a path so it
// can be flushed, and publication by rename. Everything that decides WHEN to
// call them lives in the portable files.

import (
	"os"
	"strings"
)

// mkdirPrivate creates one directory readable only by its owner. The chmod
// after creation is there because the umask can only remove bits: a 0700
// request under an unusual umask could come back narrower than asked, never
// wider, and the check that follows wants exactly owner-only.
func mkdirPrivate(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

// mkdirAllPrivate creates path and every missing parent owner-only.
func mkdirAllPrivate(path string) error {
	return os.MkdirAll(path, 0o700)
}

// restoreShell names the shell restoreCommand is written for: any POSIX one.
const restoreShell = ""

// privateAccessFault reports whether path, already known to be a real file or
// directory, is reachable by anyone but its owner.
func privateAccessFault(path string, info os.FileInfo, directory bool) error {
	if info.Mode().Perm()&0o077 == 0 {
		return nil
	}
	mode := "0600"
	if directory {
		mode = "0700"
	}
	quoted := "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
	return insecurePermissions(path, "chmod "+mode+" "+quoted)
}

// openForSync opens path so that (*os.File).Sync flushes it. On POSIX a
// read-only descriptor is enough for fsync, on files and directories alike.
func openForSync(path string) (*os.File, error) {
	return os.Open(path)
}

// readBundle reads a published ledger file. POSIX has no sharing modes, so a
// read never collides with a concurrent rename.
func readBundle(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// waitOutSharing runs op once: POSIX has no sharing modes, so there is no
// other process's handle to wait out.
func waitOutSharing(op func() error) error {
	return op()
}

// renameNoReplace publishes from under the name to. POSIX rename replaces an
// existing destination, so the never-replace rule here is kept by the callers:
// publication checks the destination under the admission lock, and an intake
// rename targets a directory, which rename refuses to replace when nonempty.
func renameNoReplace(from, to string) error {
	return os.Rename(from, to)
}

// renameReplace replaces to with from; POSIX rename does that atomically,
// whoever has to open.
func renameReplace(from, to string) error {
	return os.Rename(from, to)
}
