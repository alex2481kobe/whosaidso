package model

// Deterministic JSON encoding, ordered parsing, and exact-byte hashing live here.
// Envelope decoding lives in codec.go; shared field checks live in validation.go.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
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
// machine. That is what lets a digest mean something.
//
// Array order is preserved (it carries meaning) and numeric tokens are kept
// exactly as written. This is a local deterministic convention, not a claim of
// full RFC 8785 canonicalisation.
func Encode(v any) ([]byte, error) {
	// encoding/json silently replaces invalid UTF-8 with U+FFFD. That makes an
	// invalid byte and a real U+FFFD produce IDENTICAL bytes and one digest -
	// two different inputs with one identity, which is the collision a digest
	// exists to prevent. Found by lane E. Checking the marshalled bytes came too
	// late: by then "holder-\xff" was already a different, valid actor. So the
	// value is checked BEFORE marshalling, naming the field.
	if err := refuseInvalidUTF8(reflect.ValueOf(v), "$", 0); err != nil {
		return nil, err
	}
	first, err := json.Marshal(v)
	if err != nil {
		return nil, fault("invalid-field", "", "value cannot be encoded as JSON: "+err.Error())
	}
	// Backstop for bytes a custom MarshalJSON emits, which the walk cannot see.
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

var rawMessageType = reflect.TypeOf(json.RawMessage(nil))

// refuseInvalidUTF8 walks what json.Marshal would write: exported fields under
// their JSON names, map keys and values, slices, and raw JSON bytes (other byte
// slices become base64 and hold no strings). Past json.Marshal's own cycle
// depth the walk stops and leaves the cycle for json.Marshal to refuse.
func refuseInvalidUTF8(v reflect.Value, path string, depth int) error {
	if depth > 1000 {
		return nil
	}
	bad := func(at string) error {
		return fault("invalid-field", at, "value contains invalid UTF-8; it would be silently rewritten")
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			return refuseInvalidUTF8(v.Elem(), path, depth+1)
		}
	case reflect.String:
		if !utf8.ValidString(v.String()) {
			return bad(path)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			// json promotes an embedded struct's fields even when it is unexported.
			if !f.IsExported() && !f.Anonymous || name == "-" {
				continue
			}
			at := path
			if name != "" {
				at += "." + name
			} else if !f.Anonymous {
				at += "." + f.Name
			}
			if err := refuseInvalidUTF8(v.Field(i), at, depth+1); err != nil {
				return err
			}
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			key := fmt.Sprint(iter.Key())
			if !utf8.ValidString(key) {
				return bad(path + ".<map key>")
			}
			if err := refuseInvalidUTF8(iter.Value(), path+"."+key, depth+1); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		if v.Type() == rawMessageType {
			if !utf8.Valid(v.Bytes()) {
				return bad(path)
			}
			return nil
		}
		for i := 0; i < v.Len(); i++ {
			if err := refuseInvalidUTF8(v.Index(i), fmt.Sprintf("%s[%d]", path, i), depth+1); err != nil {
				return err
			}
		}
	}
	return nil
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
