package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"whosaidso/internal/model"
)

func TestIDPrintsRequestedCountOfValidIDs(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want int
	}{{nil, 1}, {[]string{"3"}, 3}} {
		out, errs, code := cliRun(t, t.TempDir(), nil, "", append([]string{"id"}, tc.args...)...)
		if code != 0 || errs != "" {
			t.Fatalf("args %v: code %d, stderr %q", tc.args, code, errs)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) != tc.want {
			t.Fatalf("args %v: got %d ids, want %d", tc.args, len(lines), tc.want)
		}
		seen := map[string]bool{}
		for _, line := range lines {
			if !model.ValidID(model.ID(line)) {
				t.Errorf("%q is not a valid id", line)
			}
			if seen[line] {
				t.Errorf("duplicate id %q", line)
			}
			seen[line] = true
		}
	}
}

func TestIDRefusesBadCount(t *testing.T) {
	for _, arg := range []string{"0", "-1", "x", "--help"} {
		out, errs, code := cliRun(t, t.TempDir(), nil, "", "id", arg)
		if arg == "--help" {
			// id --help is usage, never read as a count.
			if code != 0 || !strings.HasPrefix(out, "whosaidso id [N]\n") || errs != "" {
				t.Errorf("id --help: exit %d, stdout %q stderr %q", code, out, errs)
			}
			continue
		}
		if code != 2 {
			t.Errorf("%q: exit %d, want 2", arg, code)
		}
		if out != "" || !strings.Contains(errs, "is not a count of one or more") && !strings.Contains(errs, "flag provided but not defined") {
			t.Errorf("%q: stdout %q stderr %q", arg, out, errs)
		}
	}
}

func TestIDDispatchedFromMain(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, int) {
		command := exec.Command(binary, append([]string{"-test.run=^TestWhoSaidSoMainProcess$", "--", "id"}, args...)...)
		command.Env = append(os.Environ(), "WHOSAIDSO_MAIN_TEST_PROCESS=1")
		output, err := command.CombinedOutput()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return string(output), exit.ExitCode()
		}
		return string(output), 0
	}
	if out, code := run("2"); code != 0 || len(strings.Fields(out)) != 2 || !model.ValidID(model.ID(strings.Fields(out)[0])) {
		t.Errorf("whosaidso id 2: exit %d, output %q", code, out)
	}
	if out, code := run("nope"); code != 2 || !strings.Contains(out, "not a count") {
		t.Errorf("whosaidso id nope: exit %d, output %q", code, out)
	}
}
