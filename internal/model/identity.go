// Identity: minting and validating the ids, digests, actors and ledger
// filenames the wire vocabulary uses.
//
// What belongs here is the syntax of an identity and nothing more. The wire
// types that carry these values, and Fault, live in wire.go; what an identity
// MEANS to the record is the reducer's business.

package model

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

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
	// Without this check ValidID accepted "80000000000000000000000000".
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

// blank reports whether a required semantic string is effectively empty. A
// single space is not a value. This is the defect the rule exists for:
// a "non-empty" rule that a space satisfies is not a rule.
func blank(s string) bool { return Blank(s) }

// SameActor is true only for two KNOWN, identical ids. Two unknowns are not the
// same actor, so an unknown author and an unknown admitter never establish
// self-admission by accident.
func SameActor(a, b Actor) bool {
	// An actor carrying BOTH branches is malformed, and two malformed actors
	// must never compare equal - that would manufacture self-admission out of
	// invalid input.
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
