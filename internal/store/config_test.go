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
	putFile(t, filepath.Join(root, "datum.toml"), []byte("id = 'parent'\nledger = 'record/events'\n"))
	nested := filepath.Join(root, "nested")
	cwd := filepath.Join(nested, "a", "b")
	mustMkdir(t, cwd)
	parent, err := Discover(cwd)
	if err != nil || string(parent.ID) != "parent" || parent.Root != root || parent.Ledger != filepath.Join(root, "record/events") {
		t.Fatalf("parent discovery: %+v, %v", parent, err)
	}
	putFile(t, filepath.Join(nested, "datum.toml"), []byte("# nearest wins\n'id' = \"team/project # one\" # comment\nledger = '../events'\n"))
	p, err := Discover(cwd)
	if err != nil || string(p.ID) != "team/project # one" || p.Root != nested || p.Ledger != filepath.Join(root, "events") {
		t.Fatalf("nearest discovery: %+v, %v", p, err)
	}
	absolute := filepath.Join(t.TempDir(), "ledger")
	putFile(t, filepath.Join(nested, "datum.toml"), []byte("id = 'child'\nledger = '"+absolute+"'\n"))
	p, err = Discover(cwd)
	if err != nil || p.Ledger != absolute {
		t.Fatalf("absolute ledger: %+v, %v", p, err)
	}
}

func TestConfigStringSubset(t *testing.T) {
	tests := []struct{ text, id, ledger string }{
		{"id='a/b'\nledger='record/events'", "a/b", "record/events"},
		{"\t\"id\" = \"slash\\\\quote\\\"\\t\\b\\f\\n\\r\\u03BB\\U0001F40B\"\r\nledger='C:\\literal\\path#x' # comment\r\n", "slash\\quote\"\t\b\f\n\rλ\U0001F40B", `C:\literal\path#x`},
		{"id='space # = literal' # comment\nledger=\"record/#events\"", "space # = literal", "record/#events"},
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
			if _, err := parseConfig([]byte("id='good'\nledger='record/events'"), "datum.toml"); err != nil {
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

func putFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
