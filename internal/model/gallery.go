// Gallery events pin external media, record verdicts, and bind task criteria.
// Media bytes and machine-local availability never belong in these payloads.
package model

import (
	"encoding/json"
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
	return ValidDigest(f.SHA256) && ValidDigest(other.SHA256) && f.SHA256 == other.SHA256 && f.Length == other.Length
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
// as a source. Source hashes Inventory and Skipped; Entries correspond to
// Inventory in the same order. Reused binds a legacy date to an existing pin.
type GalleryImport struct {
	Source      Digest               `json:"source"`
	Inventory   []Digest             `json:"inventory"`
	EmptySource bool                 `json:"empty_source,omitempty"`
	Entries     []ID                 `json:"entries"`
	Skipped     []GalleryImportSkip  `json:"skipped,omitempty"`
	Reused      []GalleryImportReuse `json:"reused,omitempty"`
}

func (*GalleryImport) eventPayload()        {}
func (*GalleryImport) EventType() EventType { return "gallery.import" }

// GalleryImportSkip records a non-import without pretending to have a file pin.
type GalleryImportSkip struct {
	LegacyID string `json:"legacy_id"`
	Path     string `json:"path" semantic:"text"`
	Title    string `json:"title"`
	Reason   string `json:"reason"`
}

func (e GalleryImportSkip) validate(p string) error {
	return oneOf(e.Reason, p+".reason", "file missing")
}

// GalleryImportReuse binds an inventory member to an already admitted file pin.
// AddedAt is the legacy date, which need not equal the admitted entry's date.
type GalleryImportReuse struct {
	EntryID ID        `json:"entry_id"`
	AddedAt time.Time `json:"added_at"`
}

// GallerySource covers all pinned identities and every recorded non-import.
// With no skips its encoding stays identical to older sealed imports.
func GallerySource(inventory []Digest, skipped ...GalleryImportSkip) Digest {
	var raw []byte
	if len(skipped) == 0 {
		raw, _ = json.Marshal(inventory)
	} else {
		raw, _ = json.Marshal(struct {
			Inventory []Digest            `json:"inventory"`
			Skipped   []GalleryImportSkip `json:"skipped"`
		}{inventory, skipped})
	}
	return HashBytes(raw)
}

// GalleryImportIdentity identifies one legacy entry independently of its list,
// machine, presentation metadata and newly assigned ledger id.
func GalleryImportIdentity(file GalleryFile, addedAt time.Time) Digest {
	raw, _ := json.Marshal(struct {
		File    GalleryFile `json:"file"`
		AddedAt time.Time   `json:"added_at"`
	}{file, addedAt.UTC()})
	return HashBytes(raw)
}
