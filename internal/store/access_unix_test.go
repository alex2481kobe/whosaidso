//go:build darwin || linux

package store

// How the permission tests widen, inspect and restore owner-only access on
// POSIX lives here: mode bits, changed with chmod.

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func privateMode(directory bool) os.FileMode {
	if directory {
		return 0o700
	}
	return 0o600
}

// requireOwnerOnly checks the exact mode intake creates, not merely the
// absence of group and other bits.
func requireOwnerOnly(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm() != privateMode(directory) {
		return fmt.Errorf("%s mode %o, want %o", path, info.Mode().Perm(), privateMode(directory))
	}
	return nil
}

func widenAccess(t *testing.T, path string, directory bool) {
	t.Helper()
	if err := os.Chmod(path, privateMode(directory)|0o044); err != nil {
		t.Fatal(err)
	}
}

func accessStaysWidened(path string, directory bool) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().Perm() == privateMode(directory)|0o044
}

func wantRestoreCommand(path string, directory bool) string {
	return fmt.Sprintf("chmod %04o '%s'", privateMode(directory), strings.ReplaceAll(path, "'", "'\\''"))
}

// restoreAccess applies the restore the refusal named.
func restoreAccess(t *testing.T, path string, directory bool) {
	t.Helper()
	if err := os.Chmod(path, privateMode(directory)); err != nil {
		t.Fatal(err)
	}
}
