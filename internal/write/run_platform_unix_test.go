//go:build darwin || linux

package write

// The run tests' platform-specific fixtures live here: how a child ends
// itself by a signal, and how a directory is made unwritable.

import (
	"os"
	"syscall"
)

const runTestCanSignalSelf = true

func runTestSignalSelf() { _ = syscall.Kill(os.Getpid(), syscall.SIGTERM) }

func runTestDenyWrites(dir string) error { return os.Chmod(dir, 0o500) }

func runTestAllowWrites(dir string) { _ = os.Chmod(dir, 0o700) }
