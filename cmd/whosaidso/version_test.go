package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// version and --version answer the same single line naming the build, so a
// report can say which code it ran; nothing goes to stderr.
func TestVersionNamesTheBuildOnOneLine(t *testing.T) {
	var answers []string
	for _, args := range [][]string{{"version"}, {"--version"}} {
		var stdout, stderr bytes.Buffer
		code := whosaidso(context.Background(), args, t.TempDir(), strings.NewReader(""), &stdout, &stderr, func(string) string { return "" })
		if code != 0 || stderr.Len() != 0 {
			t.Fatalf("%v: exit %d, stderr %q", args, code, stderr.String())
		}
		line := stdout.String()
		build, found := strings.CutPrefix(line, "whosaidso ")
		if !found || strings.Count(line, "\n") != 1 || !strings.HasSuffix(line, "\n") || strings.TrimSpace(build) == "" {
			t.Fatalf("%v printed %q, want one line \"whosaidso BUILD\"", args, line)
		}
		answers = append(answers, line)
	}
	if answers[0] != answers[1] {
		t.Fatalf("version %q and --version %q differ", answers[0], answers[1])
	}
}

// version takes no arguments: an extra one is a usage error, not ignored.
func TestVersionRefusesArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := whosaidso(context.Background(), []string{"version", "extra"}, t.TempDir(), strings.NewReader(""), &stdout, &stderr, func(string) string { return "" })
	if code != 2 || stdout.Len() != 0 {
		t.Fatalf("exit %d, stdout %q; want usage error 2 and no answer", code, stdout.String())
	}
}
