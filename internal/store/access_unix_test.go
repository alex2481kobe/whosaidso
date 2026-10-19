//go:build darwin || linux

package store

// How the store's permission tests inspect owner-only access and predict the
// restore command on POSIX lives here; widening and restoring go through
// internal/testaccess.

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/testaccess"
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

func widenAccess(t *testing.T, path string, directory bool) { testaccess.Widen(t, path) }

func accessStaysWidened(path string, directory bool) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().Perm()&0o077 != 0
}

func wantRestoreCommand(path string, directory bool) string {
	return fmt.Sprintf("chmod %04o '%s'", privateMode(directory), strings.ReplaceAll(path, "'", "'\\''"))
}

// restoreAccess runs the command the refusal named, so a restore command that
// does not work fails here.
func restoreAccess(t *testing.T, path string, directory bool) {
	t.Helper()
	if out, err := testaccess.RunRestore(wantRestoreCommand(path, directory)); err != nil {
		t.Fatalf("restore command: %v: %s", err, out)
	}
}
