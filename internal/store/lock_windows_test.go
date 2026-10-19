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

// Every kind of link at the lock path is refused through the full acquire,
// and a dangling link's target is never created.
func TestLockRefusesEveryLinkKind(t *testing.T) {
	cases := map[string]func(t *testing.T, path, target string){
		"dangling file symlink": func(t *testing.T, path, target string) {
			if err := os.Symlink(target, path); err != nil {
				t.Skip(err)
			}
		},
		"directory symlink": func(t *testing.T, path, target string) {
			must(t, os.Mkdir(target, 0o700))
			if err := os.Symlink(target, path); err != nil {
				t.Skip(err)
			}
		},
		"junction": func(t *testing.T, path, target string) {
			must(t, os.Mkdir(target, 0o700))
			makeJunction(t, path, target)
		},
	}
	for name, plant := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, lockName)
			target := filepath.Join(dir, "outside")
			plant(t, path, target)
			for opener, open := range map[string]func(string) (*os.File, error){"openLock": openLock, "lockAcquire": lockAcquire} {
				if f, err := open(path); err == nil {
					f.Close()
					t.Fatalf("%s opened a %s at the lock path", opener, name)
				}
			}
			if _, err := os.Lstat(target); name == "dangling file symlink" && err == nil {
				t.Fatal("the lock created the link's target outside the ledger")
			}
		})
	}
}
