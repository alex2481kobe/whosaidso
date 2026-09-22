//go:build darwin || linux

package store

import "os"

// syncLedgerPath flushes one file or directory.
//
// A rename makes the new name visible immediately and does not make it durable.
// The name lives in the DIRECTORY, so the directory is what has to be flushed
// before publication can be acknowledged. Flushing the bundle file alone would
// leave a crash able to lose the name that points at perfectly durable bytes.
func syncLedgerPath(path string, disk publishIO) error {
	f, err := os.Open(path)
	if err != nil {
		return storeFault("io", path, err.Error())
	}
	syncErr := disk.sync(f)
	closeErr := f.Close()
	if syncErr != nil {
		return storeFault("io", path, syncErr.Error())
	}
	if closeErr != nil {
		return storeFault("io", path, closeErr.Error())
	}
	return nil
}

// publicationDurability reports whether this build has ESTABLISHED durable
// publication on this platform, not whether it expects to have it.
//
// On darwin and linux the crash and injected-failure tests in publish_test.go
// exercise every point from temporary creation to the acknowledgement, on
// whatever filesystem the tests run on. Those tests kill processes and inject
// errors. They do not cut power, so what they establish is that an interrupted
// publication is classified correctly, not that this filesystem honours fsync
// against a power loss.
func publicationDurability() error { return nil }
