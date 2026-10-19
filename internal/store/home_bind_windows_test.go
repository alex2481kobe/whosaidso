//go:build windows

package store

// A home's binding entry is replaced in place while a viewer may be reading
// it.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
