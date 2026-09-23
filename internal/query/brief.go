package query

// The brief: a concise text reading of one Answer, one short block per record.
// It is built from the answer's own JSON export, never from the Go values, so
// it cannot show a fact the JSON lacks. Every value it prints is a BriefFact
// carrying the JSON path it was read from; agreement with --json is defined
// and tested on those facts (brief_test.go). Section layout lives in
// brief_sections.go; this file holds the path cursor and the line writer.

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// BriefFact is one shown value. Path uses the JSON-quoted key and [i] index
// encoding of the export. Value is the JSON leaf, or for a count the length of
// the array at Path. Prefix marks a string shown truncated, never altered.
type BriefFact struct {
	Path    []string
	Value   string
	Count   bool
	Prefix  bool
	Display string
}

// cur is a position in the decoded export. A missing position is nil.
type cur struct {
	v    any
	path []string
}

func (c cur) at(steps ...any) cur {
	for _, step := range steps {
		switch k := step.(type) {
		case string:
			m, _ := c.v.(map[string]any)
			quoted, _ := json.Marshal(k)
			c = cur{m[k], append(append([]string{}, c.path...), string(quoted))}
		case int:
			xs, _ := c.v.([]any)
			var v any
			if k < len(xs) {
				v = xs[k]
			}
			c = cur{v, append(append([]string{}, c.path...), "["+strconv.Itoa(k)+"]")}
		}
	}
	return c
}

func (c cur) ok() bool { return c.v != nil }
func (c cur) items() []cur {
	xs, _ := c.v.([]any)
	out := []cur{}
	for i := range xs {
		out = append(out, c.at(i))
	}
	return out
}
func (c cur) text() string { s, _ := c.v.(string); return s }

type briefWriter struct {
	out   strings.Builder
	facts []BriefFact
}

// line writes one line. A string piece is literal layout; a cur piece is a
// fact shown whole; a prefix piece is a fact shown truncated; a count piece
// is the length of an array. A missing cur prints UNKNOWN, never a blank.
func (b *briefWriter) line(indent int, pieces ...any) {
	b.out.WriteString(strings.Repeat("  ", indent))
	for i, piece := range pieces {
		if i > 0 {
			b.out.WriteByte(' ')
		}
		switch p := piece.(type) {
		case string:
			b.out.WriteString(p)
		case cur:
			b.out.WriteString(b.fact(p, false))
		case prefix:
			b.out.WriteString(b.fact(cur(p), true))
		case count:
			xs, _ := p.v.([]any)
			n := strconv.Itoa(len(xs))
			b.facts = append(b.facts, BriefFact{Path: p.path, Value: n, Count: true, Display: n})
			b.out.WriteString(n)
		}
	}
	b.out.WriteByte('\n')
}

type prefix cur
type count cur

const briefWidth = 72

func (b *briefWriter) fact(c cur, truncate bool) string {
	if !c.ok() {
		return "UNKNOWN(absent)"
	}
	encoded, _ := json.Marshal(c.v) // decoded JSON values always re-encode
	display := string(encoded)
	if s, isString := c.v.(string); isString {
		shown, cut := s, false
		if truncate {
			shown = strings.SplitN(s, "\n", 2)[0]
			if utf8.RuneCountInString(shown) > briefWidth {
				shown = string([]rune(shown)[:briefWidth])
			}
			if cut = shown != s; cut {
				shown = strings.TrimRight(shown, " ") // a cut never ends in the space it fell on
			}
		}
		display = safe(shown)
		if cut {
			display += "…"
		}
		truncate = cut
	} else {
		truncate = false
	}
	b.facts = append(b.facts, BriefFact{Path: c.path, Value: string(encoded), Prefix: truncate, Display: display})
	return display
}

// safe prints a string bare when every rune is printable and it has no edge
// spaces; otherwise it is JSON-quoted, so authored text cannot fake a line.
func safe(s string) string {
	plain := s != "" && strings.TrimSpace(s) == s && !strings.ContainsRune(s, '"')
	for _, r := range s {
		plain = plain && unicode.IsPrint(r) && r != '…'
	}
	if plain {
		return s
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(s)
	return strings.TrimSuffix(buf.String(), "\n")
}

// RenderBrief writes the concise reading. --json keeps full detail.
func RenderBrief(w io.Writer, answer Answer) error {
	b, err := brief(answer)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, b.out.String())
	return err
}

func brief(answer Answer) (*briefWriter, error) {
	var encoded bytes.Buffer
	if err := RenderJSON(&encoded, answer); err != nil {
		return nil, err
	}
	return briefOf(encoded.Bytes())
}

// BriefOf renders the brief of an answer's JSON export and returns the facts
// it shows, each with the path it was read from, so a caller holding only the
// export can check the text against it.
func BriefOf(exported []byte) (string, []BriefFact, error) {
	b, err := briefOf(exported)
	if err != nil {
		return "", nil, err
	}
	return b.out.String(), b.facts, nil
}

func briefOf(exported []byte) (*briefWriter, error) {
	decoder := json.NewDecoder(bytes.NewReader(exported))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	b := &briefWriter{}
	root := cur{v: value}
	briefHeader(b, root, root.at("command"))
	briefBody(b, root, root.at("command").text())
	b.line(0, "full detail: add --json")
	return b, nil
}

// briefHeader opens a brief with its name (the command or view) and watermark.
func briefHeader(b *briefWriter, a, name cur) {
	mark := a.at("watermark")
	head := mark.at("head")
	headPieces := []any{"head", head.at("command_id"), "at", head.at("recorded_at")}
	if !head.at("command_id").ok() {
		headPieces = []any{"head", head.at("state"), prefix(head.at("reason"))}
	}
	b.line(0, append([]any{name, a.at("project"), a.at("result"), "| watermark sequence", mark.at("sequence"),
		"bundles", mark.at("bundles"), "events", mark.at("events")}, headPieces...)...)
	if a.at("reason").ok() {
		b.line(0, "reason:", a.at("reason"))
	}
}
