package main

// This file holds the skeleton `whosaidso template EVENT-TYPE` starts from: one
// event built by reflection over the model's payload type, so a field added to
// the schema appears in the template without anyone editing it. The facts
// reflection cannot see (enum members, unions, minted ids) live in
// template_schema.go; the verb, its bind flags and capture live in
// template_bind.go.

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

type templateNote struct {
	Path   []string
	Kind   string // "choose", "optional" or "minted"
	Union  templateUnion
	Detail string
}

type templateObject []templateMember
type templateMember struct {
	key   string
	value any
}

type templateBuilder struct {
	event model.EventType
	notes []templateNote
}

// templateEventList is every event type a template can be printed for.
func templateEventList() string {
	var b strings.Builder
	for i, event := range templateEvents {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(string(event.EventType()))
	}
	return b.String()
}

// buildTemplate renders one event type's skeleton and the notes that say which
// keys are choices, optional or minted.
func buildTemplate(eventType model.EventType) ([]byte, []templateNote, error) {
	body, notes, err := buildTemplateTree(eventType)
	if err != nil {
		return nil, nil, err
	}
	out, err := renderTemplate(eventType, body)
	return out, notes, err
}

// buildTemplateTree is one event type's skeleton as the tree a bound template
// fills (template_tree.go), with its notes.
func buildTemplateTree(eventType model.EventType) (any, []templateNote, error) {
	var payload reflect.Type
	for _, event := range templateEvents {
		if event.EventType() == eventType {
			payload = reflect.TypeOf(event).Elem()
		}
	}
	if payload == nil {
		return nil, nil, usageError("unknown event type %q; run whosaidso help template for the list", eventType)
	}
	b := &templateBuilder{event: eventType}
	return b.walk(payload, nil, reflect.StructField{}), b.notes, nil
}

// renderTemplate writes the one-event array a capture reads, indented.
func renderTemplate(eventType model.EventType, body any) ([]byte, error) {
	var buf bytes.Buffer
	writeTemplateJSON(&buf, []any{templateObject{{"type", string(eventType)}, {"data", body}}})
	var out bytes.Buffer
	if err := json.Indent(&out, buf.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

func templatePath(path []string) string {
	var b strings.Builder
	for _, part := range path {
		if part == "0" {
			b.WriteString("[0]")
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(part)
	}
	return b.String()
}

func (b *templateBuilder) walk(t reflect.Type, path []string, field reflect.StructField) any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	here := append([]string(nil), path...)
	switch t {
	case reflect.TypeOf(time.Time{}):
		return "<time: RFC 3339 in UTC, e.g. 2026-09-23T12:00:00Z>"
	case reflect.TypeOf(json.Number("")):
		return "<number: a JSON decimal number, not a string>"
	case reflect.TypeOf(model.ID("")):
		for _, minted := range templateMints[b.event] {
			if minted == templatePath(here) {
				id, err := model.NewID(time.Now(), rand.Reader)
				if err == nil {
					b.notes = append(b.notes, templateNote{Path: here, Kind: "minted", Detail: templateMintNotes[b.event]})
					return string(id)
				}
			}
		}
		// The noun is the field naming the id: a list element (event_packets[0])
		// takes its list's name, a record_id its owner's, and a record_id in
		// a list of references (context_refs[0].record_id) is any record.
		noun := here[len(here)-1]
		if noun == "0" && len(here) > 1 {
			noun = strings.TrimSuffix(here[len(here)-2], "s")
		}
		if noun == "record_id" && len(here) > 1 {
			noun = here[len(here)-2]
			if noun == "0" {
				noun = "record"
			}
		}
		return "<id: ULID of the existing " + strings.ReplaceAll(strings.TrimSuffix(noun, "_id"), "_", " ") + "; look it up>"
	case reflect.TypeOf(model.ProjectID("")):
		return "<project: the id declared in whosaidso.toml>"
	case reflect.TypeOf(model.Digest("")):
		return "<digest: lowercase SHA-256 hex of the exact bytes>"
	case reflect.TypeOf(model.Revision(0)):
		return "<revision: a whole number, 1 or more>"
	}
	if members, ok := templateEnumTypes[t]; ok {
		return "<one of: " + strings.Join(members, " | ") + ">"
	}
	switch t.Kind() {
	case reflect.Struct:
		if strings.HasPrefix(t.Name(), "Availability[") {
			return b.object(t, here, templateAvailability, true)
		}
		union, isUnion := templateUnions[t]
		return b.object(t, here, union, isUnion)
	case reflect.Map:
		key := "<key: a name>"
		if t.Key() == reflect.TypeOf(model.ID("")) {
			key = "<id: ULID of a packet this review names>"
		}
		return templateObject{{key, b.walk(t.Elem(), append(here, key), field)}}
	case reflect.Slice, reflect.Array:
		return []any{b.walk(t.Elem(), append(here, "0"), field)}
	case reflect.Bool:
		return "<bool: true | false>"
	case reflect.Int:
		return "<integer>"
	case reflect.Uint, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "<count: a whole number>"
	case reflect.String:
		return templateString(field)
	}
	return "<unsupported>"
}

func templateString(field reflect.StructField) string {
	if tag := field.Tag.Get("semantic"); tag == "text" || tag == "texts" {
		return "<text: authored words, not blank>"
	}
	switch field.Name {
	case "ID":
		return "<actor-id: who, e.g. reviewer>"
	case "UnknownReason":
		return "<text: why the actor is unknown>"
	case "Path", "PreviousLocation", "SourcePaths":
		return "<path: project-relative, forward slashes>"
	case "Commit":
		return "<commit: the full lowercase hex object name>"
	case "MediaType":
		return "<media-type: e.g. application/json>"
	case "Pointer":
		return "<pointer: a JSON pointer, e.g. /results/0>"
	}
	return "<text>"
}

func (b *templateBuilder) object(t reflect.Type, path []string, union templateUnion, isUnion bool) templateObject {
	branch := map[string]bool{}
	for _, keys := range union.branches {
		for _, key := range keys {
			branch[key] = true
		}
	}
	if isUnion {
		b.notes = append(b.notes, templateNote{Path: path, Kind: "choose", Union: union})
	}
	var out templateObject
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("json"), ",")
		if tag[0] == "" || tag[0] == "-" {
			continue
		}
		at := append(append([]string(nil), path...), tag[0])
		// An untagged union's keys are exactly its branch keys; a narrowed
		// union (templateFieldUnions) leaves the dropped members' keys out.
		if isUnion && union.tag == "" && !branch[tag[0]] {
			continue
		}
		here := templateField{t, f.Name}
		var value any
		if members, ok := templateEnumFields[here]; ok || (isUnion && tag[0] == union.tag) {
			if !ok {
				members = union.members
			}
			value = "<one of: " + strings.Join(members, " | ") + ">"
		} else if narrowed, ok := templateFieldUnions[here]; ok {
			value = b.object(indirect(f.Type), at, narrowed, true)
		} else {
			value = b.walk(f.Type, at, f)
		}
		if strings.Contains(f.Tag.Get("json"), ",omitempty") && !branch[tag[0]] {
			b.notes = append(b.notes, templateNote{Path: at, Kind: "optional"})
		}
		out = append(out, templateMember{tag[0], value})
	}
	return out
}

