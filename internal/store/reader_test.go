// A retained snapshot is reused only for the exact bytes selected now.
// Tests rewrite old bundles without changing size, mtime or the head.
package store

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReaderReusesOnlyAnUnchangedBytePrefix(t *testing.T) {
	p := warmProject(t)
	var reader Reader
	first, err := reader.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	second, err := reader.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if first.Snapshot() != second.Snapshot() {
		t.Fatal("unchanged prefix decoded a second snapshot")
	}
	files, err := inventory(p)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(p.Ledger, files[0].name)
	info, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(raw, []byte("cache task 1"), []byte("other task 1"), 1)
	if bytes.Equal(raw, changed) || len(raw) != len(changed) {
		t.Fatal("rewrite control")
	}
	if err := os.WriteFile(name, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(name, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	third, err := reader.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := Replayed(p)
	if err != nil {
		t.Fatal(err)
	}
	if first.Snapshot() == third.Snapshot() || !reflect.DeepEqual(third.Snapshot(), replay.Snapshot()) {
		t.Fatal("same-stat earlier edit reused a stale snapshot")
	}
	appendTasks(t, p, 5, 1)
	appended, err := reader.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if appended.Snapshot().Watermark().Bundles != 5 || first.Snapshot().Watermark().Bundles != 4 {
		t.Fatal("append changed a shared snapshot or was missed")
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Load(p); err == nil {
		t.Fatal("cached prefix concealed a removed predecessor")
	}
}

func TestReaderSeparatesProjectsAndHonorsNoCache(t *testing.T) {
	a, b := warmProject(t), warmProject(t)
	var reader Reader
	first, err := reader.Load(a)
	if err != nil {
		t.Fatal(err)
	}
	other, err := reader.Load(b)
	if err != nil {
		t.Fatal(err)
	}
	if first.Snapshot() == other.Snapshot() {
		t.Fatal("reader crossed project directories")
	}
	t.Setenv(NoCacheEnv, "1")
	replay, err := reader.Load(b)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Restored() != 0 || replay.Snapshot() == other.Snapshot() {
		t.Fatal("disabled cache reused the retained snapshot")
	}
	t.Setenv(NoCacheEnv, "bad")
	if _, err := reader.Load(b); err == nil {
		t.Fatal("invalid cache switch accepted")
	}
}
