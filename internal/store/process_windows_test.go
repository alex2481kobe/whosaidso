//go:build windows

package store

// Asking whether a test's child process is still running lives here. Windows
// has no signal 0; a process handle that is not yet signalled is a live one.

import (
	"errors"
	"os"
	"syscall"
)

const waitTimeout = 258

func processAlive(p *os.Process) error {
	h, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(p.Pid))
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	if event, err := syscall.WaitForSingleObject(h, 0); err != nil {
		return err
	} else if event != waitTimeout {
		return errors.New("process has exited")
	}
	return nil
}
