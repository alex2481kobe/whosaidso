package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// stale_after is optional, defaults to 24h, and must be a positive duration.
func TestConfigStaleAfter(t *testing.T) {
	for _, tc := range []struct {
		line string
		want time.Duration
		ok   bool
	}{
		{"", DefaultStaleAfter, true},
		{"stale_after = '90m'\n", 90 * time.Minute, true},
		{"stale_after = '-1h'\n", 0, false},
		{"stale_after = 'soon'\n", 0, false},
	} {
		root := t.TempDir()
		body := "id = 'team/project'\nledger = '.whosaidso/events'\n" + tc.line
		if err := os.WriteFile(filepath.Join(root, "whosaidso.toml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		p, err := Discover(root)
		if tc.ok != (err == nil) || tc.ok && p.Policy.StaleAfter != tc.want {
			t.Errorf("%q: got %v, %v; want %v ok=%v", tc.line, p.Policy.StaleAfter, err, tc.want, tc.ok)
		}
	}
}
