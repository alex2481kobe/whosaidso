//go:build windows

package write

// The run tests' platform-specific fixtures live here. Windows has no signal
// a process can send itself, so the signal half of the observer test does not
// run here; cancellation still does. Neither a read-only attribute nor a deny
// entry stops every account creating a directory inside another (an elevated
// administrator's directory creation bypasses the deny), so writes into dir
// are stopped by moving it aside and putting a plain file in its place.

import "os"

const runTestCanSignalSelf = false

func runTestSignalSelf() {}

func runTestDenyWrites(dir string) error {
	if err := os.Rename(dir, dir+".aside"); err != nil {
		return err
	}
	return os.WriteFile(dir, nil, 0o600)
}

func runTestAllowWrites(dir string) {
	if _, err := os.Stat(dir + ".aside"); err == nil {
		_ = os.Remove(dir)
		_ = os.Rename(dir+".aside", dir)
	}
}
