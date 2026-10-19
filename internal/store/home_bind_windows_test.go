//go:build windows

package store

// A home's binding entry is replaced in place while a viewer may be reading
// it.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func makeJunction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction: %v: %s", err, out)
	}
}

// A junction is a directory link as much as a symlink is (Go reports it as
// irregular, not as a symlink), so a damaged entry that is one is replaced
// too, and the directory it points at stays.
func TestBindingWriteReplacesAJunctionAtTheEntry(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "binding")
	target := filepath.Join(dir, "target")
	must(t, os.Mkdir(target, 0o700))
	putFile(t, filepath.Join(target, "kept"), []byte("x"))
	makeJunction(t, entry, target)
	root := filepath.Join(dir, "checkout")
	if err := writeBinding(entry, root); err != nil {
		t.Fatalf("a junction at the entry must be replaced: %v", err)
	}
	if data, _ := os.ReadFile(entry); string(data) != root+"\n" {
		t.Fatalf("entry = %q", data)
	}
	if _, err := os.Stat(filepath.Join(target, "kept")); err != nil {
		t.Fatalf("what the junction pointed at must stay: %v", err)
	}
}

// A reader that has the registry entry open for a moment must not make the
// registry write fail: the entry is replaced by rename, and a reader's handle
// without delete sharing makes that rename fail until it closes.
func TestBindingWriteWaitsOutAReaderHoldingTheEntry(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "binding")
	root := filepath.Join(dir, "checkout")
	putFile(t, entry, []byte(root+"\n"))
	held, err := os.Open(entry)
	if err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(dir, "probe"), []byte("p"))
	// Control: the hold is a real sharing conflict for a plain rename-over.
	if os.Rename(filepath.Join(dir, "probe"), entry) == nil {
		held.Close()
		t.Skip("control: this account's rename ignores the reader's open handle")
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		held.Close()
	}()
	other := filepath.Join(dir, "other")
	if err := writeBinding(entry, other); err != nil {
		t.Fatalf("a reader that releases the entry within 50ms failed the registry write: %v", err)
	}
	if data, _ := os.ReadFile(entry); string(data) != other+"\n" {
		t.Fatalf("entry = %q", data)
	}
}
