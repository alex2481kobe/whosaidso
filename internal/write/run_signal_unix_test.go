//go:build darwin || linux

package write

// How a run-test child ends itself by a signal lives here; Windows has no
// signal a process can send itself.

import (
	"os"
	"syscall"
)

const runTestCanSignalSelf = true

func runTestSignalSelf() { _ = syscall.Kill(os.Getpid(), syscall.SIGTERM) }
