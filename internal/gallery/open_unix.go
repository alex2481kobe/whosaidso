//go:build !windows

package gallery

// Opening media nonblocking prevents a swapped FIFO from stalling a read.
// File kind validation and hashing belong to gallery.go.

import (
	"os"
	"syscall"
)

func openMedia(root *os.Root, path string) (*os.File, error) {
	if root != nil {
		return root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	}
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
