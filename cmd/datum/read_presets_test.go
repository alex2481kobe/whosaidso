package main

// CLI tests for read presets, limits and continuation. Other read CLI tests and
// the readProcess/readJSON helpers stay in read_test.go.

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"datum/internal/model"
)

func TestReadCLIPresetsLimitAndContinueObservation(t *testing.T) {
	root, data := cliFixture(t)
	cliControl(t, root, data)
	call := func(args ...string) ([]byte, error) {
		var output bytes.Buffer
		err := readCLI(context.Background(), args, root, &output, io.Discard)
		return output.Bytes(), err
	}
	for _, args := range [][]string{{"instruments"}, {"state"}, {"now"}, {"todo", "--limit", "1"}, {"context", "--limit", "2", string(cliID(1))}} {
		if _, err := call(args...); err != nil {
			t.Fatalf("preset %v must succeed: %v", args, err)
		}
	}
	for _, args := range [][]string{{"continue"}, {"instruments", "--limit", "1"}, {"now", string(cliID(1))}, {"todo", "--limit", "-1"}} {
		if output, err := call(args...); err == nil || len(output) != 0 {
			t.Fatalf("invalid preset %v must fail without an answer, got %s", args, output)
		}
	}
	files := func() map[string]string {
		out := map[string]string{}
		filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				data, _ := os.ReadFile(path)
				out[path] = string(data)
			}
			return nil
		})
		return out
	}
	before := files()
	answer := readJSON(t, readProcess(t, root, nil, "continue", "--json", string(cliID(1))))
	c := answer.Preset.Continue
	// The fixture is not a git checkout, so HEAD and dirty are UNKNOWN, but the
	// observation time was actually taken.
	if c.Observed.Head.State != model.Unknown || c.Observed.Head.Reason == "" || c.Observed.Dirty.State != model.Unknown ||
		c.Observed.ObservedAt.State != model.Known || c.Observed.ObservedAt.Value.IsZero() {
		t.Fatalf("continue outside git must report UNKNOWN HEAD/dirty with reasons and a real observation time, got %+v", c.Observed)
	}
	if !reflect.DeepEqual(before, files()) {
		t.Fatal("continue wrote a file; it must write no handoff record")
	}
}
