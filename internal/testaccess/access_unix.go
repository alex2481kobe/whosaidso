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

// widenedMode is exactly what Widen sets: group and others may read path,
// and enter it when it is a directory.
func widenedMode(directory bool) os.FileMode {
	if directory {
		return 0o755
	}
	return 0o644
}

// privateMode is exactly what the store creates and its restore command sets.
func privateMode(directory bool) os.FileMode {
	if directory {
		return 0o700
	}
	return 0o600
}

func mode(t testing.TB, path string) (os.FileMode, bool) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm(), info.IsDir()
}

// Widen lets group and others read path (and enter it, for a directory).
func Widen(t testing.TB, path string) {
	t.Helper()
	_, directory := mode(t, path)
	if err := os.Chmod(path, widenedMode(directory)); err != nil {
		t.Fatal(err)
	}
}

// Restore puts path back to owner-only.
func Restore(t testing.TB, path string) {
	t.Helper()
	_, directory := mode(t, path)
	if err := os.Chmod(path, privateMode(directory)); err != nil {
		t.Fatal(err)
	}
}

// Widened reports whether path still has exactly the access Widen gave it.
func Widened(t testing.TB, path string) bool {
	t.Helper()
	perm, directory := mode(t, path)
	return perm == widenedMode(directory)
}

// OwnerOnly reports whether path has exactly the owner-only mode the store
// creates, not merely no group or other bits.
func OwnerOnly(t testing.TB, path string) bool {
	t.Helper()
	perm, directory := mode(t, path)
	return perm == privateMode(directory)
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
