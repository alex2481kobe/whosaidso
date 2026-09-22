package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// HashBytes is the one hash in the system: raw SHA-256 over exact bytes.
func HashBytes(b []byte) Digest {
	sum := sha256.Sum256(b)
	return Digest(hex.EncodeToString(sum[:]))
}

// ---- deterministic encoding ---------------------------------------------

// Encode writes UTF-8 JSON with recursively sorted object keys, two-space
// indentation, and one trailing newline. Same value in, same bytes out, on any
// machine — which is what lets a digest mean something.
//
// Array order is preserved (it carries meaning) and numeric tokens are kept
// exactly as written. This is a local deterministic convention, not a claim of
// full RFC 8785 canonicalisation.
func Encode(v any) ([]byte, error) {
	first, err := json.Marshal(v)
	if err != nil {
		return nil, fault("invalid-field", "", "value cannot be encoded as JSON: "+err.Error())
	}
	// encoding/json silently replaces invalid UTF-8 with U+FFFD. That makes an
	// invalid byte and a real U+FFFD produce IDENTICAL bytes and one digest -
	// two different inputs with one identity, which is the collision a digest
	// exists to prevent. Refuse instead. Found by lane E.
	if !utf8.Valid(first) {
		return nil, fault("invalid-field", "", "value contains invalid UTF-8; it would be silently rewritten")
	}
	tree, err := parseOrdered(first)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	writeOrdered(&out, tree, 0)
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// member is one key/value pair, kept so we can sort deliberately rather than
// relying on Go's map iteration.
type member struct {
	key   string
	value any
}

// parseOrdered decodes into a shape that preserves number spelling and lets us
// sort keys ourselves. It also refuses duplicate keys at any depth.
func parseOrdered(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	v, err := parseValue(dec, "$")
	if err != nil {
		return nil, err
	}
	// dec.More() returns false for a stray "}" or "]", so it never saw trailing
	// garbage. Reading the next token does. Found by lane E.
	if _, err := dec.Token(); err != io.EOF {
		return nil, fault("invalid-json", "$", "trailing content after the top-level value")
	}
	return v, nil
}

func parseValue(dec *json.Decoder, path string) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		if err == io.EOF {
			return nil, fault("invalid-json", path, "unexpected end of input")
		}
		return nil, fault("invalid-json", path, err.Error())
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return parseObject(dec, path)
		case '[':
			return parseArray(dec, path)
		}
		return nil, fault("invalid-json", path, "unexpected delimiter")
	default:
		return tok, nil
	}
}

func parseObject(dec *json.Decoder, path string) (any, error) {
	members := []member{}
	seen := map[string]bool{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, fault("invalid-json", path, err.Error())
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fault("invalid-json", path, "object key is not a string")
		}
		// encoding/json silently keeps the last duplicate. A duplicate key means
		// two readers can disagree about the same bytes, so it is refused.
		if seen[key] {
			return nil, fault("invalid-json", path+"."+key, "duplicate object key")
		}
		seen[key] = true
		val, err := parseValue(dec, path+"."+key)
		if err != nil {
			return nil, err
		}
		members = append(members, member{key, val})
	}
	if _, err := dec.Token(); err != nil { // closing brace
		return nil, fault("invalid-json", path, err.Error())
	}
	return members, nil
}

func parseArray(dec *json.Decoder, path string) (any, error) {
	items := []any{}
	for i := 0; dec.More(); i++ {
		val, err := parseValue(dec, fmt.Sprintf("%s[%d]", path, i))
		if err != nil {
			return nil, err
		}
		items = append(items, val)
	}
	if _, err := dec.Token(); err != nil { // closing bracket
		return nil, fault("invalid-json", path, err.Error())
	}
	return items, nil
}

// writeJSONString emits a JSON string literal. strconv.Quote produces GO
// escapes - it renders a NUL as \x00, which is not valid JSON at all, so any
// control character made the whole document unparseable. Found by lane E.
func writeJSONString(w *bytes.Buffer, s string) {
	// SetEscapeHTML(false) keeps <, > and & literal: still deterministic, and
	// the bytes stay readable in a diff.
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	w.Write(bytes.TrimRight(b.Bytes(), "\n"))
}

