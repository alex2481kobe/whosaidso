// Roster parsing uses the existing string config grammar. Ledger state and
// actor identity authentication do not belong to this config test.
package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestConfigTeam(t *testing.T) {
	for _, tc := range []struct {
		line string
		want []string
		bad  bool
	}{
		{"", nil, false},
		{"team = 'worker-a, worker-b, worker-a' # roster", []string{"worker-a", "worker-b"}, false},
		{`team = "worker-a, worker-\u0062"`, []string{"worker-a", "worker-b"}, false},
		{"team = 'worker-a,,worker-b'", nil, true},
		{"team = 'worker-a,'", nil, true},
		{"team = '  '", nil, true},
		{"team = ['worker-a']", nil, true},
	} {
		t.Run(tc.line, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "whosaidso.toml"), []byte("id = 'test/team'\nledger = '.whosaidso/events'\n"+tc.line), 0600); err != nil {
				t.Fatal(err)
			}
			p, err := Discover(root)
			if (err != nil) != tc.bad || !tc.bad && !reflect.DeepEqual(p.Policy.Team, tc.want) {
				t.Fatalf("roster = %v, error = %v; want %v, bad=%v", p.Policy.Team, err, tc.want, tc.bad)
			}
		})
	}
}
