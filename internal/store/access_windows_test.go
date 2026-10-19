//go:build windows

package store

// How the permission tests widen, inspect and restore owner-only access on
// Windows lives here: access-control lists, changed with icacls, the tool a
// user would run, so the tests never depend on the code they check to make
// their fixtures.

import (
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// everyone is the well-known SID of the Everyone group.
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

func icacls(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command("icacls", args...).CombinedOutput(); err != nil {
		t.Fatalf("icacls %v: %v: %s", args, err, out)
	}
}

func widenAccess(t *testing.T, path string, directory bool) {
	t.Helper()
	icacls(t, path, "/grant", "*"+everyone+":R")
}

func accessStaysWidened(path string, directory bool) bool {
	_, foreign, err := foreignGrants(path)
	return err == nil && slices.Contains(foreign, everyone)
}

func wantRestoreCommand(path string, directory bool) string {
	user, err := currentUserSID()
	if err != nil {
		return "current user unknown: " + err.Error()
	}
	return restoreCommand(path, user, []string{everyone}, directory)
}

// restoreAccess runs the command the refusal named, split as cmd.exe would
// split it, so a restore command that does not work fails here.
func restoreAccess(t *testing.T, path string, directory bool) {
	t.Helper()
	command := wantRestoreCommand(path, directory)
	quoted := `icacls "` + path + `" `
	if !strings.HasPrefix(command, quoted) {
		t.Fatalf("restore command %q does not start with %q", command, quoted)
	}
	icacls(t, append([]string{path}, strings.Fields(strings.TrimPrefix(command, quoted))...)...)
}
