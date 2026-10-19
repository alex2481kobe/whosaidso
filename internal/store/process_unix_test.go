//go:build darwin || linux

package store

// Asking whether a test's child process is still running lives here.

import (
	"os"
	"syscall"
)

func processAlive(p *os.Process) error { return p.Signal(syscall.Signal(0)) }
