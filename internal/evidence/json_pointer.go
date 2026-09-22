package evidence

// Strict artifact JSON decoding and RFC 6901 pointer traversal live here.
// Reading interpretation, measurement metadata, and byte retrieval do not.
// This file stays below 200 lines to keep JSON mechanics separate from readings.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ---- strict JSON ---------------------------------------------------------

// decodeJSON keeps exact number text and refuses duplicate keys. A duplicate key
// would give one pointer two values, and whichever one won would be an accident
// of the parser rather than something the artifact says.
func decodeJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	v, err := parseValue(dec, "$")
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fault("invalid-json", "$", "trailing content after the top-level value")
	}
	return v, nil
}

func parseValue(dec *json.Decoder, at string) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, fault("invalid-json", at, "artifact is not valid JSON")
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return parseObject(dec, at)
		case '[':
			return parseArray(dec, at)
		}
		return nil, fault("invalid-json", at, "unexpected delimiter")
	default:
		return tok, nil
	}
}

func parseObject(dec *json.Decoder, at string) (any, error) {
	obj := map[string]any{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, fault("invalid-json", at, "artifact is not valid JSON")
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fault("invalid-json", at, "object key is not a string")
		}
		if _, dup := obj[key]; dup {
			return nil, fault("invalid-json", at+"/"+key, "duplicate key: one pointer would have two values")
		}
		v, err := parseValue(dec, at+"/"+key)
		if err != nil {
			return nil, err
		}
		obj[key] = v
	}
	if _, err := dec.Token(); err != nil {
		return nil, fault("invalid-json", at, "unterminated object")
	}
	return obj, nil
}

func parseArray(dec *json.Decoder, at string) (any, error) {
	arr := []any{}
	for dec.More() {
		v, err := parseValue(dec, fmt.Sprintf("%s/%d", at, len(arr)))
		if err != nil {
			return nil, err
		}
		arr = append(arr, v)
	}
	if _, err := dec.Token(); err != nil {
		return nil, fault("invalid-json", at, "unterminated array")
	}
	return arr, nil
}

// pointerValue walks an RFC 6901 pointer. The empty pointer is the document
// root. Escapes are unescaped ~1 before ~0, because doing it the other way
// turns "~01" into "/" instead of the "~1" the author wrote.
func pointerValue(root any, ptr string) (value any, parent map[string]any, ok bool, err error) {
	if ptr == "" {
		return root, nil, true, nil
	}
	if !strings.HasPrefix(ptr, "/") {
		return nil, nil, false, fault("invalid-field", "selector.pointer", "JSON pointer must begin with a slash")
	}
	cur := root
	for _, raw := range strings.Split(ptr[1:], "/") {
		token, err := unescapeToken(raw)
		if err != nil {
			return nil, nil, false, err
		}
		switch node := cur.(type) {
		case map[string]any:
			next, found := node[token]
			if !found {
				return nil, nil, false, nil
			}
			parent, cur = node, next
		case []any:
			i, err := arrayIndex(token)
			if err != nil || i >= len(node) {
				return nil, nil, false, nil
			}
			parent, cur = nil, node[i]
		default:
			return nil, nil, false, nil
		}
	}
	return cur, parent, true, nil
}

func unescapeToken(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '~' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) {
			return "", fault("invalid-field", "selector.pointer", "pointer ends in an incomplete escape")
		}
		switch s[i+1] {
		case '0':
			b.WriteByte('~')
		case '1':
			b.WriteByte('/')
		default:
			return "", fault("invalid-field", "selector.pointer", "invalid pointer escape ~"+string(s[i+1]))
		}
		i++
	}
	return b.String(), nil
}

func arrayIndex(s string) (int, error) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, errors.New("not a canonical index")
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, errors.New("not a canonical index")
		}
	}
	return strconv.Atoi(s)
}
