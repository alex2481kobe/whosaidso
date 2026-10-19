// Config requirements use strict semantic versions and are refreshed before
// intake writes. Command diagnostics belong in the CLI tests.
package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/buildinfo"
)

func TestWriteIntakeRereadsRequirement(t *testing.T) {
	p := intakeProject(t)
	old := buildinfo.Version
	buildinfo.Version = func() string { return "v0.1.7" }
	t.Cleanup(func() { buildinfo.Version = old })
	config := filepath.Join(p.Root, "whosaidso.toml")
	put := func(version string) {
		t.Helper()
		body := "id = 'team/project'\nledger = '.whosaidso/events'\nrequires = '" + version + "'\n"
		if err := os.WriteFile(config, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	put("v0.1.7")
	p, err := Discover(p.Root)
	if err != nil {
		t.Fatal(err)
	}
	capturedControl(t, p, commandID(1), "control")
	put("v0.1.8")
	_, err = WriteIntake(context.Background(), p, requestFor(commandID(2), "refused"))
	if err == nil || !strings.Contains(err.Error(), "version-required") || !strings.Contains(err.Error(), "v0.1.8") {
		t.Fatalf("stale Project bypassed current requirement: %v", err)
	}
	if _, err := os.Stat(packetDir(t, p, commandID(2))); !os.IsNotExist(err) {
		t.Fatalf("refused intake was published: %v", err)
	}
}

func TestConfigRequiresSemver(t *testing.T) {
	for _, version := range []string{"v0.1.8", "v0.1.10", "v1.2.3-rc.1+build.2", "v999999999999999999999999.0.0"} {
		t.Run(version, func(t *testing.T) {
			root := t.TempDir()
			body := "id = 'team/project'\nledger = '.whosaidso/events'\nrequires = '" + version + "'\n"
			if err := os.WriteFile(filepath.Join(root, "whosaidso.toml"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Discover(root); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConfigRequiresMalformedIsInvalidValue(t *testing.T) {
	for _, version := range []string{"soon", "0.1.8", "v0.1", "v01.1.8", "v0.1.8-01", "v0.1.8-", "v0.1.8+", "v0.1.8+a..b", "v0.1.8 ", "(devel)"} {
		t.Run(version, func(t *testing.T) {
			root := t.TempDir()
			body := "id = 'team/project'\nledger = '.whosaidso/events'\nrequires = '" + version + "'\n"
			if err := os.WriteFile(filepath.Join(root, "whosaidso.toml"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Discover(root)
			if err == nil || !strings.Contains(err.Error(), "config-invalid-value") || !strings.Contains(err.Error(), "requires") {
				t.Fatalf("got %v, want invalid requires value", err)
			}
		})
	}
}
