// These tests exercise the version gate through dispatch with synthetic build
// metadata and projects. They do not exercise concurrent config edits.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/buildinfo"
)

func requiresBuild(t *testing.T, version string) {
	t.Helper()
	old := buildinfo.Version
	t.Cleanup(func() { buildinfo.Version = old })
	buildinfo.Version = func() string {
		if version == "(devel)" {
			return "dev"
		}
		return version
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

func TestRequiresCLICaptureReportsStoreRefusal(t *testing.T) {
	requiresBuild(t, "v0.1.7")
	t.Setenv("WHOSAIDSO_HOME", t.TempDir())
	root, input := cliFixture(t)
	requiresConfig(t, root, "v0.1.8")
	for _, args := range [][]string{{"capture"}, {"capture", "--admit", "--reason", "reviewed"}} {
		out, message, code := cliRun(t, root, input, "worker", args...)
		if code != 1 || out != "" || !strings.Contains(message, "version-required") {
			t.Fatalf("%v: exit %d stdout %q stderr %q", args, code, out, message)
		}
		for _, want := range []string{"v0.1.7", "v0.1.8", "go install github.com/alex2481kobe/whosaidso/cmd/whosaidso@v0.1.8"} {
			if !strings.Contains(message, want) {
				t.Errorf("refusal %q lacks %q", message, want)
			}
		}
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
			root, input := cliFixture(t)
			requiresConfig(t, root, tc.required)
			_, stderr, code := cliRun(t, root, input, "worker", "capture")
			if code != 1 || !strings.Contains(stderr, "go install") {
				t.Fatalf("exit %d: %s", code, stderr)
			}
		})
	}
}