// writeOrdered renders the normalized tree. Keys sort; arrays do not.
func writeOrdered(w *bytes.Buffer, v any, depth int) {
	pad := func(n int) { w.WriteString(strings.Repeat("  ", n)) }
	switch t := v.(type) {
	case []member:
		if len(t) == 0 {
			w.WriteString("{}")
			return
		}
		sorted := make([]member, len(t))
		copy(sorted, t)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].key < sorted[j].key })
		w.WriteString("{\n")
		for i, m := range sorted {
			pad(depth + 1)
			writeJSONString(w, m.key)
			w.WriteString(": ")
			writeOrdered(w, m.value, depth+1)
			if i < len(sorted)-1 {
				w.WriteByte(',')
			}
			w.WriteByte('\n')
		}
		pad(depth)
		w.WriteByte('}')
	case []any:
		if len(t) == 0 {
			w.WriteString("[]")
			return
		}
		w.WriteString("[\n")
		for i, item := range t {
			pad(depth + 1)
			writeOrdered(w, item, depth+1)
			if i < len(t)-1 {
				w.WriteByte(',')
			}
			w.WriteByte('\n')
		}
		pad(depth)
		w.WriteByte(']')
	case json.Number:
		w.WriteString(t.String()) // exact token, no reformatting
	case string:
		writeJSONString(w, t)
	case bool:
		w.WriteString(strconv.FormatBool(t))
	case nil:
		w.WriteString("null")
	}
}

// ---- strict decoding -----------------------------------------------------

// envelopeKeys lists the exact JSON names each envelope accepts. Go's decoder
// matches case-insensitively, so "Version" lands in Version and neither
// DisallowUnknownFields nor a duplicate-key check notices. An alias that
// overwrites a field is indistinguishable from the real one downstream.
// Found by lane E.
var envelopeKeys = map[string]map[string]bool{
	"packet": {"version": true, "project": true, "command_id": true,
		"request_digest": true, "author": true, "captured_at": true, "events": true},
	"bundle": {"version": true, "project": true, "sequence": true, "command_id": true,
		"predecessor": true, "request_digest": true, "admitter": true,
		"recorded_at": true, "packets": true, "events": true},
}

func strictUnmarshal(b []byte, into any, what string) error {
	// The DECODER accepted invalid UTF-8 even once the encoder refused it: the
	// same collision, entering from the other side. Found by lane E.
	if !utf8.Valid(b) {
		return fault("invalid-json", what, "input contains invalid UTF-8")
	}
	// Three passes on purpose: the ordered parse catches duplicate keys and
	// trailing content; the exact-name check catches case aliases; the struct
	// decoder catches everything else.
	tree, err := parseOrdered(b)
	if err != nil {
		return err
	}
	if allowed, ok := envelopeKeys[what]; ok {
		top, isObject := tree.([]member)
		if !isObject {
			return fault("invalid-json", what, "top-level value is not an object")
		}
		for _, m := range top {
			if !allowed[m.key] {
				return fault("invalid-field", what+"."+m.key,
					"unknown field, or a case variant of a known one")
			}
			// The envelope check stopped at the top level, so a case alias
			// INSIDE an event still overwrote its type. Found by lane E.
			if m.key != "events" {
				continue
			}
			list, isArray := m.value.([]any)
			if !isArray {
				return fault("invalid-field", what+".events", "events is not an array")
			}
			for i, item := range list {
				obj, isObject := item.([]member)
				if !isObject {
					return fault("invalid-field", fmt.Sprintf("%s.events[%d]", what, i), "event is not an object")
				}
				for _, em := range obj {
					if !eventKeys[em.key] {
						return fault("invalid-field",
							fmt.Sprintf("%s.events[%d].%s", what, i, em.key),
							"unknown field, or a case variant of a known one")
					}
				}
			}
		}
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fault("invalid-field", what, err.Error())
	}
	return nil
}

// DecodePacket parses intake bytes, refusing anything it cannot fully account for.
func DecodePacket(b []byte) (Packet, error) {
	var p Packet
	if err := strictUnmarshal(b, &p, "packet"); err != nil {
		return Packet{}, err
	}
	if p.Version != WireVersion {
		return Packet{}, fault("unknown-version", "packet.version",
			fmt.Sprintf("wire version %d, expected %d", p.Version, WireVersion))
	}
	if p.Project == "" {
		return Packet{}, fault("invalid-field", "packet.project", "empty project id")
	}
	if !ValidID(p.CommandID) {
		return Packet{}, fault("invalid-field", "packet.command_id", "not a ULID")
	}
	if !ValidDigest(p.RequestDigest) {
		return Packet{}, fault("invalid-field", "packet.request_digest", "not lowercase sha-256 hex")
	}
	if f := validActor(p.Author, "packet.author"); f != nil {
		return Packet{}, f
	}
	if err := validEvents(p.Events, "packet.events"); err != nil {
		return Packet{}, err
	}
	return p, nil
}

