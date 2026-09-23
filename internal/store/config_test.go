package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"datum/internal/model"
)

func TestDiscoverNearestAndRelativeLedger(t *testing.T) {
	root := t.TempDir()
	putFile(t, filepath.Join(root, "datum.toml"), []byte("id = 'parent'\nledger = '.datum/events'\n"))
	nested := filepath.Join(root, "nested")
	cwd := filepath.Join(nested, "a", "b")
	mustMkdir(t, cwd)
	parent, err := Discover(cwd)
	if err != nil || string(parent.ID) != "parent" || parent.Root != root || parent.Ledger != filepath.Join(root, ".datum/events") {
		t.Fatalf("parent discovery: %+v, %v", parent, err)
	}
	putFile(t, filepath.Join(nested, "datum.toml"), []byte("# nearest wins\n'id' = \"team/project # one\" # comment\nledger = '.datum/../events'\n"))
	p, err := Discover(cwd)
	if err != nil || string(p.ID) != "team/project # one" || p.Root != nested || p.Ledger != filepath.Join(nested, "events") {
		t.Fatalf("nearest discovery: %+v, %v", p, err)
	}
}

// Ruling R8.1: the ledger is committed with the project, so a ledger path that
// leaves the datum root is refused, whether absolute, climbing, or linked out.
func TestDiscoverRefusesALedgerLeavingItsRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "proj")
	outside := filepath.Join(parent, "elsewhere")
	mustMkdir(t, filepath.Join(root, ".datum"))
	mustMkdir(t, outside)
	mustSymlink(t, outside, filepath.Join(root, "out"))
	mustSymlink(t, filepath.Join(parent, "missing"), filepath.Join(root, "dangling"))
	for _, ledger := range []string{
		filepath.Join(parent, "ledger"), // absolute
		filepath.Join(root, "events"),   // absolute, even when it names the root
		"..", "../events", ".datum/../../events",
		"../proj-events",    // a string prefix of the root, not a segment
		"out/events", "out", // an in-root link that points out
		".datum/../out/new/events", // a missing tail beneath the link
	} {
		putFile(t, filepath.Join(root, "datum.toml"), []byte("id='p'\nledger='"+ledger+"'"))
		_, err := Discover(root)
		requireFault(t, err, "config-invalid-value")
		var f *model.Fault
		errors.As(err, &f)
		if !strings.Contains(f.Detail, "proj") || !strings.Contains(f.Detail, ledger) {
			t.Errorf("fault for %q must name the ledger and the root: %v", ledger, f)
		}
	}
	putFile(t, filepath.Join(root, "datum.toml"), []byte("id='p'\nledger='dangling/events'"))
	_, err := Discover(root)
	requireFault(t, err, "io")
}

func TestDiscoverKeepsALedgerInsideItsRoot(t *testing.T) {
	// The root itself is reached through a link, as macOS /tmp is.
	real := t.TempDir()
	linked := filepath.Join(t.TempDir(), "linked-root")
	mustSymlink(t, real, linked)
	mustMkdir(t, filepath.Join(real, "store", "events"))
	mustSymlink(t, filepath.Join(real, "store"), filepath.Join(real, ".datum"))
	// "..events" is a name inside the root: segments, not a string prefix.
	for _, ledger := range []string{".datum/events", ".datum/new/events", "fresh/events", "./events", "..events"} {
		putFile(t, filepath.Join(real, "datum.toml"), []byte("id='p'\nledger='"+ledger+"'"))
		p, err := Discover(linked)
		if err != nil || p.Root != linked || p.Ledger != filepath.Join(linked, ledger) {
			t.Fatalf("in-root ledger %q: %+v, %v", ledger, p, err)
		}
	}
}

func TestConfigStringSubset(t *testing.T) {
	tests := []struct{ text, id, ledger string }{
		{"id='a/b'\nledger='.datum/events'", "a/b", ".datum/events"},
		{"\t\"id\" = \"slash\\\\quote\\\"\\t\\b\\f\\n\\r\\u03BB\\U0001F40B\"\r\nledger='C:\\literal\\path#x' # comment\r\n", "slash\\quote\"\t\b\f\n\rλ\U0001F40B", `C:\literal\path#x`},
		{"id='space # = literal' # comment\nledger=\".datum/#events\"", "space # = literal", ".datum/#events"},
	}
	for _, tt := range tests {
		values, err := parseConfig([]byte(tt.text), "datum.toml")
		if err != nil || values["id"] != tt.id || values["ledger"] != tt.ledger {
			t.Fatalf("parse %q: %v, %v", tt.text, values, err)
		}
	}
}

