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

// Pin streams the actual file. Stat before opening avoids blocking on an
// already-present FIFO; the opened handle is checked again before hashing.
func Pin(path string) (model.GalleryFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return model.GalleryFile{}, err
	}
	if !info.Mode().IsRegular() {
		return model.GalleryFile{}, fmt.Errorf("not a regular file: %s", path)
	}
	f, err := openMedia(path)
	if err != nil {
		return model.GalleryFile{}, err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return model.GalleryFile{}, err
	}
	if !info.Mode().IsRegular() {
		return model.GalleryFile{}, fmt.Errorf("not a regular file: %s", path)
	}
	return Hash(f, path)
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
	path = ResolvePath(dir, path)
	pin, err := Pin(filepath.Clean(path))
	if err != nil {
		return model.GalleryAdd{}, err
	}
	if rel, err := filepath.Rel(dir, pin.Path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		pin.Path = filepath.ToSlash(rel)
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
		if pin, err := Pin(e.Path); err == nil {
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
	path := ResolvePath(p.Root, a.File.Path)
	return Entry{ID: string(a.EntryID), Path: path, Title: a.Title, Note: a.Note, AddedAt: record.AddedAt, AddedBy: a.Actor.ID, SHA256: a.File.SHA256, Length: a.File.Length, Verdict: record.Verdict, VerdictBy: record.Actor, VerdictNote: record.Note, Tasks: a.Tasks}
}

// ResolvePath interprets a media locator in the caller's checkout.
func ResolvePath(root, locator string) string {
	if filepath.IsAbs(locator) {
		return locator
	}
	return filepath.Join(root, filepath.FromSlash(locator))
}
