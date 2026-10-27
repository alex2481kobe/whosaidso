package evidence

// Invocation-local Git reads must preserve resolver answers while bounding
// process starts. These tests use only synthetic repositories.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitSessionPreservesResolutionAndBoundsProcesses(t *testing.T) {
	root := newRepo(t)
	head := commitFile(t, root, "nested/a.txt", "old bytes\x00\n")
	head = commitFile(t, root, "nested/space name.txt", "second")
	head = commitIndexOnly(t, root, "nested/new\nline.txt", "third")
	entries, err := os.ReadDir(filepath.Join(root, "nested"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "new\nline.txt" {
			t.Fatal("Git-only filename must not be materialized in the working tree")
		}
	}
	tree := gitRun(t, root, "rev-parse", head+":nested")
	blob := gitRun(t, root, "rev-parse", head+":nested/a.txt")
	writeFile(t, root, "nested/a.txt", "dirty")
	root = filepath.Join(root, "nested")
	trace := filepath.Join(t.TempDir(), "git.jsonl")
	t.Setenv("GIT_TRACE2_EVENT", trace)
	ctx := context.Background()
	session := NewGitSession(ctx, root)
	optimized := NewResolver(root)
	optimized.Git = session.Run
	ordinary := NewResolver(root)
	for _, tc := range []struct {
		commit, path, format string
		limit                int64
	}{
		{head, "a.txt", "sha1", 0}, {head, "space name.txt", "sha1", 0},
		{head, "new\nline.txt", "sha1", 0}, {head, "absent", "sha1", 0},
		{head, ".", "sha1", 0}, {tree, "a.txt", "sha1", 0}, {blob, "a.txt", "sha1", 0},
		{strings.Repeat("a", 40), "a.txt", "sha1", 0},
		{strings.Repeat("a", 64), "a.txt", "sha256", 0}, {head, "a.txt", "sha1", 1},
	} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			ordinary.MaxBytes, optimized.MaxBytes = tc.limit, tc.limit
			ref := gitRef(tc.format, tc.commit, tc.path, "whole", "")
			want, we := ordinary.Resolve(ctx, ref)
			got, ge := optimized.Resolve(ctx, ref)
			if fmt.Sprint(we) != fmt.Sprint(ge) || string(want.Bytes) != string(got.Bytes) {
				t.Fatalf("ordinary %q %v; session %q %v", want.Bytes, we, got.Bytes, ge)
			}
		})
	}
	// Isolate process counts from the ordinary resolver used as an oracle above.
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	trace = filepath.Join(t.TempDir(), "bounded.jsonl")
	t.Setenv("GIT_TRACE2_EVENT", trace)
	session = NewGitSession(ctx, root)
	optimized.Git = session.Run
	optimized.MaxBytes = 0
	for i := 0; i < 12; i++ {
		for _, p := range []string{"a.txt", "space name.txt"} {
			if _, err := optimized.Resolve(ctx, gitRef("sha1", head, p, "whole", "")); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := session.Run(ctx, root, "merge-base", "--is-ancestor", head, head); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	starts := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		if e.Event == "start" {
			starts++
		}
	}
	if starts > 3 {
		t.Fatalf("24 reads of two paths at one commit started %d git processes; want at most format + batch + ancestry (3)", starts)
	}
}

// The protocol stream is the instrument: repeating an immutable commit query
// must not write a second request, while a different commit must be queried.
func TestGitSessionMemoizesCommitExistence(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	var requests bytes.Buffer
	session := NewGitSession(ctx, root)
	session.cmd = &exec.Cmd{}
	session.input = gitTestInput{&requests}
	session.output = bufio.NewReader(strings.NewReader("aaa commit 12\x00bbb commit 13\x00"))
	for _, commit := range []string{"aaa", "aaa", "bbb", "bbb"} {
		got, err := session.Run(ctx, root, "cat-file", "-t", commit)
		if err != nil || string(got) != "commit\n" {
			t.Fatalf("commit %s: %q %v", commit, got, err)
		}
	}
	if got, want := requests.String(), "info aaa\x00info bbb\x00"; got != want {
		t.Fatalf("requests %q, want %q", got, want)
	}
}

type gitTestInput struct{ *bytes.Buffer }

func (gitTestInput) Close() error { return nil }