// DecodeBundle parses ledger bytes. A bundle that fails here is never repaired
// by re-encoding it; admitted bytes are what they are.
func DecodeBundle(b []byte) (Bundle, error) {
	var bd Bundle
	if err := strictUnmarshal(b, &bd, "bundle"); err != nil {
		return Bundle{}, err
	}
	if bd.Version != WireVersion {
		return Bundle{}, fault("unknown-version", "bundle.version",
			fmt.Sprintf("wire version %d, expected %d", bd.Version, WireVersion))
	}
	if bd.Project == "" {
		return Bundle{}, fault("invalid-field", "bundle.project", "empty project id")
	}
	if bd.Sequence == 0 {
		return Bundle{}, fault("invalid-field", "bundle.sequence", "sequence starts at 1")
	}
	if !ValidID(bd.CommandID) {
		return Bundle{}, fault("invalid-field", "bundle.command_id", "not a ULID")
	}
	// Only the genesis bundle may stand alone; every other one names its parent,
	// so a gap in the chain is detectable rather than invisible.
	if bd.Sequence == 1 {
		if bd.Predecessor != "" {
			return Bundle{}, fault("ledger-discontinuity", "bundle.predecessor", "sequence 1 cannot have a predecessor")
		}
	} else if !ValidID(bd.Predecessor) {
		return Bundle{}, fault("ledger-discontinuity", "bundle.predecessor", "missing or malformed predecessor")
	}
	if !ValidDigest(bd.RequestDigest) {
		return Bundle{}, fault("invalid-field", "bundle.request_digest", "not lowercase sha-256 hex")
	}
	if f := validActor(bd.Admitter, "bundle.admitter"); f != nil {
		return Bundle{}, f
	}
	for i, pr := range bd.Packets {
		at := fmt.Sprintf("bundle.packets[%d]", i)
		if !ValidID(pr.CommandID) {
			return Bundle{}, fault("invalid-field", at+".command_id", "not a ULID")
		}
		if !ValidDigest(pr.Digest) {
			return Bundle{}, fault("invalid-field", at+".digest", "not lowercase sha-256 hex")
		}
	}
	if err := validEvents(bd.Events, "bundle.events"); err != nil {
		return Bundle{}, err
	}
	return bd, nil
}

// eventKeys is the exact spelling an event object accepts. The envelope check
// stops at the top level, so a case alias INSIDE an event slipped through and
// overwrote its type. Found by lane E.
var eventKeys = map[string]bool{"type": true, "data": true}

func validEvents(events []Event, path string) error {
	if len(events) == 0 {
		return fault("invalid-field", path, "no events; an empty write is not a fact")
	}
	for i, e := range events {
		if e.Type == "" {
			return faultAt("unknown-event", path, i, "empty event type")
		}
		// Shape only. Whether this type is in the closed set, and whether its
		// fields make sense, is U02's job.
		trimmed := bytes.TrimSpace(e.Data)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			return faultAt("invalid-field", path, i, "event data must be a JSON object")
		}
		if _, err := parseOrdered(trimmed); err != nil {
			return err
		}
	}
	return nil
}

func lowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return len(s) > 0
}

// ValidateArtifactRef checks the tagged-locator shape: exactly one pin kind is
// required by Kind, and a second may appear only as corroboration.
func ValidateArtifactRef(a ArtifactRef, path string) error {
	switch a.Kind {
	case "git":
		if a.Git == nil {
			return fault("invalid-field", path+".git", `kind "git" requires a git pin`)
		}
	case "content":
		if a.Content == nil {
			return fault("invalid-field", path+".content", `kind "content" requires a content pin`)
		}
	default:
		return fault("invalid-field", path+".kind", `kind must be "git" or "content"`)
	}
	if a.Git != nil {
		want := 40
		if a.Git.ObjectFormat == "sha256" {
			want = 64
		} else if a.Git.ObjectFormat != "sha1" {
			return fault("invalid-field", path+".git.object_format", `object format must be "sha1" or "sha256"`)
		}
		if len(a.Git.Commit) != want || !lowerHex(a.Git.Commit) {
			// Length alone let "gggg...g" through - a true measurement of the
			// wrong property. Found by lane E.
			return fault("invalid-field", path+".git.commit",
				fmt.Sprintf("commit must be %d lowercase hex characters for %s", want, a.Git.ObjectFormat))
		}
		if a.Git.Path == "" {
			return fault("invalid-field", path+".git.path", "empty path")
		}
	}
	if a.Content != nil {
		if !ValidDigest(a.Content.SHA256) {
			return fault("invalid-field", path+".content.sha256", "not lowercase sha-256 hex")
		}
		if a.Content.MediaType == "" {
			return fault("invalid-field", path+".content.media_type", "empty media type")
		}
	}
	switch a.Selector.Kind {
	case "whole":
		if a.Selector.Pointer != "" {
			return fault("invalid-field", path+".selector.pointer", `"whole" takes no pointer`)
		}
	case "json-pointer":
		// The empty pointer is the JSON root and is legal.
	default:
		return fault("invalid-field", path+".selector.kind", `selector must be "whole" or "json-pointer"`)
	}
	return nil
}
