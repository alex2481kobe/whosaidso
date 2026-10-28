package evidence

// Invocation-local Git memoization and object batching live here. Resolver
// validation and ledger policy stay with their existing owners. A session is
// serial, bound to one root and context, and never persists its answers.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

var errGitBatch = errors.New("git batch")

type gitAnswer struct {
	data []byte
	err  error
}

// GitSession shares immutable queries and one cat-file process within a check.
// Call Close after the last query, including when the check fails.
type GitSession struct {
	ctx    context.Context
	root   string
	memo   map[string]gitAnswer
	cmd    *exec.Cmd
	input  io.WriteCloser
	output *bufio.Reader
	failed error
}

func NewGitSession(ctx context.Context, root string) *GitSession {
	return &GitSession{ctx: ctx, root: root, memo: map[string]gitAnswer{}}
}

// Run implements GitRunner. Only object format, commit type and ancestry are
// memoized; path types and bytes use Git's batch protocol without retaining blobs.
func (s *GitSession) Run(ctx context.Context, root string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.failed != nil {
		return nil, s.failed
	}
	if root != s.root {
		return nil, errors.New("Git session used with a different root")
	}
	key := strings.Join(args, "\x00")
	cache := len(args) == 2 && args[0] == "rev-parse" && args[1] == "--show-object-format" ||
		len(args) == 3 && args[0] == "cat-file" && args[1] == "-t" && !strings.Contains(args[2], ":") ||
		len(args) == 4 && args[0] == "merge-base" && args[1] == "--is-ancestor"
	if a, ok := s.memo[key]; cache && ok {
		return a.data, a.err
	}
	var data []byte
	var err error
	if len(args) == 3 && args[0] == "cat-file" && (args[1] == "-t" || args[1] == "-s" || args[1] == "blob") {
		data, err = s.object(args[2], args[1])
	} else {
		data, err = ExecGit(ctx, root, args...)
	}
	if cache {
		s.memo[key] = gitAnswer{data, err}
	}
	return data, err
}

func (s *GitSession) start() error {
	if s.cmd != nil {
		return nil
	}
	cmd := gitCommand(s.ctx, s.root, "cat-file", "--batch-command", "-Z")
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return err
	}
	if err := cmd.Start(); err != nil {
		input.Close()
		output.Close()
		return err
	}
	s.cmd, s.input, s.output = cmd, input, bufio.NewReader(output)
	return nil
}

func (s *GitSession) object(spec string, request string) ([]byte, error) {
	if err := s.start(); err != nil {
		return nil, err
	}
	command := "info"
	if request == "blob" {
		command = "contents"
	}
	if _, err := io.WriteString(s.input, command+" "+spec+"\x00"); err != nil {
		return nil, err
	}
	header, err := s.output.ReadString(0)
	if err != nil {
		s.failed = fmt.Errorf("%w header: %w", errGitBatch, err)
		return nil, s.failed
	}
	header = strings.TrimSuffix(header, "\x00")
	if strings.HasSuffix(header, " missing") {
		return nil, fmt.Errorf("git object not found: %s", spec)
	}
	if strings.ContainsAny(header, "\r\n") {
		return nil, fmt.Errorf("invalid git batch header: %q", header)
	}
	fields := strings.Fields(header)
	if len(fields) != 3 {
		return nil, fmt.Errorf("invalid git batch header: %q", header)
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 {
		return nil, fmt.Errorf("invalid git object size: %q", header)
	}
	if request == "-s" {
		return []byte(fields[2] + "\n"), nil
	}
	if request == "-t" {
		return []byte(fields[1] + "\n"), nil
	}
	// gitBlob still owns the byte limit and object-type refusal. Read the same
	// immutable bytes the single-query runner would return.
	data, err := io.ReadAll(io.LimitReader(s.output, size))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != size {
		return nil, io.ErrUnexpectedEOF
	}
	end, err := s.output.ReadByte()
	if err != nil {
		return nil, err
	}
	if end != 0 {
		return nil, errors.New("invalid git batch content terminator")
	}
	if fields[1] != "blob" {
		return nil, fmt.Errorf("git object is %s, not blob", fields[1])
	}
	return data, nil
}

// Close releases the process and reports a failed batch command.
func (s *GitSession) Close() error {
	if s.cmd == nil {
		return nil
	}
	closeErr := s.input.Close()
	err := s.cmd.Wait()
	s.cmd = nil
	return errors.Join(closeErr, err)
}
