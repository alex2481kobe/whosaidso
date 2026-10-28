// Git artifact reads reject oversized or undecidable lengths before requesting
// blob bytes. The same resolver limit applies to batch and single-query runners.
package evidence

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestGitBlobChecksSizeBeforeReadingBytes(t *testing.T) {
	root := newRepo(t)
	for _, tc := range []struct {
		declared, body string
		allowed        bool
	}{{"5\n", "hello", true}, {"0\n", "", true}, {"6\n", "unused", false}, {"-1\n", "unused", false}, {"unknown\n", "unused", false}, {"9223372036854775808\n", "unused", false}} {
		t.Run(strings.TrimSpace(tc.declared), func(t *testing.T) {
			reads := 0
			r := NewResolver(root)
			r.MaxBytes = 5
			r.Git = func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if len(args) == 2 && args[0] == "rev-parse" {
					return []byte("sha1\n"), nil
				}
				if len(args) == 3 && args[0] == "cat-file" {
					switch args[1] {
					case "-t":
						if strings.Contains(args[2], ":") {
							return []byte("blob\n"), nil
						}
						return []byte("commit\n"), nil
					case "-s":
						return []byte(tc.declared), nil
					case "blob":
						reads++
						return []byte(tc.body), nil
					}
				}
				return nil, fmt.Errorf("unexpected Git query: %q", args)
			}
			got, err := r.Resolve(context.Background(), gitRef("sha1", strings.Repeat("a", 40), "file.txt", "whole", ""))
			if tc.allowed {
				if err != nil || reads != 1 || string(got.Bytes) != tc.body {
					t.Fatalf("control: %q reads=%d error=%v", got.Bytes, reads, err)
				}
			} else if err == nil || reads != 0 {
				t.Fatalf("invalid length materialized bytes: reads=%d error=%v", reads, err)
			}
		})
	}
}
