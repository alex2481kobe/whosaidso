package evidence

// Batch transport failures must not become successful artifact observations.
// Fixtures exercise framing, resolver limits, and an actual truncated process.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestGitBatchRejectsCRLFHeader(t *testing.T) {
	for _, header := range []string{"aaa blob 3\r\n\x00abc\x00", "aaa blob 3\r\x00abc\x00"} {
		s := NewGitSession(context.Background(), "fixture")
		s.cmd = &exec.Cmd{}
		s.input = gitTestInput{&bytes.Buffer{}}
		s.output = bufio.NewReader(strings.NewReader(header))
		if b, err := s.object("aaa", "blob"); err == nil {
			t.Fatalf("malformed CRLF framing succeeded: %q", b)
		}
	}
}

func TestGitBatchLargeObjectRespectsResolverLimit(t *testing.T) {
	root := newRepo(t)
	body := strings.Repeat("large\r\n", 1<<18)
	head := commitFile(t, root, "large.txt", body)
	s := NewGitSession(context.Background(), root)
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}()
	r := NewResolver(root)
	r.Git = s.Run
	ref := gitRef("sha1", head, "large.txt", "whole", "")
	r.MaxBytes = int64(len(body))
	if got, err := r.Resolve(context.Background(), ref); err != nil || string(got.Bytes) != body {
		t.Fatalf("large binary-safe control: %v", err)
	}
	r.MaxBytes--
	if _, err := r.Resolve(context.Background(), ref); err == nil || !strings.Contains(err.Error(), "exceeds the resolver limit") {
		t.Fatalf("large object escaped limit: %v", err)
	}
}

func TestGitBatchProcessDiesMidRead(t *testing.T) {
	if os.Getenv("WHOSAIDSO_BATCH_CHILD") == "1" {
		_, _ = bufio.NewReader(os.Stdin).ReadString(0)
		fmt.Fprint(os.Stdout, "aaa blob 100\x00partial")
		os.Exit(17)
	}
	ctx := context.Background()
	cmd := exec.Command(os.Args[0], "-test.run=^TestGitBatchProcessDiesMidRead$")
	cmd.Env = append(os.Environ(), "WHOSAIDSO_BATCH_CHILD=1")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := NewGitSession(ctx, "fixture")
	s.cmd, s.input, s.output = cmd, input, bufio.NewReader(output)
	b, readErr := s.object("aaa", "blob")
	closeErr := s.Close()
	if readErr == nil || len(b) != 0 {
		t.Fatalf("truncated process returned success: %q %v", b, readErr)
	}
	if closeErr == nil {
		t.Fatal("failed process exit was swallowed")
	}
}

func TestRequireGitCommitOwnsClassification(t *testing.T) {
	for _, tc := range []struct {
		output string
		err    error
		want   string
	}{
		{"commit\r\n", nil, ""}, {"tree\n", nil, "pinned object is a tree, not a commit"},
		{"", io.ErrUnexpectedEOF, "commit not found in this repository: pinned"},
	} {
		run := func(context.Context, string, ...string) ([]byte, error) { return []byte(tc.output), tc.err }
		err := RequireGitCommit(context.Background(), run, "fixture", "pinned")
		if tc.want == "" {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("classification: %v, want %s", err, tc.want)
		}
	}
}
