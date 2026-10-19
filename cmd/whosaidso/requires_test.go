// These tests exercise the version gate through dispatch with synthetic build
// metadata and projects. They do not exercise concurrent config edits.
package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

func requiresBuild(t *testing.T, version string) {
	t.Helper()
	old := readBuildInfo
	t.Cleanup(func() { readBuildInfo = old })
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: version}}, true
	}
}

func requiresConfig(t *testing.T, root, version string) {
	t.Helper()
	body := "id = 'test/cli'\nledger = '.whosaidso/events'\n"
	if version != "" {
		body += "requires = '" + version + "'\n"
	}
	if err := os.WriteFile(filepath.Join(root, "whosaidso.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRequiresOlderBuildRefusesEveryWriteEntrance(t *testing.T) {
	requiresBuild(t, "v0.1.7")
	t.Setenv("WHOSAIDSO_HOME", t.TempDir())
	root := t.TempDir()
	requiresConfig(t, root, "v0.1.8")
	for _, args := range [][]string{
		{"capture"}, {"capture", "--admit", "--reason", "reviewed"},
		{"admit", "packet"}, {"handback"}, {"run", "--", "unused"}, {"reconcile"},
		{"template", "task.create", "--capture"},
		{"template", "task.create", "--capture=true", "--admit", "--reason", "reviewed"},
		{"template", "source.intake", "--capture", "--pin", "source_ref=missing"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := whosaidso(context.Background(), args, root, strings.NewReader("invalid input"), &out, &errOut, os.Getenv)
			message := errOut.String()
			if code != 2 || out.Len() != 0 || strings.Count(message, "\n") != 1 {
				t.Fatalf("exit %d stdout %q stderr %q; want exit 2 and one refusal", code, out.String(), message)
			}
			for _, want := range []string{"v0.1.7", "v0.1.8", "go install github.com/alex2481kobe/whosaidso/cmd/whosaidso@v0.1.8"} {
				if !strings.Contains(message, want) {
					t.Errorf("refusal %q lacks %q", message, want)
				}
			}
		})
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("refused writes changed project: %v, %v", entries, err)
	}
	entries, err = os.ReadDir(os.Getenv("WHOSAIDSO_HOME"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("refused writes changed home: %v, %v", entries, err)
	}
}

func TestRequiresReadWarningsPreserveAnswers(t *testing.T) {
	t.Setenv("WHOSAIDSO_HOME", t.TempDir())
	for _, version := range []string{"v0.1.7", "(devel)"} {
		t.Run(version, func(t *testing.T) {
			requiresBuild(t, version)
			root, _ := cliFixture(t)
			for _, args := range [][]string{{"todo"}, {"show"}, {"continue", string(cliID(1))}, {"check", "claim", string(cliID(1))}, {"history"}} {
				requiresConfig(t, root, "")
				out, _, code := cliRun(t, root, nil, "", args...)
				requiresConfig(t, root, "v0.1.8")
				got, warning, gotCode := cliRun(t, root, nil, "", args...)
				if gotCode != code || got != out || strings.Count(warning, "\n") != 1 || !strings.HasPrefix(warning, "warning: ") || !strings.Contains(warning, "v0.1.8") {
					t.Errorf("%v: exit %d/%d answer equal=%v warning %q", args, gotCode, code, got == out, warning)
				}
				want := version
				if version == "(devel)" {
					want = "dev"
				}
				if !strings.Contains(warning, want) {
					t.Errorf("warning %q lacks running version %s", warning, want)
				}
			}
		})
	}
}

func TestRequiresSatisfiedBuildsCanCaptureAndAdmit(t *testing.T) {
	t.Setenv("WHOSAIDSO_HOME", t.TempDir())
	for _, tc := range []struct{ running, required string }{
		{"v0.1.8", "v0.1.8"}, {"v0.1.9", "v0.1.8"}, {"v0.1.10", "v0.1.9"},
		{"v0.1.8+build.2", "v0.1.8+build.1"}, {"v0.1.8", "v0.1.8-rc.1"},
		{"v0.1.8-rc.10", "v0.1.8-rc.9"}, {"(devel)", "v0.1.8"},
	} {
		t.Run(tc.running+"/"+tc.required, func(t *testing.T) {
			requiresBuild(t, tc.running)
			root, input := cliFixture(t)
			requiresConfig(t, root, tc.required)
			_, stderr, code := cliRun(t, root, input, "worker", "capture", "--admit", "--reason", "checked", "--json")
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			if tc.running == "(devel)" {
				if strings.Count(stderr, "warning:") != 1 || !strings.Contains(stderr, "dev") || !strings.Contains(stderr, tc.required) {
					t.Fatalf("development warning %q", stderr)
				}
			} else if stderr != "" {
				t.Fatalf("satisfied build warned: %q", stderr)
			}
		})
	}
}

func TestRequiresSemverOlderBuildsAreRefused(t *testing.T) {
	t.Setenv("WHOSAIDSO_HOME", t.TempDir())
	for _, tc := range []struct{ running, required string }{
		{"v0.1.9", "v0.1.10"}, {"v0.1.8-rc.1", "v0.1.8"},
		{"v0.1.8-rc.9", "v0.1.8-rc.10"}, {"v0.1.8-1", "v0.1.8-alpha"},
		{"v0.1.8-alpha", "v0.1.8-alpha.1"},
		{"v0.1.8-1", "v0.1.8--"},
	} {
		t.Run(tc.running+"/"+tc.required, func(t *testing.T) {
			requiresBuild(t, tc.running)
			root := t.TempDir()
			requiresConfig(t, root, tc.required)
			_, stderr, code := cliRun(t, root, nil, "", "capture")
			if code != 2 || !strings.Contains(stderr, "go install") {
				t.Fatalf("exit %d: %s", code, stderr)
			}
		})
	}
}
