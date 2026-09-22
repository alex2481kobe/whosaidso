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

func TestArtifactRefSelectorMatchesSchema(t *testing.T) {
	for _, tc := range []struct {
		name     string
		selector Selector
		valid    bool
	}{
		{"whole", Selector{Kind: "whole"}, true},
		{"root", Selector{Kind: "json-pointer"}, true},
		{"member", Selector{Kind: "json-pointer", Pointer: "/value"}, true},
		{"escapes", Selector{Kind: "json-pointer", Pointer: "/a~1b/~0/~01"}, true},
		{"empty member", Selector{Kind: "json-pointer", Pointer: "/"}, true},
		{"missing slash", Selector{Kind: "json-pointer", Pointer: "value"}, false},
		{"dangling escape", Selector{Kind: "json-pointer", Pointer: "/value~"}, false},
		{"invalid escape", Selector{Kind: "json-pointer", Pointer: "/value~2"}, false},
		{"later invalid escape", Selector{Kind: "json-pointer", Pointer: "/good~0/bad~3"}, false},
		{"whole with pointer", Selector{Kind: "whole", Pointer: "/value"}, false},
		{"unknown kind", Selector{Kind: "other"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref := ArtifactRef{Kind: "content", Selector: tc.selector,
				Content: &ContentPin{SHA256: HashBytes([]byte("x")), Length: 1,
					MediaType: "text/plain", Locators: []Locator{}}}
			cheapErr := ValidateArtifactRef(ref, "ref")
			// Validate the selector alone so this oracle does not call the
			// cheap artifact validator through the schema's ArtifactRef case.
			schemaErr := ValidateSchema(tc.selector)
			if (cheapErr == nil) != tc.valid || (schemaErr == nil) != tc.valid {
				t.Fatalf("valid=%v: cheap=%v, schema=%v", tc.valid, cheapErr, schemaErr)
			}
			if !tc.valid {
				f, ok := cheapErr.(*Fault)
				if !ok || f.Code != "invalid-field" || !strings.HasPrefix(f.Path, "ref.selector") {
					t.Errorf("expected selector field fault: %v", cheapErr)
				}
			}
		})
	}
}
