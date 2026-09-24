package main

// What `whosaidso run` prints: its keys and its stdout/stderr tails. Whether the
// run itself is recorded and admitted is tested in run_test.go.

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

func TestRunPrintsSnakeCaseKeysAndReadableTails(t *testing.T) {
	root, _, instrument, attempt := e2eWorld(t)
	run := func(script string) map[string]any {
		t.Helper()
		proofWrite(t, root, "tools/talk.sh", script)
		out, _ := e2eInvoke(t, root, nil, "run", "--attempt-id", string(attempt), "--instrument", string(instrument.RecordID), "--", "/bin/sh", "tools/talk.sh")
		var printed map[string]any
		if err := json.Unmarshal(out, &printed); err != nil {
			t.Fatalf("run printed unparseable output %q: %v", out, err)
		}
		return printed
	}
	keys := func(m map[string]any) []string {
		out := []string{}
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	text := run("printf 'measured \\316\\273\\n'; printf 'warned\\n' >&2\n")
	if want := []string{"artifact_dir", "envelope", "seal_packet", "start_packet", "stderr_tail", "stdout_tail"}; !reflect.DeepEqual(keys(text), want) {
		t.Fatalf("run must print %v, got %v", want, keys(text))
	}
	if text["stdout_tail"] != "measured λ\n" || text["stderr_tail"] != "warned\n" {
		t.Fatalf("valid UTF-8 tails must print as text, got %q and %q", text["stdout_tail"], text["stderr_tail"])
	}
	binary := run("printf '\\377\\376'\n")
	if _, isText := binary["stdout_tail"]; isText || binary["stdout_tail_base64"] != base64.StdEncoding.EncodeToString([]byte{0xff, 0xfe}) || binary["stderr_tail"] != "" {
		t.Fatalf("a tail that is not UTF-8 must print only as base64, got %v", binary)
	}
}
