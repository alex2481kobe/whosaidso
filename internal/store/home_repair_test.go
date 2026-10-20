package store

// Recovery from a registry entry that names no checkout (home.go,
// home_bind.go): an explicit registration replaces it and invents no previous
// checkout. The registry's ordinary cases are in home_test.go.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestBindReplacesAnUnreadableEntry(t *testing.T) {
	root := homeWorld(t)
	mustBind(t, root, root) // control: an ordinary entry
	path, err := bindingPath("team/project")
	if err != nil {
		t.Fatal(err)
	}
	for _, damage := range []func(){
		func() { putFile(t, path, []byte("broken\n")) },
		func() { must(t, os.Remove(path)); must(t, os.Symlink(root, path)) },
	} {
		damage()
		if _, _, err := Binding("team/project"); !isFault(err, codeBindingCorrupt) {
			t.Fatalf("control: a damaged entry must read as damaged, got %v", err)
		}
		if r := mustBind(t, root, root); r.Previous != "" {
			t.Fatalf("replacing an unreadable entry must invent no previous checkout: %+v", r)
		}
		if got, bound, err := Binding("team/project"); err != nil || !bound || got != root {
			t.Fatalf("the entry must name the checkout again: %q %t %v", got, bound, err)
		}
	}
}

// A real directory where the entry belongs is not an entry to replace: the
// write is refused and the directory and what it holds stay.
func TestBindingWriteRefusesADirectoryAtTheEntry(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "binding")
	must(t, os.Mkdir(entry, 0o700))
	if err := writeBinding(entry, filepath.Join(dir, "checkout")); !isFault(err, "io") {
		t.Fatalf("a directory at the entry must refuse the write, got %v", err)
	}
	if info, err := os.Lstat(entry); err != nil || !info.IsDir() {
		t.Fatalf("the directory at the entry must stay: %v", err)
	}
}

func isFault(err error, code string) bool {
	var f *model.Fault
	return errors.As(err, &f) && f.Code == code
}
