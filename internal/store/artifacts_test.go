package store

// Durable artifact publication and run staging placement. Which bytes may be
// published is admission's question and is tested in internal/write.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"datum/internal/model"
)

const artifactTestDir = ".datum/artifacts"

func TestPublishArtifactOneCopyByDigest(t *testing.T) {
	root := t.TempDir()
	data := []byte(`{"measured":true}`)
	for i := 0; i < 2; i++ {
		if err := PublishArtifact(root, artifactTestDir, data); err != nil {
			t.Fatalf("control publication %d: %v", i, err)
		}
	}
	store := filepath.Join(root, ".datum", "artifacts")
	entries, err := os.ReadDir(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != string(model.HashBytes(data)) {
		t.Fatalf("publishing twice must leave one file named by digest, got %v", entries)
	}
	if got, err := os.ReadFile(filepath.Join(store, entries[0].Name())); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("published bytes = %q, %v", got, err)
	}
}

func TestPublishArtifactRefusals(t *testing.T) {
	data := []byte("the verified bytes")
	for _, tc := range []struct {
		name string
		code string
		// arrange prepares root and returns the root to publish into.
		arrange func(t *testing.T, root string) string
	}{
		{"relative root", "invalid-field", func(t *testing.T, root string) string { return "relative/root" }},
		{"store directory is a symlink", "invalid-field", func(t *testing.T, root string) string {
			mustMkdir(t, filepath.Join(root, ".datum"))
			mustSymlink(t, t.TempDir(), filepath.Join(root, ".datum", "artifacts"))
			return root
		}},
		{"digest name holds other bytes", "conflict", func(t *testing.T, root string) string {
			mustMkdir(t, filepath.Join(root, ".datum", "artifacts"))
			putFile(t, filepath.Join(root, ".datum", "artifacts", string(model.HashBytes(data))), []byte("other bytes"))
			return root
		}},
		{"digest name holds a longer file", "conflict", func(t *testing.T, root string) string {
			mustMkdir(t, filepath.Join(root, ".datum", "artifacts"))
			putFile(t, filepath.Join(root, ".datum", "artifacts", string(model.HashBytes(data))), append(append([]byte{}, data...), '!'))
			return root
		}},
		{"digest name is a symlink to the right bytes", "invalid-field", func(t *testing.T, root string) string {
			mustMkdir(t, filepath.Join(root, ".datum", "artifacts"))
			elsewhere := filepath.Join(t.TempDir(), "copy")
			putFile(t, elsewhere, data)
			mustSymlink(t, elsewhere, filepath.Join(root, ".datum", "artifacts", string(model.HashBytes(data))))
			return root
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := PublishArtifact(t.TempDir(), artifactTestDir, data); err != nil {
				t.Fatalf("control: %v", err)
			}
			requireFault(t, PublishArtifact(tc.arrange(t, t.TempDir()), artifactTestDir, data), tc.code)
		})
	}
}

// Publication reads the destination back. A link that lands different bytes
// under the digest name (a racing writer, a lying filesystem) must be refused,
// not acknowledged as the verified artifact.
func TestPublishArtifactVerifiesTheDestination(t *testing.T) {
	data := []byte("the verified bytes")
	honest := artifactIO{link: os.Link}
	if err := publishArtifact(t.TempDir(), artifactTestDir, data, honest); err != nil {
		t.Fatalf("control: %v", err)
	}
	swapped := artifactIO{link: func(_, newname string) error {
		return os.WriteFile(newname, []byte("not the verified bytes"), 0600)
	}}
	requireFault(t, publishArtifact(t.TempDir(), artifactTestDir, data, swapped), "conflict")
}

func TestRunStagingLivesInTheDatumHomeOutsideTheProject(t *testing.T) {
	p := intakeProject(t)
	id := commandID(1)
	dir, err := MakeRunStaging(p, id)
	if err != nil {
		t.Fatalf("control: %v", err)
	}
	inbox, err := IntakeDir(p)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Dir(filepath.Dir(inbox))
	if want := filepath.Join(home, "staging", filepath.Base(inbox), string(id)); dir != want {
		t.Fatalf("staging = %s, want %s beside intake in the Datum home", dir, want)
	}
	if rel, err := filepath.Rel(p.Root, dir); err != nil || !strings.HasPrefix(rel, "..") {
		t.Fatalf("staging %s must be outside the project %s", dir, p.Root)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatalf("staging must be a private directory: %v, %v", info, err)
	}

	t.Run("an existing invocation directory is not fresh", func(t *testing.T) {
		_, err := MakeRunStaging(p, id)
		requireFault(t, err, "io")
	})
	t.Run("a symlinked staging directory", func(t *testing.T) {
		q := intakeProject(t)
		qInbox, err := IntakeDir(q)
		if err != nil {
			t.Fatal(err)
		}
		mustMkdir(t, filepath.Join(filepath.Dir(filepath.Dir(qInbox)), "staging"))
		mustSymlink(t, t.TempDir(), filepath.Join(filepath.Dir(filepath.Dir(qInbox)), "staging", filepath.Base(qInbox)))
		_, err = MakeRunStaging(q, id)
		requireFault(t, err, "invalid-field")
	})
	t.Run("an invalid invocation id", func(t *testing.T) {
		_, err := MakeRunStaging(p, "../escape")
		requireFault(t, err, "invalid-field")
	})
}
