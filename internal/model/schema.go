package model

// Schema entry points and recursive JSON-shape/value validation live here.
// Domain-specific validators stay with their types; primitive checks live in validation.go.

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode"
)

// ValidateSchema checks a programmatically constructed payload at the same
// boundary as DecodeEvent. It does not resolve references or admit facts.
func ValidateSchema(v any) error {
	// Before json.Marshal can rewrite invalid UTF-8 as U+FFFD; EncodeEvent
	// reaches its own marshal only through here.
	if err := refuseInvalidUTF8(reflect.ValueOf(v), "payload"); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return invalid("payload", err.Error())
	}
	tree, err := parseOrdered(b)
	if err != nil {
		return err
	}
	if err = checkJSONShape(tree, reflect.TypeOf(v), "payload"); err != nil {
		return err
	}
	return validateValue(reflect.ValueOf(v), "payload")
}

type schemaValidator interface{ validate(string) error }

// Reflection here enforces JSON presence/types, never reference discovery.
// encoding/json alone accepts case aliases, null zero-values and missing fields.
func checkJSONShape(tree any, t reflect.Type, p string) error {
	if t == nil || tree == nil {
		return invalid(p, "null is not a value; use explicit availability")
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeOf(time.Time{}) {
		if _, ok := tree.(string); !ok {
			return invalid(p, "expected time text")
		}
		return nil
	}
	if t == reflect.TypeOf(json.Number("")) {
		if _, ok := tree.(json.Number); !ok {
			return invalid(p, "expected JSON number")
		}
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		fields, ok := tree.([]member)
		if !ok {
			return invalid(p, "expected object")
		}
		known := map[string]reflect.StructField{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			if tag != "" && tag != "-" {
				known[tag] = f
			}
		}
		seen := map[string]bool{}
		for _, m := range fields {
			if strings.TrimSpace(strings.Map(func(r rune) rune {
				if unicode.Is(unicode.Cf, r) || r == '\uFEFF' {
					return -1
				}
				return r
			}, m.key)) == "status" {
				return invalid(p+".status", "status is a projection, never writable")
			}
			f, ok := known[m.key]
			if !ok {
				return invalid(p+"."+m.key, "unknown field")
			}
			seen[m.key] = true
			if f.Tag.Get("semantic") == "text" {
				if text, ok := m.value.(string); ok && Blank(text) {
					return invalid(p+"."+m.key, "whitespace-only is empty")
				}
			}
			if err := checkJSONShape(m.value, f.Type, p+"."+m.key); err != nil {
				return err
			}
		}
		for name, f := range known {
			if !seen[name] && !strings.Contains(f.Tag.Get("json"), ",omitempty") {
				return invalid(p+"."+name, "required field is missing")
			}
		}
	case reflect.Map:
		fields, ok := tree.([]member)
		if !ok {
			return invalid(p, "expected object")
		}
		for _, m := range fields {
			if Blank(m.key) || m.key == "status" {
				return invalid(p+"."+m.key, "blank or reserved map key")
			}
			if err := checkJSONShape(m.value, t.Elem(), p+"."+m.key); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		items, ok := tree.([]any)
		if !ok {
			return invalid(p, "expected array")
		}
		for i, v := range items {
			if err := checkJSONShape(v, t.Elem(), fmt.Sprintf("%s[%d]", p, i)); err != nil {
				return err
			}
		}
	case reflect.String:
		if _, ok := tree.(string); !ok {
			return invalid(p, "expected string")
		}
	case reflect.Bool:
		if _, ok := tree.(bool); !ok {
			return invalid(p, "expected boolean")
		}
	case reflect.Int, reflect.Int64, reflect.Uint, reflect.Uint32, reflect.Uint64, reflect.Uint16:
		if _, ok := tree.(json.Number); !ok {
			return invalid(p, "expected number")
		}
	default:
		return invalid(p, "unsupported schema type")
	}
	return nil
}

func validateValue(v reflect.Value, p string) error {
	if !v.IsValid() {
		return invalid(p, "missing value")
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		return validateValue(v.Elem(), p)
	}
	x := v.Interface()
	if s, ok := x.(schemaValidator); ok {
		if err := s.validate(p); err != nil {
			return err
		}
	}
	switch s := x.(type) {
	case ID:
		if !ValidID(s) {
			return invalid(p, "not a ULID")
		}
	case ProjectID:
		if Blank(string(s)) {
			return invalid(p, "empty project")
		}
	case Digest:
		if !ValidDigest(s) {
			return invalid(p, "not lowercase SHA-256")
		}
	case Revision:
		if s == 0 {
			return invalid(p, "revision starts at 1")
		}
	case Actor:
		if err := validActor(s, p); err != nil {
			return err
		}
		if Blank(s.ID) && Blank(s.UnknownReason) {
			return invalid(p, "actor identity or unknown reason is blank")
		}
	case ArtifactRef:
		if err := ValidateArtifactRef(s, p); err != nil {
			return err
		}
	case GitPin:
		if err := validateCommit(s.ObjectFormat, s.Commit, p); err != nil {
			return err
		}
		if err := relativePath(s.Path, p+".path"); err != nil {
			return err
		}
	case ContentPin:
		if Blank(s.MediaType) {
			return invalid(p+".media_type", "blank media type")
		}
	case Locator:
		if err := relativePath(s.Path, p+".path"); err != nil {
			return err
		}
	case Selector:
		if err := oneOf(s.Kind, p+".kind", "whole", "json-pointer"); err != nil {
			return err
		}
		if s.Kind == "whole" && s.Pointer != "" {
			return invalid(p, "whole selector takes no pointer")
		}
		if s.Kind == "json-pointer" && s.Pointer != "" {
			if s.Pointer[0] != '/' {
				return invalid(p+".pointer", "JSON pointer must begin with slash")
			}
			for i := 0; i < len(s.Pointer); i++ {
				if s.Pointer[i] == '~' {
					if i+1 == len(s.Pointer) || (s.Pointer[i+1] != '0' && s.Pointer[i+1] != '1') {
						return invalid(p+".pointer", "invalid JSON pointer escape")
					}
					i++
				}
			}
		}
	case time.Time:
		if s.IsZero() {
			return invalid(p, "missing time")
		}
		return nil
	case json.Number:
		_, err := DecimalRat(s)
		return err
	}
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			if tag == "" || tag == "-" {
				continue
			}
			fv := v.Field(i)
			if strings.Contains(f.Tag.Get("json"), ",omitempty") && fv.IsZero() {
				continue
			}
			at := p + "." + tag
			if f.Tag.Get("semantic") == "text" && Blank(fv.String()) {
				return invalid(at, "whitespace-only is empty")
			}
			if f.Tag.Get("semantic") == "texts" {
				for j := 0; j < fv.Len(); j++ {
					if Blank(fv.Index(j).String()) {
						return invalid(fmt.Sprintf("%s[%d]", at, j), "whitespace-only is empty")
					}
				}
			}
			if err := validateValue(fv, at); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if err := validateValue(v.Index(i), fmt.Sprintf("%s[%d]", p, i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			if err := validateValue(iter.Value(), p+"."+iter.Key().String()); err != nil {
				return err
			}
		}
	}
	return nil
}
