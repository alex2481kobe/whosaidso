package write

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

// A cancellation can kill a child that already exited but is not yet reaped;
// Kill still succeeds then. On POSIX the child's own exit status must stand;
// on Windows, where the runner's kill is an exit code, the flag decides.
func TestRunOutcomeKeepsTheExitStatusOfAChildKilledAfterItExited(t *testing.T) {
	err := exec.Command(os.Args[0], "-test.run=^TestRunChildProcess$", "--").Run()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 90 {
		t.Fatalf("child without a mode should exit 90: %v", err)
	}
	for _, c := range []struct {
		killed, windows bool
		kind            string
	}{
		{true, false, "exit"},
		{false, false, "exit"},
		{false, true, "exit"},
		{true, true, "signal"},
	} {
		got, ok := runOutcome(exited.ProcessState, c.killed, c.windows)
		if !ok || got.Kind != c.kind {
			t.Fatalf("killed=%v windows=%v: outcome %+v, want %s", c.killed, c.windows, got, c.kind)
		}
		if c.kind == "exit" && (got.ExitCode == nil || *got.ExitCode != 90) {
			t.Fatalf("killed=%v windows=%v: exit %+v, want 90", c.killed, c.windows, got)
		}
		if c.kind == "signal" && (got.Signal == nil || *got.Signal != syscall.SIGKILL.String()) {
			t.Fatalf("killed=%v windows=%v: signal %+v, want %s", c.killed, c.windows, got, syscall.SIGKILL)
		}
	}
}
