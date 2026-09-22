package model

// Direct-reference checks must enforce the schema's shared path vocabulary.

import (
	"fmt"
	"strings"
	"testing"
)

func TestArtifactRefPathSafety(t *testing.T) {
	for _, kind := range []string{"git", "content"} {
		for _, field := range []string{"git", "locator"} {
			for i, tc := range []struct {
				path string
				good bool
			}{
				{"src/file.json", true}, {"./src/file.json", true}, {"évidence.json", true},
				{"", false}, {" \t", false}, {"\u200b", false},
				{"../secret", false}, {"src/../secret", false}, {"src/..", false},
				{"/tmp/secret", false}, {`C:\secret`, false}, {`src\secret`, false},
				{"C:secret", false}, {"src\x00secret", false},
			} {
				t.Run(fmt.Sprintf("%s/%s/%d", kind, field, i), func(t *testing.T) {
					ref := ArtifactRef{
						Kind: kind, Selector: Selector{Kind: "whole"},
						Git: &GitPin{ObjectFormat: "sha1", Commit: strings.Repeat("a", 40), Path: "source.json"},
						Content: &ContentPin{SHA256: HashBytes([]byte("x")), Length: 1, MediaType: "application/json",
							Locators: []Locator{{Path: "first.json"}, {Path: "second.json"}}},
					}
					at := "ref.git.path"
					if field == "git" {
						ref.Git.Path = tc.path
					} else {
						ref.Content.Locators[1].Path = tc.path
						at = "ref.content.locators[1].path"
					}
					err := ValidateArtifactRef(ref, "ref")
					if tc.good {
						if err != nil {
							t.Fatalf("safe path rejected: %v", err)
						}
					} else if f, ok := err.(*Fault); !ok || f.Code != "invalid-field" || f.Path != at {
						t.Errorf("unsafe path %q must fail at %s: %v", tc.path, at, err)
					}
					if schemaErr := ValidateSchema(ref); (schemaErr == nil) != tc.good {
						t.Errorf("schema disagrees for %q: %v", tc.path, schemaErr)
					}
				})
			}
		}
	}
}