func indirect(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func writeTemplateJSON(buf *bytes.Buffer, v any) {
	switch t := v.(type) {
	case templateObject:
		buf.WriteByte('{')
		for i, m := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeTemplateJSON(buf, m.key)
			buf.WriteByte(':')
			writeTemplateJSON(buf, m.value)
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, item := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeTemplateJSON(buf, item)
		}
		buf.WriteByte(']')
	default:
		// No HTML escaping: a placeholder's "<" must read as "<" in the template.
		enc := json.NewEncoder(buf)
		enc.SetEscapeHTML(false)
		enc.Encode(t)
		buf.Truncate(buf.Len() - 1)
	}
}

func renderTemplateNotes(event model.EventType, notes []templateNote) string {
	var b strings.Builder
	fmt.Fprintf(&b, "whosaidso template %s: fill every \"<...>\" placeholder; paths below are under data.\n", event)
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].Kind < notes[j].Kind })
	for _, n := range notes {
		at := templatePath(n.Path)
		switch n.Kind {
		case "minted":
			fmt.Fprintf(&b, "minted   %s is a fresh id", at)
			if n.Detail != "" {
				fmt.Fprintf(&b, " (%s)", n.Detail)
			}
			b.WriteByte('\n')
		case "optional":
			if n.Detail == "absent" {
				fmt.Fprintf(&b, "optional %s: absent; --set or --pin %s adds it\n", at, at)
				continue
			}
			fmt.Fprintf(&b, "optional %s: delete the key to omit it\n", at)
		case "choose":
			if at == "" {
				at = "(the event)"
			}
			if len(n.Union.members) == 1 && n.Union.tag == "" {
				fmt.Fprintf(&b, "only     %s: %s only; %s\n", at, n.Union.members[0], n.Union.note)
				continue
			}
			fmt.Fprintf(&b, "choose   %s", at)
			if n.Union.tag != "" {
				fmt.Fprintf(&b, ".%s", n.Union.tag)
			}
			parts := []string{strings.Join(n.Union.members, " | ")}
			for _, member := range n.Union.members {
				keys := n.Union.branches[member]
				if len(keys) == 0 {
					keys = []string{"none of the choice keys"}
				}
				parts = append(parts, member+" keeps "+strings.Join(keys, ", "))
			}
			if n.Union.note != "" {
				parts = append(parts, n.Union.note)
			}
			fmt.Fprintf(&b, ": %s", strings.Join(parts, "; "))
			b.WriteByte('\n')
		}
	}
	return b.String()
}
