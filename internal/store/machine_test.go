package store

// The persistent machine id: created once, stable across reads and racing
// first runs, never replaced when damaged. Intake and ledger tests live elsewhere.

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"datum/internal/model"
)

func TestMachineIDIsCreatedOnceUnderTheUserHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	first, err := MachineID()
	if err != nil || !model.ValidID(first) {
		t.Fatalf("control: the first call creates a ULID: %q, %v", first, err)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".datum", MachineIDFile))
	if err != nil || string(raw) != string(first)+"\n" {
		t.Fatalf("expected ~/.datum/%s to hold the id and a newline, got %q, %v", MachineIDFile, raw, err)
	}
	again, err := MachineID()
	if err != nil || again != first {
		t.Fatalf("a second call must read the same identity: %q vs %q, %v", again, first, err)
	}
	t.Setenv("HOME", t.TempDir())
	other, err := MachineID()
	if err != nil || other == first {
		t.Fatalf("another home is another machine identity: %q vs %q, %v", other, first, err)
	}
}

func TestMachineIDRacingFirstRunsAgree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".datum")
	ids := make([]model.ID, 16)
	errs := make([]error, len(ids))
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func(i int) { defer wg.Done(); ids[i], errs[i] = machineIDAt(dir) }(i)
	}
	wg.Wait()
	for i := range ids {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("racing creators must all read the published id: %v vs %v, %v", ids[i], ids[0], errs[i])
		}
	}
}

func TestMachineIDDamageIsRefusedNotReplaced(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T, path string){
		"malformed":       func(t *testing.T, path string) { writeMachineFixture(t, path, "not-a-ulid\n") },
		"missing newline": func(t *testing.T, path string) { writeMachineFixture(t, path, "01J8Z0000000000000000000AA") },
		"lowercase":       func(t *testing.T, path string) { writeMachineFixture(t, path, "01j8z0000000000000000000aa\n") },
		"symlink": func(t *testing.T, path string) {
			target := filepath.Join(filepath.Dir(path), "elsewhere")
			writeMachineFixture(t, target, "01J8Z0000000000000000000AA\n")
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, MachineIDFile)
			setup(t, path)
			before, _ := os.Lstat(path)
			if id, err := machineIDAt(dir); err == nil {
				t.Fatalf("a damaged machine id must be refused, got %q", id)
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("the damaged file must be left in place: %v", err)
			}
		})
	}
	t.Run("control", func(t *testing.T) {
		dir := t.TempDir()
		writeMachineFixture(t, filepath.Join(dir, MachineIDFile), "01J8Z0000000000000000000AA\n")
		if id, err := machineIDAt(dir); err != nil || id != "01J8Z0000000000000000000AA" {
			t.Fatalf("a well-formed existing id is read as is: %q, %v", id, err)
		}
	})
}

func writeMachineFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
