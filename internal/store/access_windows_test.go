//go:build windows

package store

// How the store's permission tests inspect owner-only access and predict the
// restore command on Windows lives here; widening and restoring go through
// internal/testaccess, which uses icacls, the tool a user would run.

import (
	"fmt"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/testaccess"
)

// everyone is the well-known SID testaccess.Widen grants.
const everyone = "S-1-1-0"

func requireOwnerOnly(path string, directory bool) error {
	_, foreign, err := foreignGrants(path)
	if err != nil {
		return err
	}
	if len(foreign) != 0 {
		return fmt.Errorf("%s grants access to %v", path, foreign)
	}
	return nil
}

func widenAccess(t *testing.T, path string, directory bool) { testaccess.Widen(t, path) }

func accessStaysWidened(path string, directory bool) bool {
	_, foreign, err := foreignGrants(path)
	return err == nil && len(foreign) != 0
}

func wantRestoreCommand(path string, directory bool) string {
	user, err := currentUserSID()
	if err != nil {
		return "current user unknown: " + err.Error()
	}
	return restoreCommand(path, user, []string{everyone}, directory)
}

// restoreAccess runs the command the refusal named, so a restore command that
// does not work fails here.
func restoreAccess(t *testing.T, path string, directory bool) {
	t.Helper()
	if out, err := testaccess.RunRestore(wantRestoreCommand(path, directory)); err != nil {
		t.Fatalf("restore command: %v: %s", err, out)
	}
}
