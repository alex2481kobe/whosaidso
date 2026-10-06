// Package gallery keeps the owner's gallery: files an agent was asked to show,
// each with a short title and note. It is a per-machine convenience list, not
// a record. Its one file lives wherever the caller says (the CLI and the viewer
// use store.GalleryFile, in the WhoSaidSo home, outside every ledger and
// repository), and removing an entry never touches the file it names.
//
// Entries, their limits and the file's reads and writes live here; what a
// file previews as is kind.go's; serving and drawing are internal/ui's.
package gallery

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Entry is one shown file. Path is absolute; AddedBy is "" when no actor
// was supplied, never guessed.
type Entry struct {
	ID      string    `json:"id"`
	Path    string    `json:"path"`
	Title   string    `json:"title"`
	Note    string    `json:"note,omitempty"`
	AddedAt time.Time `json:"added_at"`
	AddedBy string    `json:"added_by,omitempty"`
}

// The gallery holds short words, not documents: a title fits one card line
// and a note a few.
const (
	maxTitle = 120
	maxNote  = 1000
)

// ErrNoEntry is an id the gallery does not hold.
var ErrNoEntry = errors.New("no such gallery entry")

// Load reads every entry in the order they were added. A gallery never
// written is empty, not an error.
func Load(file string) ([]Entry, error) {
	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	entries := []Entry{}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("gallery %s is damaged: %v", file, err)
	}
	return entries, nil
}

// Find returns the entry with id.
func Find(file, id string) (Entry, error) {
	entries, err := Load(file)
	if err != nil {
		return Entry{}, err
	}
	for _, e := range entries {
		if e.ID == id {
			return e, nil
		}
	}
	return Entry{}, ErrNoEntry
}

// Add records path (absolute, or relative to dir) with a title and note.
// It refuses a path that is not an existing regular file, a blank or
// multi-line title, and words past their limits.
func Add(file, dir, path, title, note, by string, now time.Time) (Entry, error) {
	title, note = strings.TrimSpace(title), strings.TrimSpace(note)
	switch {
	case title == "" || strings.ContainsAny(title, "\r\n"):
		return Entry{}, errors.New("a gallery entry needs a one-line --title")
	case utf8.RuneCountInString(title) > maxTitle:
		return Entry{}, fmt.Errorf("the title is longer than %d characters; keep it short and put the rest in --note", maxTitle)
	case utf8.RuneCountInString(note) > maxNote:
		return Entry{}, fmt.Errorf("the note is longer than %d characters", maxNote)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		return Entry{}, fmt.Errorf("cannot add %s: %v", path, err)
	}
	if !info.Mode().IsRegular() {
		return Entry{}, fmt.Errorf("cannot add %s: not a regular file", path)
	}
	entries, err := Load(file)
	if err != nil {
		return Entry{}, err
	}
	id, err := freshID(entries)
	if err != nil {
		return Entry{}, err
	}
	entry := Entry{ID: id, Path: path, Title: title, Note: note, AddedAt: now.UTC().Truncate(time.Second), AddedBy: strings.TrimSpace(by)}
	return entry, save(file, append(entries, entry))
}

// Remove drops the entry with id from the gallery and returns it. The file
// it names is left exactly where it is.
func Remove(file, id string) (Entry, error) {
	entries, err := Load(file)
	if err != nil {
		return Entry{}, err
	}
	for i, e := range entries {
		if e.ID == id {
			return e, save(file, append(entries[:i:i], entries[i+1:]...))
		}
	}
	return Entry{}, ErrNoEntry
}

// freshID is eight random hex digits no current entry holds: short enough
// to type in gallery rm.
func freshID(entries []Entry) (string, error) {
	for {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		id := hex.EncodeToString(b[:])
		taken := false
		for _, e := range entries {
			taken = taken || e.ID == id
		}
		if !taken {
			return id, nil
		}
	}
}

// save replaces the gallery file whole through a rename, so a reader sees
// the old list or the new one, never half of either. Two writers at the
// same instant can still lose one change: the gallery is not a record.
func save(file string, entries []Entry) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".gallery-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}
