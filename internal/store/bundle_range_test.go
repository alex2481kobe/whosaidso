// Range reads must agree with full selected bundles and refuse a changed
// selected file, without decoding unrelated bundles after selection.
package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBundleRangesPreserveSelectionAndBounds(t *testing.T) {
	p := ledgerProject(t)
	if err := os.WriteFile(filepath.Join(p.Root, "whosaidso.toml"), []byte("id = 'team/project'\nledger = '.whosaidso/events'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	admitCached(t, p, 1, 5)
	state, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	all, err := state.Bundles()
	if err != nil {
		t.Fatal(err)
	}
	cold, err := Replayed(p)
	if err != nil {
		t.Fatal(err)
	}
	coldRange, err := cold.BundleRange(1, 2)
	if err != nil || !reflect.DeepEqual(coldRange, all[1:3]) {
		t.Fatalf("cold range differs: %v", err)
	}
	selected, err := state.BundleRange(1, 2)
	if err != nil || !reflect.DeepEqual(selected, all[1:3]) {
		t.Fatalf("range differs from full selection: %v", err)
	}
	for _, r := range [][2]int{{-1, 1}, {0, -1}} {
		if _, err := state.BundleRange(r[0], r[1]); err == nil {
			t.Fatalf("negative range accepted: %v", r)
		}
	}
	empty, err := state.BundleRange(999999, 10)
	if err != nil || len(empty) != 0 {
		t.Fatalf("past end: %v %v", empty, err)
	}
	path := filepath.Join(p.Ledger, state.files[1].name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Trailing JSON whitespace preserves decoding but changes the selected bytes.
	if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := state.BundleRange(1, 1); err == nil {
		t.Fatal("range served changed selected bytes")
	}
	if _, err := state.BundleRange(3, 1); err != nil {
		t.Fatalf("unrelated changed bundle was decoded by a range read: %v", err)
	}
}
