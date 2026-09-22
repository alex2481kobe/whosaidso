// Package model is the wire vocabulary every other package shares: identities,
// references, packets and bundles, plus the strict encode/decode boundary.
//
// It knows nothing about what an event MEANS. Event payloads arrive here as raw
// JSON and are validated for shape only; their fields are the reducer's business.
// This package imports nothing internal, touches no filesystem, and reads no clock.
package model

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// WireVersion is the envelope version. A packet or bundle at any other version
// is refused rather than guessed at.
const WireVersion uint16 = 1

type (
	// ID is a 26-character uppercase ULID.
	ID string
	// ProjectID is the identity declared in datum.toml. Declared, never derived.
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
// DATUM_ACTOR produce the unknown branch, never a fallback to the OS username.
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
	Path         string `json:"path"` // relative to the declared datum root
}

// Locator says where a copy of content can be found, relative to the datum root.
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

// ---- identity ------------------------------------------------------------

// crockford is the ULID alphabet: no I, L, O or U, so ids cannot be misread.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var crockfordValue = func() [256]int8 {
	var t [256]int8
	for i := range t {
		t[i] = -1
	}
	for i, c := range crockford {
		t[c] = int8(i)
	}
	return t
}()

// NewID mints a ULID: 48 bits of millisecond time, 80 bits of supplied entropy.
// Time makes ids roughly sortable for humans reading a directory; it is NEVER
// the ordering the system uses. Causal order is ledger sequence, always.
func NewID(at time.Time, entropy io.Reader) (ID, error) {
	ms := at.UTC().UnixMilli()
	if ms < 0 || ms >= 1<<48 {
		return "", fault("invalid-field", "id.time", "timestamp outside the 48-bit ULID range")
	}
	var raw [16]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(ms)<<16)
	if _, err := io.ReadFull(entropy, raw[6:]); err != nil {
		return "", fault("io", "id.entropy", "short read from the entropy source")
	}
	// re-lay the time bytes: entropy overwrote the low two bytes of the 8-byte write
	binary.BigEndian.PutUint16(raw[4:6], uint16(ms))
	binary.BigEndian.PutUint32(raw[0:4], uint32(ms>>16))

	var out [26]byte
	for i := 25; i >= 0; i-- {
		var v int
		bit := (25 - i) * 5
		for k := 0; k < 5; k++ {
			b := bit + k
			if b >= 128 {
				continue
			}
			byteIdx, bitIdx := 15-b/8, b%8
			v |= int((raw[byteIdx]>>bitIdx)&1) << k
		}
		out[i] = crockford[v]
	}
	return ID(out[:]), nil
}

// ValidID reports whether s is a well-formed ULID. Lowercase is refused rather
// than silently upcased, so two spellings can never become two identities.
func ValidID(s ID) bool {
	if len(s) != 26 {
		return false
	}
	// 26 Crockford characters encode 130 bits, but a ULID is 128. Every id above
	// 7ZZZZZZZZZZZZZZZZZZZZZZZZZ overflows, and an overflowing spelling could
	// decode to the same 128 bits as a valid one - two strings, one identity.
	// Found by lane E: ValidID accepted "80000000000000000000000000".
	if s[0] > '7' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if crockfordValue[s[i]] < 0 {
			return false
		}
	}
	return true
}

// ValidDigest reports whether s is lowercase 64-character SHA-256 hex.
func ValidDigest(s Digest) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// SameActor is true only for two KNOWN, identical ids. Two unknowns are not the
// same actor, so an unknown author and an unknown admitter never establish
// self-admission by accident.
// blank reports whether a required semantic string is effectively empty. A
// single space is not a value. This is the defect the contract names directly:
// a "non-empty" rule that a space satisfies is not a rule.
func blank(s string) bool { return strings.TrimSpace(s) == "" }

func SameActor(a, b Actor) bool {
	// An actor carrying BOTH branches is malformed, and two malformed actors
	// must never compare equal - that would manufacture self-admission out of
	// invalid input. Found by lane E.
	if !blank(a.UnknownReason) || !blank(b.UnknownReason) {
		return false
	}
	return !blank(a.ID) && a.ID == b.ID
}

func validActor(a Actor, path string) *Fault {
	known, unknown := !blank(a.ID), !blank(a.UnknownReason)
	switch {
	case known && unknown:
		return fault("invalid-field", path, "actor has both an id and an unknown reason")
	case !known && !unknown:
		return fault("invalid-field", path, "actor needs either an id or a stated reason it is unknown")
	}
	return nil
}

// BundleName is the ledger filename: zero-padded sequence, then the transaction id.
func BundleName(sequence uint64, command ID) (string, error) {
	if sequence == 0 {
		return "", fault("invalid-field", "bundle.sequence", "sequence starts at 1")
	}
	if sequence > 99999999 {
		// Widening the format is a future measured change, not a silent one: an
		// ambiguous filename is worse than a refusal.
		return "", fault("invalid-field", "bundle.sequence", "sequence exceeds the eight-digit filename format")
	}
	if !ValidID(command) {
		return "", fault("invalid-field", "bundle.command_id", "not a ULID")
	}
	return fmt.Sprintf("%08d-%s.json", sequence, command), nil
}
