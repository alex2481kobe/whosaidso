package evidence

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"datum/internal/model"
)

func artifactLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func artifactRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestArtifactFilesystemContainment(t *testing.T) {
	for _, route := range []string{"file", "parent", "nested-parent", "chain", "parent-chain", "store-file", "store-parent", "custom-store"} {
		t.Run(route, func(t *testing.T) {
			base := artifactRoot(t)
			root := filepath.Join(base, "root")
			// A sibling with the root as a string prefix is still outside it.
			outside := filepath.Join(base, "rootmalicious")
			const body = `{"value":1}`
			writeFile(t, root, "inside.json", body)
			writeFile(t, outside, "source.json", body)
			ref := contentRef(body, "application/json", []string{"escape.json"}, "whole", "")
			r := NewResolver(root)
			target := filepath.Join(outside, "source.json")
			switch route {
			case "file":
				artifactLink(t, target, filepath.Join(root, "escape.json"))
			case "parent", "nested-parent":
				parent := "escape"
				if route == "nested-parent" {
					parent = "nested/escape"
				}
				artifactLink(t, outside, filepath.Join(root, parent))
				ref.Content.Locators[0].Path = parent + "/source.json"
			case "chain":
				artifactLink(t, target, filepath.Join(root, "hop.json"))
				artifactLink(t, "hop.json", filepath.Join(root, "escape.json"))
			case "parent-chain":
				artifactLink(t, outside, filepath.Join(root, "hop"))
				artifactLink(t, "hop", filepath.Join(root, "escape"))
				ref.Content.Locators[0].Path = "escape/source.json"
			case "store-file":
				ref.Content.Locators = []model.Locator{}
				artifactLink(t, target, filepath.Join(root, DefaultArtifactDir, string(ref.Content.SHA256)))
			case "store-parent", "custom-store":
				ref.Content.Locators = []model.Locator{}
				if route == "custom-store" {
					r.ArtifactDir = "cache/blobs"
				}
				target = filepath.Join(outside, string(ref.Content.SHA256))
				writeFile(t, outside, string(ref.Content.SHA256), body)
				artifactLink(t, outside, filepath.Join(root, r.ArtifactDir))
			}
			got, err := r.Resolve(context.Background(), ref)
			wantFault(t, err, "unavailable")
			if len(got.Bytes) != 0 {
				t.Fatalf("escaped bytes returned: %q", got.Bytes)
			}
			for _, detail := range []string{"resolved outside the root", root, target} {
				if !strings.Contains(err.Error(), detail) {
					t.Errorf("fault must name containment failure, root and destination; missing %q: %v", detail, err)
				}
			}
		})
	}
}

func TestArtifactInsideSymlinksResolve(t *testing.T) {
	for _, rootRoute := range []string{"direct", "symlink", "symlink-ancestor"} {
		for _, route := range []string{"file", "parent", "chain", "store-file", "store-parent"} {
			t.Run(rootRoute+"/"+route, func(t *testing.T) {
				base := artifactRoot(t)
				root := filepath.Join(base, "real", "project")
				const body = `{"value":1}`
				writeFile(t, root, "data/source.json", body)
				ref := contentRef(body, "application/json", []string{"source.json"}, "whole", "")
				origin := OriginLocator
				switch route {
				case "file":
					artifactLink(t, "data/source.json", filepath.Join(root, "source.json"))
				case "parent":
					artifactLink(t, "data", filepath.Join(root, "alias"))
					ref.Content.Locators[0].Path = "alias/source.json"
				case "chain":
					artifactLink(t, "data/source.json", filepath.Join(root, "hop.json"))
					artifactLink(t, "hop.json", filepath.Join(root, "source.json"))
				case "store-file":
					ref.Content.Locators = []model.Locator{}
					origin = OriginArtifactStore
					artifactLink(t, filepath.Join(root, "data/source.json"), filepath.Join(root, DefaultArtifactDir, string(ref.Content.SHA256)))
				case "store-parent":
					ref.Content.Locators = []model.Locator{}
					origin = OriginArtifactStore
					writeFile(t, root, "data/"+string(ref.Content.SHA256), body)
					artifactLink(t, filepath.Join(root, "data"), filepath.Join(root, DefaultArtifactDir))
				}
				if rootRoute == "symlink" {
					artifactLink(t, root, filepath.Join(base, "alias"))
					root = filepath.Join(base, "alias")
				} else if rootRoute == "symlink-ancestor" {
					artifactLink(t, filepath.Join(base, "real"), filepath.Join(base, "alias"))
					root = filepath.Join(base, "alias", "project")
				}
				got, err := NewResolver(root).Resolve(context.Background(), ref)
				if err != nil {
					t.Fatalf("legitimate in-root symlink refused: %v", err)
				}
				if string(got.Bytes) != body || got.Origin != origin {
					t.Fatalf("wrong bytes or origin: %+v", got)
				}
			})
		}
	}
}

func TestResolveSelectorValidation(t *testing.T) {
	root := t.TempDir()
	const body = `{"value":1}`
	writeFile(t, root, "source.json", body)
	for _, pointer := range []string{"value", "/value~", "/value~2", "/valid~0/bad~3"} {
		t.Run(pointer, func(t *testing.T) {
			ref := contentRef(body, "application/json", []string{"source.json"}, "json-pointer", pointer)
			_, err := NewResolver(root).Resolve(context.Background(), ref)
			wantFault(t, err, "invalid-field")
		})
	}
	for _, pointer := range []string{"", "/value", "/a~1b/~0", "/~01"} {
		ref := contentRef(body, "application/json", []string{"source.json"}, "json-pointer", pointer)
		if _, err := NewResolver(root).Resolve(context.Background(), ref); err != nil {
			t.Errorf("valid selector %q refused: %v", pointer, err)
		}
	}
}
