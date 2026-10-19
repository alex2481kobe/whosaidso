//go:build darwin || linux

// Package testaccess gives tests outside the store one way, per platform, to
// widen a path's access beyond its owner, to undo that, and to run the restore
// command an insecure-permissions refusal names. On POSIX that is mode bits;
// on Windows it is the access-control list, changed with icacls. Only tests
// import it.
package testaccess

import (
	"os"
	"os/exec"
	"testing"
)

// Widen lets group and others read path (and enter it, for a directory).
func Widen(t testing.TB, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mode := os.FileMode(0o644)
	if info.IsDir() {
		mode = 0o755
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// Restore puts path back to owner-only.
func Restore(t testing.TB, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mode := os.FileMode(0o600)
	if info.IsDir() {
		mode = 0o700
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// Widened reports whether anyone but the owner still has access to path.
func Widened(t testing.TB, path string) bool {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()&0o077 != 0
}

// RunRestore executes a displayed restore command exactly as a user would
// paste it into a POSIX shell.
func RunRestore(command string) ([]byte, error) {
	return exec.Command("/bin/sh", "-c", command).CombinedOutput()
}

// MakePrivate makes an existing directory owner-only, as a home the store
// created itself would be, so fixtures written below it by hand pass the
// store's owner-only checks.
func MakePrivate(dir string) error { return os.Chmod(dir, 0o700) }
