//go:build windows

package write

// The run tests' platform-specific fixtures live here. Windows has no signal
// a process can send itself, so the signal half of the observer test does not
// run here; cancellation still does. A directory's read-only attribute does
// not stop writes into it on Windows, so a deny entry for the current user,
// on the directory itself and inherited by nothing inside, does that instead;
// a deny entry leaves the directory owner-only.

import (
	"os/exec"
	"os/user"
)

const runTestCanSignalSelf = false

func runTestSignalSelf() {}

func runTestDenyWrites(dir string) error {
	u, err := user.Current()
	if err != nil {
		return err
	}
	return exec.Command("icacls", dir, "/deny", "*"+u.Uid+":(W)").Run()
}

func runTestAllowWrites(dir string) {
	if u, err := user.Current(); err == nil {
		_ = exec.Command("icacls", dir, "/remove:d", "*"+u.Uid).Run()
	}
}
