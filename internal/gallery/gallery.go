// Package gallery captures external media pins and observes local availability.
// Ledger transitions and approval requirements belong to internal/reduce.
package gallery

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

var ErrNoEntry = errors.New("no such gallery entry")

// Entry is a ledger entry with this machine's resolved locator for display.
type Entry struct {
	Locator     string            `json:"-"`
	ID          string            `json:"id"`
	Path        string            `json:"path"`
	Title       string            `json:"title"`
	Note        string            `json:"note,omitempty"`
	AddedAt     time.Time         `json:"added_at"`
	AddedBy     string            `json:"added_by,omitempty"`
	SHA256      model.Digest      `json:"sha256"`
	Length      uint64            `json:"length"`
	Verdict     string            `json:"verdict"`
	VerdictBy   model.Actor       `json:"verdict_by"`
	VerdictNote string            `json:"verdict_note,omitempty"`
	Tasks       []model.RecordRef `json:"tasks"`
}

// Open confines relative locators at open time. Its returned regular-file handle
// is the only handle callers may hash or serve; display paths are not authority.
func Open(checkout, locator string) (*os.File, error) {
	var root *os.Root
	if !filepath.IsAbs(locator) {
		locator = filepath.Clean(filepath.FromSlash(locator))
		if !filepath.IsLocal(locator) {
			return nil, fmt.Errorf("relative gallery locator escapes checkout: %s", locator)
		}
		var err error
		root, err = os.OpenRoot(checkout)
		if err != nil {
			return nil, err
		}
		defer root.Close()
	}
	f, err := openMedia(root, locator)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("not a regular file: %s", locator)
	}
	return f, nil
}

// Pin streams the same handle whose open enforced locator containment.
func Pin(checkout, locator string) (model.GalleryFile, error) {
	f, err := Open(checkout, locator)
	if err != nil {
		return model.GalleryFile{}, err
	}
	defer f.Close()
	return Hash(f, locator)
}

// Hash reads the supplied handle, so callers can verify the exact file they serve.
func Hash(f io.Reader, path string) (model.GalleryFile, error) {
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return model.GalleryFile{}, err
	}
	return model.GalleryFile{SHA256: model.Digest(hex.EncodeToString(h.Sum(nil))), Length: uint64(n), Path: path}, nil
}

func Prepare(dir, path, title, note string, by model.Actor, tasks []model.RecordRef, now time.Time) (model.GalleryAdd, error) {
	locator := path
	if filepath.IsAbs(locator) {
		if rel, err := filepath.Rel(dir, locator); err == nil && filepath.IsLocal(rel) {
			// Only internal absolute locators become portable. Explicit paths
			// to external targets stay absolute, including symlinks.
			if _, err := ResolvePath(dir, rel); err == nil {
				locator = rel
			}
		}
	}
	pin, err := Pin(dir, locator)
	if err != nil {
		return model.GalleryAdd{}, err
	}
	if !filepath.IsAbs(locator) {
		pin.Path = filepath.ToSlash(filepath.Clean(locator))
	}
	id, err := model.NewID(now, rand.Reader)
	if err != nil {
		return model.GalleryAdd{}, err
	}
	if tasks == nil {
		tasks = []model.RecordRef{}
	}
	e := model.GalleryAdd{EntryID: id, File: pin, Title: strings.TrimSpace(title), Note: strings.TrimSpace(note), Actor: by, Tasks: tasks, AddedAt: now.UTC()}
	return e, model.ValidateSchema(e)
}

func Capture(ctx context.Context, p store.Project, by model.Actor, event model.TypedEvent) (model.PacketRef, error) {
	raw, err := model.EncodeEvent(event)
	if err != nil {
		return model.PacketRef{}, err
	}
	return store.WriteIntake(ctx, p, store.IntakeRequest{Author: by, Events: []model.Event{raw}})
}

func Entries(p store.Project) ([]reduce.GalleryEntry, error) {
	state, err := store.Load(p)
	if err != nil {
		return nil, err
	}
	snapshot, err := state.Folded()
	if err != nil {
		return nil, err
	}
	return snapshot.Gallery(), nil
}

func List(p store.Project) ([]Shown, error) {
	entries, err := Entries(p)
	if err != nil {
		return nil, err
	}
	out := make([]Shown, 0, len(entries))
	for _, record := range entries {
		if record.Retracted {
			continue
		}
		e := localEntry(p, record)
		kind, _ := Kind(e.Path)
		status := "missing"
		if pin, err := Pin(p.Root, e.Locator); err == nil {
			status = "changed"
			if pin.SameContent(record.Add.File) {
				status = "present"
			}
		}
		out = append(out, Shown{Entry: e, Kind: kind, Status: status})
	}
	return out, nil
}

func Find(p store.Project, id string) (Entry, error) {
	entries, err := Entries(p)
	if err != nil {
		return Entry{}, err
	}
	for _, e := range entries {
		if string(e.Add.EntryID) == id && !e.Retracted {
			return localEntry(p, e), nil
		}
	}
	return Entry{}, ErrNoEntry
}

func localEntry(p store.Project, record reduce.GalleryEntry) Entry {
	a := record.Add
	// Unavailable locators remain visible as ledger records, without a usable
	// local display path. Media reads use the original locator through Open.
	path, _ := ResolvePath(p.Root, a.File.Path)
	return Entry{Locator: a.File.Path, ID: string(a.EntryID), Path: path, Title: a.Title, Note: a.Note, AddedAt: record.AddedAt, AddedBy: a.Actor.ID, SHA256: a.File.SHA256, Length: a.File.Length, Verdict: record.Verdict, VerdictBy: record.Actor, VerdictNote: record.Note, Tasks: a.Tasks}
}

// ResolvePath supplies a display path after checking current containment. It
// cannot authorize a later open; media reads must use Open with the locator.
func ResolvePath(root, locator string) (string, error) {
	if filepath.IsAbs(locator) {
		return locator, nil
	}
	local := filepath.Clean(filepath.FromSlash(locator))
	if !filepath.IsLocal(local) {
		return "", fmt.Errorf("relative gallery locator escapes checkout: %s", locator)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	path, err := filepath.EvalSymlinks(filepath.Join(resolvedRoot, local))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(resolvedRoot, path)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("relative gallery locator escapes checkout: %s", locator)
	}
	return path, nil
}
