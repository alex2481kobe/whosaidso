//go:build windows

package store

// The admission lock opens without following a link, so a link substituted
// at the lock path after lockAcquire's Lstat check is refused, not locked.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLockRefusesALinkSubstitutedAfterTheCheck(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside")
	putFile(t, outside, []byte("not a lock"))
	path := filepath.Join(dir, lockName)
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("this account cannot create a symlink: %v", err)
	}
	// openLock is the open lockAcquire makes after its Lstat check, so calling
	// it directly is the moment a link substituted in between would be met.
	f, err := openLock(path)
	if err == nil {
		f.Close()
		t.Fatal("the lock followed a link to a file outside the ledger")
	}
	requireFault(t, err, "ledger-corrupt")
	if data, _ := os.ReadFile(outside); string(data) != "not a lock" {
		t.Fatalf("the link's target changed: %q", data)
	}
	// A regular lock file still opens and locks.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	held, err := lockAcquire(path)
	if err != nil {
		t.Fatalf("a regular lock path must lock: %v", err)
	}
	if err := lockRelease(held); err != nil {
		t.Fatal(err)
	}
}
