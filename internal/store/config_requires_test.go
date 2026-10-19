// requires is a strict semantic version in config; command behavior belongs
// in the CLI tests, not in these parser fixtures.
package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