func TestConfigRefusals(t *testing.T) {
	tests := []struct{ name, text, code string }{
		{"duplicate", "id='one'\nid='two'\nledger='x'", "config-duplicate-key"},
		{"quoted duplicate", "id='one'\n\"id\"='two'\nledger='x'", "config-duplicate-key"},
		{"unknown", "id='one'\nledger='x'\nextra='no'", "config-unknown-key"},
		{"missing id", "ledger='x'", "config-missing-key"},
		{"missing ledger", "id='one'", "config-missing-key"},
		{"empty id", "id=''\nledger='x'", "config-invalid-value"},
		{"empty ledger", "id='one'\nledger=''", "config-invalid-value"},
		{"table", "[project]\nid='one'\nledger='x'", "config-syntax"},
		{"array table", "[[project]]\nid='one'\nledger='x'", "config-syntax"},
		{"dotted key", "id.part='one'\nledger='x'", "config-syntax"},
		{"number", "id=12\nledger='x'", "config-syntax"},
		{"boolean", "id=true\nledger='x'", "config-syntax"},
		{"array", "id=['one']\nledger='x'", "config-syntax"},
		{"inline table", "id={value='one'}\nledger='x'", "config-syntax"},
		{"multiline basic", "id=\"\"\"one\"\"\"\nledger='x'", "config-syntax"},
		{"multiline literal", "id='''one'''\nledger='x'", "config-syntax"},
		{"unterminated", "id='one\nledger='x'", "config-syntax"},
		{"two assignments", "id='one' ledger='x'", "config-syntax"},
		{"trailing text", "id='one' junk\nledger='x'", "config-syntax"},
		{"go escape", "id=\"\\x41\"\nledger='x'", "config-syntax"},
		{"slash escape", "id=\"\\/\"\nledger='x'", "config-syntax"},
		{"surrogate", "id=\"\\uD800\"\nledger='x'", "config-syntax"},
		{"overflow scalar", "id=\"\\U00110000\"\nledger='x'", "config-syntax"},
		{"signed escape", "id=\"\\u+041\"\nledger='x'", "config-syntax"},
		{"NUL", "id=\"\\u0000\"\nledger='x'", "config-invalid-value"},
		{"raw control", "id='one\x01'\nledger='x'", "config-syntax"},
		{"non TOML whitespace", "\u00a0id='one'\nledger='x'", "config-syntax"},
		{"invalid UTF8", "id='\xff'\nledger='x'", "config-syntax"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseConfig([]byte("id='good'\nledger='.datum/events'"), "datum.toml"); err != nil {
				t.Fatalf("good control: %v", err)
			}
			_, err := parseConfig([]byte(tt.text), "datum.toml")
			requireFault(t, err, tt.code)
		})
	}
}

func TestDiscoverRefusesInvalidNearest(t *testing.T) {
	root := t.TempDir()
	putFile(t, filepath.Join(root, "datum.toml"), []byte("id='parent'\nledger='events'"))
	child := filepath.Join(root, "child")
	mustMkdir(t, child)
	if p, err := Discover(child); err != nil || p.ID != "parent" {
		t.Fatalf("good control: %+v, %v", p, err)
	}
	putFile(t, filepath.Join(child, "datum.toml"), []byte("id='child'"))
	_, err := Discover(child)
	requireFault(t, err, "config-missing-key")
}

func TestDiscoverMissingAndNonDirectory(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "datum.toml")
	putFile(t, path, []byte("id='good'\nledger='events'"))
	if _, err := Discover(root); err != nil {
		t.Fatalf("good control: %v", err)
	}
	_, err := Discover(path)
	requireFault(t, err, "invalid-field")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, err = Discover(root)
	requireFault(t, err, "config-not-found")
}

func requireFault(t *testing.T, err error, code string) {
	t.Helper()
	var f *model.Fault
	if !errors.As(err, &f) || f.Code != code || f.Path == "" || strings.TrimSpace(f.Detail) == "" || f.EventIndex != -1 {
		t.Fatalf("expected located %s fault, got %#v", code, err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
}

func putFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

// R13.1: the artifact store is the ledger's sibling in its record folder,
// derived from the configured ledger rather than named a second time.
func TestProjectArtifactDirSitsBesideTheLedger(t *testing.T) {
	root := t.TempDir()
	for ledger, want := range map[string]string{
		".datum/events":        ".datum/artifacts",
		"custom/deep/events":   "custom/deep/artifacts",
		"events":               "artifacts",
		"record/../rec/ledger": "rec/artifacts",
	} {
		p := Project{Root: root, Ledger: filepath.Join(root, ledger)}
		if got := p.ArtifactDir(); got != want {
			t.Errorf("ledger %q: artifact dir %q, want %q", ledger, got, want)
		}
	}
}
