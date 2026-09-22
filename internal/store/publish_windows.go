//go:build windows

package store

// syncLedgerPath would flush one file or directory.
//
// Flushing a directory on Windows is not the Unix operation under another name.
// It needs a handle opened with FILE_FLAG_BACKUP_SEMANTICS, FlushFileBuffers
// refuses a read-only handle, and the ordering NTFS guarantees for a rename is
// not the ordering the fsync-the-directory rule establishes. Returning nil here
// would make the publisher acknowledge a durability nothing has shown.
func syncLedgerPath(path string, disk publishIO) error {
	return storeFault("unsupported-platform", path,
		"directory durability after a rename is not established on windows")
}

// publicationDurability refuses publication on Windows.
//
// The lock in lock_windows.go is implemented and compiles. What is missing is
// evidence: no crash test and no injected-failure test has run natively here,
// so no restart outcome on this platform has a classified result. Intake
// already refuses for the same reason. Lifting this is a measurement, running
// the store tests on Windows, not an edit to this comment.
func publicationDurability() error {
	return storeFault("unsupported-platform", "ledger",
		"ledger publication is crash tested on darwin and linux only, so windows publication is refused until those tests run natively")
}
