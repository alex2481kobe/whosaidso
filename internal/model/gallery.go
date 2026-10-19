package model

// Gallery events pin external media, record verdicts, and bind task criteria.
// Media bytes and machine-local availability never belong in these payloads.

import (
	"strings"
	"time"
	"unicode/utf8"
)

type GalleryFile struct {
	SHA256 Digest `json:"sha256"`
	Length uint64 `json:"length"`
	Path   string `json:"path" semantic:"text"`
}

// SameContent compares identities, never retrieval hints.
func (f GalleryFile) SameContent(other GalleryFile) bool {
	return f.SHA256 == other.SHA256 && f.Length == other.Length
}

type GalleryAdd struct {
	AddedAt  time.Time   `json:"added_at"`
	EntryID  ID          `json:"entry_id"`
	File     GalleryFile `json:"file"`
	Title    string      `json:"title" semantic:"text"`
	Note     string      `json:"note,omitempty"`
	Actor    Actor       `json:"actor"`
	Tasks    []RecordRef `json:"tasks"`
	ImportID string      `json:"import_id,omitempty"`
}

func (*GalleryAdd) eventPayload()        {}
func (*GalleryAdd) EventType() EventType { return "gallery.add" }
func (e GalleryAdd) validate(p string) error {
	if Blank(e.Title) || strings.ContainsAny(e.Title, "\r\n") || utf8.RuneCountInString(e.Title) > 120 || utf8.RuneCountInString(e.Note) > 1000 {
		return invalid(p, "gallery needs a one-line title of at most 120 characters and a note of at most 1000")
	}
	return nil
}

type GalleryRetract struct {
	EntryID ID `json:"entry_id"`
}

func (*GalleryRetract) eventPayload()        {}
func (*GalleryRetract) EventType() EventType { return "gallery.retract" }

type GalleryApprove struct {
	EntryID ID     `json:"entry_id"`
	SHA256  Digest `json:"sha256"`
	Actor   Actor  `json:"actor"`
	Note    string `json:"note,omitempty"`
}

func (*GalleryApprove) eventPayload()        {}
func (*GalleryApprove) EventType() EventType { return "gallery.approve" }

type GalleryReject struct {
	EntryID ID     `json:"entry_id"`
	SHA256  Digest `json:"sha256"`
	Actor   Actor  `json:"actor"`
	Note    string `json:"note" semantic:"text"`
}

func (*GalleryReject) eventPayload()        {}
func (*GalleryReject) EventType() EventType { return "gallery.reject" }

// GalleryRequire adds a visual obligation to an existing task criterion.
// It survives task amendments while that criterion identity remains in use.
type GalleryRequire struct {
	Task              RecordRef `json:"task"`
	CriterionID       ID        `json:"criterion_id"`
	CriterionRevision Revision  `json:"criterion_revision"`
	EntryID           ID        `json:"entry_id"`
	Approver          Actor     `json:"approver"`
}

func (*GalleryRequire) eventPayload()        {}
func (*GalleryRequire) EventType() EventType { return "gallery.require" }
func (e GalleryRequire) validate(p string) error {
	if Blank(e.Approver.ID) {
		return invalid(p+".approver", "a gallery requirement needs an identified approver")
	}
	return nil
}

// GalleryImport seals a completed import of one machine list without keeping it
// as a source. Source identifies its locator; Entries must already be admitted.
type GalleryImport struct {
	Source  Digest `json:"source"`
	Entries []ID   `json:"entries"`
}

func (*GalleryImport) eventPayload()        {}
func (*GalleryImport) EventType() EventType { return "gallery.import" }
