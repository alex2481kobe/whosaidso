package main

// CLI tests for the views' limits, kinds and continuation, and the brief as
// the default text. Other read CLI tests and the readProcess/readJSON helpers
// stay in read_test.go.

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"datum/internal/model"
	"datum/internal/query"
)

func TestReadCLIViewsLimitAndContinueObservation(t *testing.T) {
	root, data := cliFixture(t)
	cliControl(t, root, data)
	for _, args := range [][]string{{"show", "--kind", "instrument"}, {"show"}, {"todo", "--limit", "1"}, {"continue", "--limit", "2", string(cliID(1))}} {
		if _, _, code := cliRun(t, root, nil, "", args...); code != 0 {
			t.Fatalf("view %v must succeed: %d", args, code)
		}
	}
	for _, args := range [][]string{{"continue"}, {"show", "--limit", "1"}, {"todo", string(cliID(1))}, {"todo", "--limit", "-1"}} {
		if output, _, code := cliRun(t, root, nil, "", args...); code != 2 || len(output) != 0 {
			t.Fatalf("invalid view %v must be a usage error without an answer, got %d %s", args, code, output)
		}
	}
	files := func() map[string]string {
		out := map[string]string{}
		filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && !cacheImagePath(path) {
				data, _ := os.ReadFile(path)
				out[path] = string(data)
			}
			return nil
		})
		return out
	}
	before := files()
	answer := readJSON[query.ContinueAnswer](t, readProcess(t, root, nil, "continue", "--json", string(cliID(1))))
	c := answer
	// The fixture is not a git checkout, so HEAD and dirty are UNKNOWN, but the
	// observation time was actually taken.
	if c.Observed == nil || c.Observed.Head.State != model.Unknown || c.Observed.Head.Reason == "" || c.Observed.Dirty.State != model.Unknown ||
		c.Observed.ObservedAt.State != model.Known || c.Observed.ObservedAt.Value.IsZero() {
		t.Fatalf("continue outside git must report UNKNOWN HEAD/dirty with reasons and a real observation time, got %+v", c.Observed)
	}
	if !reflect.DeepEqual(before, files()) {
		t.Fatal("continue wrote a file; it must write no handoff record")
	}
}

// The brief is the default text: concise and watermarked. R19: --full and
// --brief are removed; --json is the complete answer, and the text is the
// brief of that same JSON.
func TestReadCLIBriefIsTheDefault(t *testing.T) {
	root, data := cliFixture(t)
	cliControl(t, root, data)
	out := readProcess(t, root, nil, "todo")
	want := []byte("ready: 1\n  TASK " + string(cliID(1)) + " rev 1 READY next lane\n    exercise the CLI\n")
	if !bytes.Contains(out, want) || !bytes.HasPrefix(out, []byte("todo test/cli KNOWN | watermark sequence 1 ")) || bytes.Contains(out, []byte(`"fact"`)) {
		t.Fatalf("todo must default to the brief, opening with its watermark and one block per record, got\n%s", out)
	}
	brief, _, err := query.ViewBriefOf(readProcess(t, root, nil, "todo", "--json"))
	if err != nil || brief != string(out) {
		t.Fatalf("the default text must be the brief of --json: %v\n%s", err, brief)
	}
}