func TestGitSessionReadsSHA256AndRejectsLFSPointers(t *testing.T) {
	root := newRepo(t, "--object-format=sha256")
	head := commitFile(t, root, "body.txt", "bytes")
	pointer := "version https://git-lfs.github.com/spec/v1\noid sha256:" + strings.Repeat("a", 64) + "\nsize 12\n"
	head = commitFile(t, root, "pointer.txt", pointer)
	session := NewGitSession(context.Background(), root)
	defer func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	}()
	r := NewResolver(root)
	r.Git = session.Run
	if got, err := r.Resolve(context.Background(), gitRef("sha256", head, "body.txt", "whole", "")); err != nil || string(got.Bytes) != "bytes" {
		t.Fatalf("SHA256 control: %q %v", got.Bytes, err)
	}
	if _, err := r.Resolve(context.Background(), gitRef("sha256", head, "pointer.txt", "whole", "")); err == nil || !strings.Contains(err.Error(), "LFS pointer") {
		t.Fatalf("pointer must remain unavailable: %v", err)
	}
}

func TestGitSessionRefusesCanceledQueriesAndFailedProcesses(t *testing.T) {
	root := newRepo(t)
	head := commitFile(t, root, "body.txt", "bytes")
	ctx, cancel := context.WithCancel(context.Background())
	session := NewGitSession(ctx, root)
	if _, err := session.Run(ctx, root, "cat-file", "-t", head); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := session.Run(ctx, root, "cat-file", "-t", head); err == nil {
		t.Fatal("canceled cached query succeeded")
	}
	if err := session.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatal(err)
	}
	if err := session.Close(); err == nil {
		t.Fatal("failed process exit was swallowed")
	}
	session = NewGitSession(context.Background(), root)
	if _, err := session.Run(context.Background(), t.TempDir(), "cat-file", "-t", head); err == nil {
		t.Fatal("session crossed roots")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}

// A partial clone may otherwise turn a supposedly local object read into a
// lazy fetch. The promisor is a synthetic local repository, never a server.
func TestGitSessionNeverFetchesMissingPromisorObjects(t *testing.T) {
	t.Log(gitRun(t, t.TempDir(), "--version"))
	source := newRepo(t)
	head := commitFile(t, source, "body.txt", "pinned bytes")
	gitRun(t, source, "config", "uploadpack.allowFilter", "true")
	root := filepath.Join(t.TempDir(), "partial")
	sourcePath := filepath.ToSlash(source)
	if !strings.HasPrefix(sourcePath, "/") {
		sourcePath = "/" + sourcePath
	}
	sourceURL := (&url.URL{Scheme: "file", Path: sourcePath}).String()
	gitRun(t, source, "clone", "--quiet", "--filter=blob:none", "--no-checkout", sourceURL, root)
	trace := filepath.Join(t.TempDir(), "trace.jsonl")
	t.Setenv("GIT_TRACE2_EVENT", trace)
	session := NewGitSession(context.Background(), root)
	r := NewResolver(root)
	r.Git = session.Run
	_, resolveErr := r.Resolve(context.Background(), gitRef("sha1", head, "body.txt", "whole", ""))
	_, secondErr := session.Run(context.Background(), root, "cat-file", "-t", head)
	closeErr := session.Close()
	if resolveErr == nil {
		t.Fatalf("missing promisor bytes were fetched instead of refused (close: %v)", closeErr)
	}
	if closeErr == nil {
		if !strings.Contains(resolveErr.Error(), "path does not exist at the pinned commit") {
			t.Fatalf("successful batch close needs an explicit missing-path answer: %v", resolveErr)
		}
	} else {
		var exit *exec.ExitError
		if !errors.As(closeErr, &exit) || exit.ExitCode() != 128 || !strings.Contains(resolveErr.Error(), "git batch header") || secondErr == nil {
			t.Fatalf("failed missing-object batch: resolve=%v second=%v close=%v", resolveErr, secondErr, closeErr)
		}
	}
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e struct {
			Event string
			Argv  []string
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		if e.Event == "start" {
			for _, arg := range e.Argv {
				if arg == "fetch" {
					t.Fatalf("local check spawned a fetch: %v", e.Argv)
				}
			}
		}
	}
}

func TestGitSessionReturnsObservedObjectSize(t *testing.T) {
	s := NewGitSession(context.Background(), "fixture")
	s.cmd = &exec.Cmd{}
	s.input = gitTestInput{&bytes.Buffer{}}
	s.output = bufio.NewReader(strings.NewReader("aaa blob 7\x00"))
	got, err := s.Run(context.Background(), "fixture", "cat-file", "-s", "aaa")
	if err != nil || string(got) != "7\n" {
		t.Fatalf("measured object size: %q %v", got, err)
	}
}
