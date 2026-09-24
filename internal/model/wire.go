// Package model is the wire vocabulary every other package shares: identities,
// references, packets and bundles, plus the strict encode/decode boundary.
//
// It knows nothing about what an event MEANS. Event payloads arrive here as raw
// JSON and are validated for shape only; their fields are the reducer's business.
// This package imports nothing internal, touches no filesystem, and reads no clock.
package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// WireVersion is the envelope version. A packet or bundle at any other version
// is refused rather than guessed at.
const WireVersion uint16 = 1

type (
	// ID is a 26-character uppercase ULID.
	ID string
	// ProjectID is the identity declared in whosaidso.toml. Declared, never derived.
	ProjectID string
	// Digest is a lowercase 64-character raw SHA-256 hex string.
	Digest string
	// Revision counts a record's admitted revisions, starting at 1.
	Revision uint64
	// Kind is one of the four authored record kinds.
	Kind string
	// EventType names a member of the closed event set. This package does not
	// police membership; it only requires the name to be non-empty.
	EventType string
)

const (
	Claim      Kind = "CLAIM"
	Decision   Kind = "DECISION"
	Instrument Kind = "INSTRUMENT"
	Task       Kind = "TASK"
)

// Actor is exactly one of a known id or a stated reason the actor is unknown.
// Unknown is a legitimate, representable answer: an absent --actor and an absent
// WHOSAIDSO_ACTOR produce the unknown branch, never a fallback to the OS username.
type Actor struct {
	ID            string `json:"id,omitempty"`
	UnknownReason string `json:"unknown_reason,omitempty"`
}

// RecordRef points at one revision of one record, possibly in another project.
type RecordRef struct {
	Project  ProjectID `json:"project"`
	RecordID ID        `json:"record_id"`
	Revision Revision  `json:"revision"`
}

// Selector picks what part of an artifact a reference means.
type Selector struct {
	Kind    string `json:"kind"` // whole | json-pointer
	Pointer string `json:"pointer,omitempty"`
}

// GitPin identifies bytes git already stores. For a committed file this is
// sufficient on its own; a second digest buys nothing.
type GitPin struct {
	ObjectFormat string `json:"object_format"` // sha1 | sha256
	Commit       string `json:"commit"`
	Path         string `json:"path"` // relative to the declared whosaidso root
}

// Locator says where a copy of content can be found, relative to the whosaidso root.
type Locator struct {
	Path string `json:"path"`
}

// ContentPin identifies bytes by their own hash. Required for dirty source,
// generated output before commit, and payloads git only stores a pointer to.
type ContentPin struct {
	SHA256    Digest    `json:"sha256"`
	Length    uint64    `json:"length"`
	MediaType string    `json:"media_type"`
	Locators  []Locator `json:"locators"`
}

// ArtifactRef is a tagged locator: exactly one pin kind is required, and both
// may appear only as corroboration. U01 checks shape; U07 verifies the bytes.
type ArtifactRef struct {
	Kind     string      `json:"kind"` // git | content
	Git      *GitPin     `json:"git,omitempty"`
	Content  *ContentPin `json:"content,omitempty"`
	Selector Selector    `json:"selector"`
}

// Event is one typed thing that happened. Data stays raw here on purpose.
type Event struct {
	Type EventType       `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Packet is what a lane writes to intake. It is durable before the work that
// produced it is acknowledged, and immutable once written.
type Packet struct {
	Version       uint16    `json:"version"`
	Project       ProjectID `json:"project"`
	CommandID     ID        `json:"command_id"`
	RequestDigest Digest    `json:"request_digest"`
	Author        Actor     `json:"author"`
	CapturedAt    time.Time `json:"captured_at"`
	Events        []Event   `json:"events"`
}

// PacketRef binds an admitted packet by identity and exact bytes.
type PacketRef struct {
	CommandID ID     `json:"command_id"`
	Digest    Digest `json:"digest"`
}

// Bundle is one atomic admission. Its CommandID is the transaction id: the
// ADMISSION command's id, not any packet's, since one admission may carry several.
type Bundle struct {
	Version       uint16      `json:"version"`
	Project       ProjectID   `json:"project"`
	Sequence      uint64      `json:"sequence"`
	CommandID     ID          `json:"command_id"`
	Predecessor   ID          `json:"predecessor,omitempty"`
	RequestDigest Digest      `json:"request_digest"`
	Admitter      Actor       `json:"admitter"`
	RecordedAt    time.Time   `json:"recorded_at"`
	Packets       []PacketRef `json:"packets"`
	Events        []Event     `json:"events"`
}

// Packet and Bundle normalize their timestamps to UTC when encoding. Without
// this the SAME INSTANT captured in two zones produced different bytes and
// different digests - one moment with two identities, which breaks the rule
// that two runs compare only when their conditions match. Found by lane E.
//
// The alias type is the standard trick to marshal a struct from inside its own
// MarshalJSON without recursing forever.

func (p Packet) MarshalJSON() ([]byte, error) {
	type alias Packet
	a := alias(p)
	a.CapturedAt = a.CapturedAt.UTC()
	return json.Marshal(a)
}

func (b Bundle) MarshalJSON() ([]byte, error) {
	type alias Bundle
	a := alias(b)
	a.RecordedAt = a.RecordedAt.UTC()
	return json.Marshal(a)
}

// Fault is a refusal with a machine-readable code and enough location to act on.
// Diagnostic text is not an invariant; the code and location are.
type Fault struct {
	Code       string
	Sequence   uint64
	EventIndex int // -1 when the fault is outside any event
	Path       string
	Detail     string
}

func (f *Fault) Error() string {
	var b strings.Builder
	b.WriteString(f.Code)
	if f.Path != "" {
		fmt.Fprintf(&b, " at %s", f.Path)
	}
	if f.EventIndex >= 0 {
		fmt.Fprintf(&b, " in event %d", f.EventIndex)
	}
	if f.Sequence > 0 {
		fmt.Fprintf(&b, " (sequence %d)", f.Sequence)
	}
	if f.Detail != "" {
		fmt.Fprintf(&b, ": %s", f.Detail)
	}
	return b.String()
}

func fault(code, path, detail string) *Fault {
	return &Fault{Code: code, EventIndex: -1, Path: path, Detail: detail}
}

func faultAt(code, path string, idx int, detail string) *Fault {
	return &Fault{Code: code, EventIndex: idx, Path: path, Detail: detail}
}
