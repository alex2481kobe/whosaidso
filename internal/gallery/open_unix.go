//go:build !windows

package gallery

// Opening media nonblocking prevents a swapped FIFO from stalling a read.
// File kind validation and hashing belong to gallery.go.

import (
	"os"
	"syscall"
)

func openMedia(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
